package officeengine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// x2tEngine is x2t-server.wasm compiled once in the engine process; every
// job runs it in a new instance (15-80 ms to make), on the job's directory.
type x2tEngine struct {
	rt      wazero.Runtime
	mod     wazero.CompiledModule
	limit   uint32 // pages
	missing []string
}

// runtimeConfig is the engine's wazero: a compiler where wazero has one,
// WITHOUT the context watch (a job is stopped by ending its process), each
// instance's memory capped.
func runtimeConfig(cache wazero.CompilationCache, limitPages uint32) wazero.RuntimeConfig {
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(false).WithMemoryLimitPages(limitPages)
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}
	return cfg
}

func newX2T(ctx context.Context, cache wazero.CompilationCache, wasm []byte, limitPages uint32) (*x2tEngine, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, runtimeConfig(cache, limitPages))
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("office engine: x2t does not compile: %w", err)
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	missing, err := instantiateEnv(ctx, rt, mod)
	if err != nil {
		rt.Close(ctx)
		return nil, err
	}
	return &x2tEngine{rt: rt, mod: mod, limit: limitPages, missing: missing}, nil
}

func (x *x2tEngine) close(ctx context.Context) { _ = x.rt.Close(ctx) }

// x2tTail is the most of x2t's own output a failure keeps.
const x2tTail = 2048

// run runs one x2t job in dir (mounted at workDir). A failure is an *Error.
func (x *x2tEngine) run(ctx context.Context, dir string, j x2tJob) error {
	if err := os.WriteFile(filepath.Join(dir, "params.xml"), []byte(j.params()), 0o600); err != nil {
		return &Error{Code: CodeUnknown, What: "io", Err: err}
	}
	out := &tail{max: x2tTail}
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs("x2t", workDir+"/params.xml").
		WithEnv("TMPDIR", workDir+"/tmp").
		WithFSConfig(wazero.NewFSConfig().WithDirMount(dir, workDir)).
		WithStdout(out).WithStderr(out).
		// x2t encrypts with what it reads from /dev/urandom (a protected
		// document): wazero's default source is deterministic.
		WithRandSource(rand.Reader).
		WithSysWalltime().WithSysNanotime().
		WithStartFunctions() // _start is called below
	ictx := experimental.WithMemoryAllocator(withCallCache(ctx), reserveAllocator{})
	inst, err := x.rt.InstantiateModule(ictx, x.mod, cfg)
	if err != nil {
		return &Error{Code: CodeTooLarge, What: "instance", Err: err}
	}
	defer inst.Close(ctx)
	start := inst.ExportedFunction("_start")
	if start == nil {
		return &Error{Code: CodeUnknown, What: "module", Err: errors.New("x2t has no _start (not the server build)")}
	}
	_, err = start.Call(ictx)
	var exit *sys.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		if c := exit.ExitCode(); c != 0 {
			return &Error{Code: codeOfExit(c), What: "x2t-" + strconv.FormatUint(uint64(c), 10), Err: errors.New(out.String())}
		}
	default:
		var miss *missingFunction
		if errors.As(err, &miss) {
			return &Error{Code: CodeCorrupt, What: "x2t-missing", Err: miss}
		}
		// A trap. Out of memory looks like one (malloc fails, x2t aborts):
		// told apart by how far the memory had grown.
		if m := inst.Memory(); m != nil && uint64(m.Size()) >= uint64(x.limit)*65536*95/100 {
			return &Error{Code: CodeTooLarge, What: "memory", Err: err}
		}
		return &Error{Code: CodeCorrupt, What: "x2t-trap", Err: err}
	}
	if st, err := os.Stat(filepath.Join(dir, j.to)); err != nil || st.Size() == 0 {
		return &Error{Code: CodeCorrupt, What: "x2t-empty", Err: errors.New("x2t wrote nothing: " + out.String())}
	}
	return nil
}

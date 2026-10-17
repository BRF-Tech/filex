package officeengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ── The page layout: sdkjs in QuickJS, one warm worker per kind ─────────
//
// layout-host.wasm is QuickJS compiled to WebAssembly; it runs the app's
// layout/worker.js, which loads the editor's scripts (sdkjs) the way a
// Document Server's doctrenderer does and then takes one JSON job per line
// on its stdin, answering one JSON line on its stdout. Loading the scripts is
// most of a worker's cost (2.4-4.8 s with the bytecode, measured), so a
// worker stays warm and takes job after job; it is started again after
// WorkerJobs jobs or once its memory passes WorkerMaxBytes (a warm worker's
// memory grew with every spreadsheet, measured).
//
// The worker sees three directories: /e - the engine's files (the app's
// layout/ from engine.zip, sdkjs/ and fonts/ from its interface bundle), read
// only; /j - the engine process's work directory, where each job's files
// are; /c - the bytecode cache.

// Worker renewal.
const (
	WorkerJobs     = 20
	WorkerMaxBytes = 1536 << 20
)

// layoutEngine is layout-host.wasm compiled once in the engine process.
type layoutEngine struct {
	rt       wazero.Runtime
	mod      wazero.CompiledModule
	files    fs.FS
	work     string
	bytecode string
	workers  map[string]*worker
}

func newLayout(ctx context.Context, cache wazero.CompilationCache, wasm []byte, limitPages uint32, files fs.FS, work, bytecode string) (*layoutEngine, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, runtimeConfig(cache, limitPages))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("office engine: the layout does not compile: %w", err)
	}
	return &layoutEngine{rt: rt, mod: mod, files: files, work: work, bytecode: bytecode, workers: map[string]*worker{}}, nil
}

func (l *layoutEngine) close(ctx context.Context) {
	for k, w := range l.workers {
		w.close(ctx)
		delete(l.workers, k)
	}
	_ = l.rt.Close(ctx)
}

// worker is one warm QuickJS instance with one kind's scripts loaded.
type worker struct {
	kind string
	inst api.Module
	in   *os.File
	out  *bufio.Reader
	outF *os.File
	jobs int
	// exited is closed when the instance's _start returns; exitErr is what
	// it returned.
	exited  chan struct{}
	exitErr error
}

// workerFor is the warm worker of a kind, started when there is none.
func (l *layoutEngine) workerFor(ctx context.Context, kind string) (*worker, error) {
	if w := l.workers[kind]; w != nil {
		return w, nil
	}
	w, err := l.start(ctx, kind)
	if err != nil {
		return nil, err
	}
	l.workers[kind] = w
	return w, nil
}

// retire ends a kind's worker once it has done its share, or grown too
// large; the next job of that kind starts a fresh one.
func (l *layoutEngine) retire(ctx context.Context, w *worker, broken bool) {
	if !broken && w.jobs < WorkerJobs {
		if m := w.inst.Memory(); m == nil || uint64(m.Size()) < WorkerMaxBytes {
			return
		}
	}
	w.close(ctx)
	if l.workers[w.kind] == w {
		delete(l.workers, w.kind)
	}
}

// start loads one kind's scripts into a new instance and waits for it to
// say it is ready.
func (l *layoutEngine) start(ctx context.Context, kind string) (*worker, error) {
	// OS pipes, not io.Pipe: with an io.Pipe as its stdin a worker read its
	// first job and never the second (measured by the app); wazero reads an
	// *os.File as the system does.
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	fsc := wazero.NewFSConfig().WithFSMount(l.files, "/e").WithDirMount(l.work, "/j")
	cacheEnv := ""
	if l.bytecode != "" {
		fsc = fsc.WithDirMount(l.bytecode, "/c")
		cacheEnv = "/c"
	}
	cfg := wazero.NewModuleConfig().WithName("").
		WithArgs("layout-host", "/e/layout/worker.js", "/e", kind).
		WithEnv("LAYOUT_CACHE", cacheEnv).
		WithFSConfig(fsc).
		WithStdin(inR).WithStdout(outW).WithStderr(io.Discard).
		WithSysWalltime().WithSysNanotime().WithStartFunctions()
	inst, err := l.rt.InstantiateModule(ctx, l.mod, cfg)
	if err != nil {
		for _, f := range []*os.File{inR, inW, outR, outW} {
			f.Close()
		}
		return nil, fmt.Errorf("office engine: a %s worker could not start: %w", kind, err)
	}
	start := inst.ExportedFunction("_start")
	if start == nil {
		_ = inst.Close(ctx)
		for _, f := range []*os.File{inR, inW, outR, outW} {
			f.Close()
		}
		return nil, fmt.Errorf("office engine: layout-host.wasm has no _start (not a WASI command)")
	}
	w := &worker{kind: kind, inst: inst, in: inW, out: bufio.NewReaderSize(outR, 1<<16), outF: outR, exited: make(chan struct{})}
	go func() {
		_, err := start.Call(ctx)
		w.exitErr = err
		outW.Close()
		inR.Close()
		close(w.exited)
	}()
	var hello struct {
		Ready bool   `json:"ready"`
		Error string `json:"error"`
	}
	if err := w.read(&hello); err != nil {
		w.close(ctx)
		return nil, fmt.Errorf("office engine: the %s worker ended before it was ready: %w", kind, err)
	}
	if !hello.Ready {
		w.close(ctx)
		return nil, fmt.Errorf("office engine: the %s scripts did not load: %s", kind, hello.Error)
	}
	return w, nil
}

func (w *worker) read(v any) error {
	line, err := w.out.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

// close ends the worker: its stdin closes, so its loop ends and _start
// returns; an instance that does not end in a moment is closed under it.
func (w *worker) close(ctx context.Context) {
	_ = w.in.Close()
	select {
	case <-w.exited:
	case <-time.After(2 * time.Second):
	}
	_ = w.inst.Close(ctx)
	_ = w.outF.Close()
}

// layoutJob is one document to lay out: in is its Editor.bin and out where
// the drawing (or the HTML) goes, both under /j; fonts is where the fonts
// the pages were drawn with are written.
type layoutJob struct {
	In     string          `json:"in"`
	Out    string          `json:"out"`
	Fonts  string          `json:"fonts,omitempty"`
	Mode   string          `json:"mode"`
	Params json.RawMessage `json:"json,omitempty"`
}

// layoutAnswer is the worker's answer.
type layoutAnswer struct {
	OK    bool   `json:"ok"`
	Stage string `json:"stage"`
	Error string `json:"error"`
	Pages int    `json:"pages"`
}

// do runs one job on the worker. A failure is an *Error; broken says the
// worker itself is gone and must not be asked again.
func (w *worker) do(job layoutJob) (*layoutAnswer, bool, error) {
	if len(job.Params) == 0 {
		job.Params = json.RawMessage("null")
	}
	b, err := json.Marshal(job)
	if err != nil {
		return nil, false, &Error{Code: CodeUnknown, What: "layout-request", Err: err}
	}
	w.jobs++
	if _, err := w.in.Write(append(b, '\n')); err != nil {
		return nil, true, &Error{Code: CodeCorrupt, What: "layout-crash", Err: err}
	}
	var ans layoutAnswer
	if err := w.read(&ans); err != nil {
		// The worker ended mid-job: a trap, or out of memory.
		var why error = err
		select {
		case <-w.exited:
			if w.exitErr != nil {
				why = w.exitErr
			}
		case <-time.After(time.Second):
		}
		return nil, true, &Error{Code: CodeCorrupt, What: "layout-crash", Err: why}
	}
	if !ans.OK {
		if strings.Contains(strings.ToLower(ans.Error), "out of memory") {
			return nil, true, &Error{Code: CodeTooLarge, What: "memory", Err: errors.New(ans.Error)}
		}
		stage := ans.Stage
		if stage == "" {
			stage = "failed"
		}
		return nil, false, &Error{Code: CodeCorrupt, What: "layout-" + cleanName(stage), Err: errors.New(ans.Error)}
	}
	return &ans, false, nil
}

// cleanName keeps a short name to what a record may carry.
func cleanName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 16 {
			break
		}
	}
	if b.Len() == 0 {
		return "failed"
	}
	return b.String()
}

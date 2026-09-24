// Package wasmplugin runs filex "app plugins": WebAssembly modules executed
// inside the filex process by wazero (through the Extism host SDK), with no
// access to the host's filesystem, network, environment or database — only
// to the host functions filex exposes, each gated by a permission the
// administrator granted at install.
//
// # Why in-process wasm and not a subprocess or a sidecar
//
// The storage-plugin subsystem (internal/plugin) starts a plugin BINARY in
// filex's own container: same filesystem, same environment, FILEX_SECRET_KEY
// in reach. That is fine for a driver the operator built, and unacceptable for
// third-party code a user installs from a URL. A container per plugin needs a
// Docker socket somewhere; an OS sandbox is strong on Linux and absent on the
// other two platforms filex ships for. A wasm sandbox is the same on all three,
// costs no extra process, and cannot reach anything it is not handed.
//
// # What the runtime guarantees
//
//   - wazero's compiler backend only (amd64/arm64). The interpreter is refused,
//     not tolerated: measured elsewhere, it takes every core for minutes.
//   - A fresh instance per call: no state survives between users or jobs,
//     memory is returned when the call ends.
//   - A memory ceiling (Manifest.MemoryPages) and a wall-clock budget per
//     call; the instance is torn down when either is hit.
//   - No WASI filesystem preopens, no environment, no arguments.
//   - Compiled modules are cached on disk (WithCompilationCache), so a
//     20–50 MB Go module is compiled once per host, not once per boot.
package wasmplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	extism "github.com/extism/go-sdk"
	"github.com/tetratelabs/wazero"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// HostVersion is what `describe` is told; filled by the server from its build.
var HostVersion = "dev"

// Runtime owns the compilation cache and the per-host settings.
type Runtime struct {
	cacheDir string
	cache    wazero.CompilationCache
	mu       sync.Mutex
	closed   bool
}

// New prepares a runtime. cacheDir holds compiled modules; "" disables the
// on-disk cache (tests).
func NewRuntime(cacheDir string) (*Runtime, error) {
	// Guest log lines reach the per-plugin ring through inst.SetLogger; the
	// SDK still drops them below its process-wide level, which starts at
	// Off. Debug here, filtering is the ring reader's job.
	extism.SetLogLevel(extism.LogLevelDebug)
	if !ArchSupported() {
		return nil, ErrUnsupportedArch
	}
	r := &Runtime{cacheDir: cacheDir}
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("wasmplugin: cache dir: %w", err)
		}
		c, err := wazero.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("wasmplugin: compilation cache: %w", err)
		}
		r.cache = c
	} else {
		r.cache = wazero.NewCompilationCache()
	}
	return r, nil
}

// ArchSupported reports whether wazero has a compiler for this CPU.
func ArchSupported() bool {
	return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
}

// Close releases the compilation cache.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.cache.Close(ctx)
}

// HostFunc is a host function the runtime links into every instance. The
// registry builds them (hostfn.go); tests pass a few of their own.
type HostFunc = extism.HostFunction

// Compiled is one installed plugin's module, compiled once and instantiated
// per call.
type Compiled struct {
	Name     string
	Manifest *Manifest
	SHA256   string
	rt       *Runtime
	cp       *extism.CompiledPlugin
	logger   func(level, msg string)
}

// Compile validates the module against sha256 (lower-hex; "" skips the check)
// and compiles it with the given host functions.
func (r *Runtime) Compile(ctx context.Context, wasmPath, sha256Hex string, m *Manifest, fns []HostFunc, logger func(level, msg string)) (*Compiled, error) {
	sum, err := fileSHA256(wasmPath)
	if err != nil {
		return nil, err
	}
	if sha256Hex != "" && !strings.EqualFold(sum, sha256Hex) {
		return nil, fmt.Errorf("wasmplugin: %s: sha256 mismatch (file %s, expected %s)", wasmPath, sum[:12], strings.ToLower(sha256Hex)[:12])
	}
	pages := uint32(m.MemoryPages())
	manifest := extism.Manifest{
		Wasm:   []extism.Wasm{extism.WasmFile{Path: wasmPath, Hash: sum, Name: "main"}},
		Memory: &extism.ManifestMemory{MaxPages: pages, MaxHttpResponseBytes: 8 << 20, MaxVarBytes: 1 << 20},
		// No AllowedHosts: Extism's own http_request import stays closed, so
		// the guarded filex http_request host function (outbound.go — host
		// allow-list, private-address refusal, size and time caps) is the
		// only way onto the network. No AllowedPaths: no filesystem at all.
		AllowedHosts: nil,
		Config:       map[string]string{},
	}
	cfg := extism.PluginConfig{
		EnableWasi: true,
		RuntimeConfig: wazero.NewRuntimeConfigCompiler().
			WithCompilationCache(r.cache).
			WithCloseOnContextDone(true),
	}
	// The host function table is part of the runtime contract: a module
	// that imports file_open must find it whether or not the caller cares,
	// and a call without a scope on its context is refused inside the
	// function, not at instantiation.
	fns = append(hostFunctions(), fns...)
	cp, err := extism.NewCompiledPlugin(ctx, manifest, cfg, fns)
	if err != nil {
		return nil, fmt.Errorf("wasmplugin: compile %s: %w", m.Name, err)
	}
	if logger == nil {
		logger = func(string, string) {}
	}
	return &Compiled{Name: m.Name, Manifest: m, SHA256: sum, rt: r, cp: cp, logger: logger}, nil
}

// Close frees the compiled module.
func (c *Compiled) Close(ctx context.Context) error {
	if c == nil || c.cp == nil {
		return nil
	}
	return c.cp.Close(ctx)
}

// Call runs one export on a fresh instance with a wall-clock budget. The
// returned bytes are the guest's output. Errors are *CallError.
//
// ctx may carry a call scope (WithScope) that host functions read to decide
// what this particular call is allowed to touch; the guest never sees it.
func (c *Compiled) Call(ctx context.Context, export string, input []byte, budget time.Duration) ([]byte, error) {
	if budget <= 0 {
		budget = time.Duration(c.Manifest.CallTimeout()) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	inst, err := c.cp.Instance(ctx, extism.PluginInstanceConfig{
		ModuleConfig: wazero.NewModuleConfig().WithSysWalltime().WithSysNanotime().WithRandSource(randReader{}),
	})
	if err != nil {
		return nil, &CallError{Code: CodePluginTrap, Export: export, Message: "the plugin could not be started", Cause: err}
	}
	defer inst.Close(context.WithoutCancel(ctx))
	inst.SetLogger(func(level extism.LogLevel, msg string) {
		// The SDK narrates its own steps at debug ("Initializing runtime",
		// "Calling function : describe"); that is the host's business, not
		// the plugin's, and it would bury the 500-line ring under three lines
		// per call. The guest's own debug lines still pass.
		if level <= extism.LogLevelDebug && sdkChatter(msg) {
			return
		}
		c.logger(levelName(level), msg)
	})

	if !inst.FunctionExists(export) {
		return nil, &CallError{Code: CodePluginError, Export: export, Message: "the plugin has no " + export + " export"}
	}
	started := time.Now()
	exit, out, err := inst.CallWithContext(ctx, export, input)
	if err != nil {
		return nil, classify(ctx, export, err, exit, inst.GetErrorWithContext(context.WithoutCancel(ctx)), time.Since(started), budget)
	}
	if exit != 0 {
		msg := strings.TrimSpace(inst.GetErrorWithContext(context.WithoutCancel(ctx)))
		if msg == "" {
			msg = fmt.Sprintf("exit code %d", exit)
		}
		return nil, &CallError{Code: CodePluginError, Export: export, Message: clip(msg, 1000)}
	}
	return out, nil
}

// HasExport reports whether the module exports a function by that name.
//
// It costs one instantiation, which is why it is asked at load rather than
// per call: the alternative is discovering an hour later, in a log, that an
// app which asked to be woken has nothing to wake.
func (c *Compiled) HasExport(ctx context.Context, export string) (bool, error) {
	inst, err := c.cp.Instance(ctx, extism.PluginInstanceConfig{ModuleConfig: wazero.NewModuleConfig()})
	if err != nil {
		return false, &CallError{Code: CodePluginTrap, Export: export, Message: "the plugin could not be started", Cause: err}
	}
	defer inst.Close(context.WithoutCancel(ctx))
	return inst.FunctionExists(export), nil
}

// Describe calls the guest's describe export and checks that it agrees with
// the installed manifest on the things a grant depends on. The file on disk
// is the operator's intent; the guest's answer is the proof it is the same
// program.
func (c *Compiled) Describe(ctx context.Context, locale string) (*wire.Manifest, error) {
	in, _ := jsonMarshal(wire.DescribeInput{HostVersion: HostVersion, Locale: locale})
	out, err := c.Call(ctx, "describe", in, 0)
	if err != nil {
		return nil, err
	}
	var got wire.Manifest
	if err := jsonUnmarshal(out, &got); err != nil {
		return nil, &CallError{Code: CodePluginError, Export: "describe", Message: "describe did not answer with a manifest", Cause: err}
	}
	if got.Name != c.Manifest.Name || got.Version != c.Manifest.Version || got.ManifestVersion != c.Manifest.ManifestVersion {
		return nil, &CallError{Code: CodeRefused, Export: "describe",
			Message: fmt.Sprintf("describe says %s %s (v%d), the installed manifest says %s %s (v%d)", got.Name, got.Version, got.ManifestVersion, c.Manifest.Name, c.Manifest.Version, c.Manifest.ManifestVersion)}
	}
	want := map[string]bool{}
	for _, p := range c.Manifest.Perms {
		want[string(p)] = true
	}
	for _, raw := range got.Permissions {
		p, err := ParsePermission(raw)
		if err != nil {
			return nil, &CallError{Code: CodeRefused, Export: "describe", Message: "describe names an unknown permission: " + raw}
		}
		if !want[string(p)] {
			return nil, &CallError{Code: CodeRefused, Export: "describe", Message: "describe asks for " + string(p) + ", which the installed manifest does not declare"}
		}
	}
	return &got, nil
}

func classify(ctx context.Context, export string, err error, exit uint32, guestMsg string, took, budget time.Duration) *CallError {
	msg := strings.TrimSpace(guestMsg)
	s := err.Error()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || strings.Contains(s, "context deadline exceeded") || strings.Contains(s, "timeout"):
		return &CallError{Code: CodeTimeout, Export: export,
			Message: fmt.Sprintf("the plugin did not finish within %s", budget.Round(time.Second)), Cause: err}
	case strings.Contains(s, "out of memory") || strings.Contains(s, "memory.grow") || strings.Contains(s, "out of bounds memory"):
		return &CallError{Code: CodePluginOOM, Export: export, Message: "the plugin ran out of its memory allowance", Cause: err}
	case msg != "":
		return &CallError{Code: CodePluginError, Export: export, Message: clip(msg, 1000), Cause: err}
	default:
		return &CallError{Code: CodePluginTrap, Export: export, Message: "the plugin crashed", Cause: err}
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func levelName(l extism.LogLevel) string {
	switch l {
	case extism.LogLevelTrace:
		return "trace"
	case extism.LogLevelDebug:
		return "debug"
	case extism.LogLevelInfo:
		return "info"
	case extism.LogLevelWarn:
		return "warn"
	case extism.LogLevelError:
		return "error"
	}
	return "info"
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("wasmplugin: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("wasmplugin: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sdkChatter recognises the Extism SDK's own debug narration.
func sdkChatter(msg string) bool {
	return strings.HasPrefix(msg, "Initializing runtime") || strings.HasPrefix(msg, "Calling ")
}

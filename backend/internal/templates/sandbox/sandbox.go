// Package sandbox renders a template somebody EDITED: the Liquid dialect of
// internal/templates/engine compiled to WebAssembly (guest/, built by
// build.sh into engine.wasm.gz) and run in the pure-Go wazero runtime, the way
// thumb/svgwasm draws an SVG.
//
// ⚠⚠ Why a sandbox and not the engine in-process (measured 2026-11-11, docs/
// TEMPLATES.md "Security"): Liquid has loops, ranges and string building, and
// neither osteele/liquid nor any other Go Liquid library bounds them. In the
// server, `{% for i in (1..100000000) %}{% endfor %}` ran past every context
// deadline and a string doubled thirty times asked for ten gigabytes - one
// administrator's template (or typo) could take the whole server, every
// tenant's, down. Here each of those ends at a ceiling:
//
//   - a fresh instance per render (a template that traps the engine or fills
//     its memory leaves nothing for the next one, and no tenant's render
//     shares a heap with another's);
//   - a memory ceiling (memoryPages), a time limit per render (the caller's
//     context closes the instance: Page 250 ms, Mail 1 s), an output ceiling
//     (MaxOutput) and a ceiling on how many render at once (parallel);
//   - the module imports WASI and one host function (filex.host_call: a
//     catalogue sentence, a size) and is given no file and no socket.
//
// A render that ends at a ceiling, or fails at all, is an error: the caller
// (internal/templates) then renders the template filex ships instead, so a
// broken template never costs a visitor their page or a person their mail.
package sandbox

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/brf-tech/filex/backend/internal/templates/engine"
	"github.com/brf-tech/filex/backend/internal/wasmguest"
)

//go:embed engine.wasm.gz
var engineGz []byte

const (
	// memoryPages caps one instance's linear memory: 1024 × 64 KiB = 64 MiB.
	memoryPages = 1024
	// parallel is how many templates are rendered in the sandbox at once,
	// process-wide.
	parallel = 2
	// MaxOutput is the most a template may write (and the most a request may
	// carry in).
	MaxOutput = 1 << 20
	// MaxSource is the longest template the sandbox accepts.
	MaxSource = 128 << 10
	// DefaultTimeout bounds a render whose context has no deadline.
	DefaultTimeout = time.Second
	// hostReplyMax caps one host call's answer.
	hostReplyMax = 64 << 10
)

// Request is one render: a template, optionally the layout it is put in
// (rendered with `content` set to the template's output), the data both see,
// which data paths are filex's own markup (Safe), and the shared pieces
// include may read.
type Request struct {
	Text     bool
	Strict   bool
	Name     string
	Source   string
	Layout   string
	LayoutAs string
	Data     map[string]any
	Safe     []string
	Partials map[string]string
	Host     engine.Host
}

// ErrTooLarge: a template or its data is over the ceilings.
var ErrTooLarge = errors.New("the template is larger than the sandbox accepts")

// ErrTooSlow: the render reached its time limit.
var ErrTooSlow = errors.New("the template took longer than a render may")

// ErrStopped: the engine stopped inside the render - it reached the memory
// ceiling, or failed on the template.
var ErrStopped = errors.New("the template engine stopped: the template used more memory than a render may, or the engine failed on it")

// TemplateError is the template's own failure (a syntax error, an undefined
// variable in a strict render, a filter's error), as the engine said it.
type TemplateError struct{ Msg string }

func (e *TemplateError) Error() string { return e.Msg }

var (
	compileOnce sync.Once
	wrt         wazero.Runtime
	compiled    wazero.CompiledModule
	compileErr  error
	slots       = make(chan struct{}, parallel)
)

type hostKey struct{}

func compile() {
	ctx := context.Background()
	zr, err := gzip.NewReader(bytes.NewReader(engineGz))
	if err != nil {
		compileErr = fmt.Errorf("template sandbox: %w", err)
		return
	}
	wasm, err := io.ReadAll(zr)
	if err != nil {
		compileErr = fmt.Errorf("template sandbox: %w", err)
		return
	}
	wrt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(memoryPages).
		WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wrt); err != nil {
		compileErr = fmt.Errorf("template sandbox: wasi: %w", err)
		return
	}
	if _, err := wrt.NewHostModuleBuilder("filex").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostCall),
		[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
		[]api.ValueType{api.ValueTypeI32}).Export("host_call").
		Instantiate(ctx); err != nil {
		compileErr = fmt.Errorf("template sandbox: host module: %w", err)
		return
	}
	compiled, compileErr = wrt.CompileModule(ctx, wasm)
	if compileErr != nil {
		compileErr = fmt.Errorf("template sandbox: compile: %w", compileErr)
	}
}

// hostCall answers filex.host_call(req, reqLen, buf, bufCap): the request is
// {"fn", "args"} (engine.Host), the answer {"ok"} or {"err"}, written into buf
// when it fits. It returns the answer's length either way, so a guest whose
// buffer was too small asks again with a larger one.
func hostCall(ctx context.Context, m api.Module, stack []uint64) {
	reqPtr, reqLen := uint32(stack[0]), uint32(stack[1])
	bufPtr, bufCap := uint32(stack[2]), uint32(stack[3])
	answer := func(r map[string]string) {
		b, _ := json.Marshal(r)
		if len(b) > hostReplyMax {
			b, _ = json.Marshal(map[string]string{"err": "the answer is too long"})
		}
		if uint32(len(b)) <= bufCap {
			m.Memory().Write(bufPtr, b)
		}
		stack[0] = uint64(len(b))
	}
	if reqLen > hostReplyMax {
		answer(map[string]string{"err": "the request is too long"})
		return
	}
	raw, ok := m.Memory().Read(reqPtr, reqLen)
	if !ok {
		answer(map[string]string{"err": "the request is outside the guest's memory"})
		return
	}
	var req struct {
		Fn   string `json:"fn"`
		Args []any  `json:"args"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		answer(map[string]string{"err": err.Error()})
		return
	}
	h, _ := ctx.Value(hostKey{}).(engine.Host)
	if h == nil {
		answer(map[string]string{"err": engine.ErrNoHost.Error()})
		return
	}
	// JSON gave the vars back as map[string]any; the Host contract is
	// map[string]string.
	if len(req.Args) > 1 {
		if m, ok := req.Args[1].(map[string]any); ok {
			vars := make(map[string]string, len(m))
			for k, v := range m {
				vars[k] = fmt.Sprint(v)
			}
			req.Args[1] = vars
		}
	}
	if req.Fn == "bytes" && len(req.Args) > 0 {
		if f, ok := req.Args[0].(float64); ok {
			req.Args[0] = int64(f)
		}
	}
	s, err := h.Call(req.Fn, req.Args)
	if err != nil {
		answer(map[string]string{"err": err.Error()})
		return
	}
	answer(map[string]string{"ok": s})
}

// Prepare compiles the engine if it has not been yet (a few seconds, once per
// process). A caller that puts a time limit on Render calls it first, so the
// compile is not counted against the first render's limit.
func Prepare() error {
	compileOnce.Do(compile)
	return compileErr
}

// Slot waits for one of the `parallel` places to render in and returns its
// release. Taken BEFORE a render's time limit starts: waiting for a place is
// not the template's fault.
func Slot(ctx context.Context) (release func(), err error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// wireRequest is the request as the guest reads it (guest/main.go request).
type wireRequest struct {
	Mode     string            `json:"mode"`
	Strict   bool              `json:"strict"`
	Name     string            `json:"name"`
	Src      string            `json:"src"`
	Layout   string            `json:"layout,omitempty"`
	LayoutAs string            `json:"layout_name,omitempty"`
	Data     map[string]any    `json:"data"`
	Safe     []string          `json:"safe,omitempty"`
	Partials map[string]string `json:"partials,omitempty"`
	MaxOut   int               `json:"max_out"`
}

// Render renders req in a fresh instance. The caller holds a Slot and has put
// its time limit on ctx; a ctx without a deadline gets DefaultTimeout.
func Render(ctx context.Context, req Request) (string, error) {
	if len(req.Source) > MaxSource || len(req.Layout) > MaxSource {
		return "", ErrTooLarge
	}
	for _, p := range req.Partials {
		if len(p) > MaxSource {
			return "", ErrTooLarge
		}
	}
	compileOnce.Do(compile)
	if compileErr != nil {
		return "", compileErr
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	w := wireRequest{
		Mode: "html", Strict: req.Strict, Name: req.Name, Src: req.Source,
		Layout: req.Layout, LayoutAs: req.LayoutAs, Data: req.Data, Safe: req.Safe,
		Partials: req.Partials, MaxOut: MaxOutput,
	}
	if req.Text {
		w.Mode = "text"
	}
	in, err := json.Marshal(w)
	if err != nil {
		return "", fmt.Errorf("template sandbox: %w", err)
	}
	if len(in) > 4*MaxOutput {
		return "", ErrTooLarge
	}
	ctx = context.WithValue(ctx, hostKey{}, req.Host)
	// Anonymous (""), so any number of instances can exist side by side.
	// _initialize is the reactor's start: the Go runtime and package inits.
	mod, err := wrt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().
		WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return "", deadlineOr(ctx, err)
	}
	defer mod.Close(context.WithoutCancel(ctx))
	g := wasmguest.Guest{Ctx: ctx, Mod: mod}
	g.Put("fx_in", in)
	rc := g.Call("fx_render", uint64(len(in)))
	n := g.Call("fx_out_len")
	var outp uint64
	if n > 0 {
		outp = g.Call("fx_out_ptr")
	}
	if g.Err != nil {
		return "", deadlineOr(ctx, g.Err)
	}
	if n > MaxOutput+4096 {
		return "", ErrTooLarge
	}
	var res []byte
	if n > 0 {
		b, ok := mod.Memory().Read(uint32(outp), uint32(n))
		if !ok {
			return "", errors.New("template sandbox: the engine answered outside its memory")
		}
		res = append([]byte(nil), b...)
	}
	if rc != 0 {
		return "", &TemplateError{Msg: string(res)}
	}
	return string(res), nil
}

// deadlineOr names a closed instance's reason: the time limit, when that is
// what ended it.
func deadlineOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w (%v)", ErrTooSlow, ctx.Err())
	}
	return fmt.Errorf("%w (%v)", ErrStopped, errLine(err.Error()))
}

func errLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

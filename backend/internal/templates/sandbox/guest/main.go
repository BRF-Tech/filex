//go:build wasip1

// Command guest is the template engine run inside the sandbox
// (internal/templates/sandbox): internal/templates/engine compiled for
// GOOS=wasip1 as a reactor. The host hands it one request, it renders, the
// host reads the answer and throws the instance away.
//
// It imports nothing from the host but WASI (stdout, the clock) and one
// function, filex.host_call, through which the template filters that need the
// server (a catalogue sentence, a size) ask it. No file is preopened and no
// socket exists: there is nothing for a template to reach.
//
// Built by ../build.sh into ../engine.wasm.gz.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"unsafe"

	"github.com/brf-tech/filex/backend/internal/templates/engine"
)

// request is what the host sends (sandbox.wireRequest has the same shape).
type request struct {
	Mode     string            `json:"mode"`
	Strict   bool              `json:"strict"`
	Name     string            `json:"name"`
	Src      string            `json:"src"`
	Layout   string            `json:"layout"`
	LayoutAs string            `json:"layout_name"`
	Data     map[string]any    `json:"data"`
	Safe     []string          `json:"safe"`
	Partials map[string]string `json:"partials"`
	MaxOut   int               `json:"max_out"`
}

var (
	in      []byte
	out     []byte
	failure string
)

//go:wasmimport filex host_call
func hostCall(req unsafe.Pointer, reqLen uint32, buf unsafe.Pointer, bufCap uint32) uint32

type host struct{}

// reply is the host's answer to one call.
type reply struct {
	OK  string `json:"ok"`
	Err string `json:"err"`
}

func (host) Call(fn string, args []any) (string, error) {
	req, err := json.Marshal(map[string]any{"fn": fn, "args": args})
	if err != nil {
		return "", err
	}
	buf := make([]byte, 4096)
	for i := 0; i < 2; i++ {
		n := hostCall(unsafe.Pointer(&req[0]), uint32(len(req)), unsafe.Pointer(&buf[0]), uint32(len(buf)))
		if int(n) <= len(buf) {
			var r reply
			if err := json.Unmarshal(buf[:n], &r); err != nil {
				return "", err
			}
			if r.Err != "" {
				return "", errors.New(r.Err)
			}
			return r.OK, nil
		}
		buf = make([]byte, n)
	}
	return "", errors.New("the host's answer did not fit")
}

// fx_in gives the host a buffer of n bytes to write the request into.
//
//go:wasmexport fx_in
func fxIn(n uint32) uint32 {
	in = make([]byte, n+1)
	return uint32(uintptr(unsafe.Pointer(&in[0])))
}

// fx_render renders the request; 0 = rendered (fx_out), 1 = the template
// failed (fx_out is the message).
//
//go:wasmexport fx_render
func fxRender(n uint32) uint32 {
	res, err := render(in[:n])
	if err != nil {
		out = []byte(err.Error())
		return 1
	}
	out = []byte(res)
	return 0
}

//go:wasmexport fx_out_ptr
func fxOutPtr() uint32 {
	if len(out) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&out[0])))
}

//go:wasmexport fx_out_len
func fxOutLen() uint32 { return uint32(len(out)) }

// capped is the output writer: a template that writes past max is stopped.
type capped struct {
	b   []byte
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if len(c.b)+len(p) > c.max {
		return 0, fmt.Errorf("the template wrote more than %d bytes", c.max)
	}
	c.b = append(c.b, p...)
	return len(p), nil
}

func render(raw []byte) (string, error) {
	var r request
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	if r.MaxOut <= 0 {
		r.MaxOut = 1 << 20
	}
	o := engine.Options{Host: host{}, Partials: r.Partials, Strict: r.Strict}
	if r.Mode == "text" {
		o.Mode = engine.Text
	}
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	engine.MarkSafe(r.Data, r.Safe)
	w := &capped{max: r.MaxOut}
	if err := engine.RenderTo(w, o, r.Name, r.Src, r.Data); err != nil {
		return "", err
	}
	if r.Layout == "" {
		return string(w.b), nil
	}
	r.Data["content"] = engine.Safe(string(w.b))
	lw := &capped{max: r.MaxOut}
	if err := engine.RenderTo(lw, o, r.LayoutAs, r.Layout, r.Data); err != nil {
		return "", err
	}
	return string(lw.b), nil
}

func main() {}

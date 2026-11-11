// Package wasmguest calls into one instance of a single-purpose WebAssembly
// engine the way filex's engines are driven (internal/thumb/svgwasm, the SVG
// rasteriser; internal/templates/sandbox, the template engine): put the
// input into the guest's memory, call its exports, read the answer back.
//
// The first error sticks and every call after it is a no-op, so a render
// reads as a straight line and is checked once, at the end.
package wasmguest

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Guest is one instance being called. Err is the first thing that failed.
type Guest struct {
	Ctx context.Context
	Mod api.Module
	Err error
}

// Call calls the export name with args and returns its first result (0 when
// it has none, or after an error).
func (g *Guest) Call(name string, args ...uint64) uint64 {
	if g.Err != nil {
		return 0
	}
	f := g.Mod.ExportedFunction(name)
	if f == nil {
		g.Err = fmt.Errorf("the engine has no %s", name)
		return 0
	}
	res, err := f.Call(g.Ctx, args...)
	if err != nil {
		g.Err = err
		return 0
	}
	if len(res) == 0 {
		return 0
	}
	return res[0]
}

// Put asks the guest for len(b) bytes through its export alloc (one argument,
// the length; it answers the address) and writes b there.
func (g *Guest) Put(alloc string, b []byte) uint64 {
	p := g.Call(alloc, uint64(len(b)))
	if g.Err == nil && !g.Mod.Memory().Write(uint32(p), b) {
		g.Err = errors.New("the engine's memory is too small")
	}
	return p
}

// Read copies n bytes at p out of the guest's memory.
func (g *Guest) Read(p, n uint64) []byte {
	if g.Err != nil {
		return nil
	}
	b, ok := g.Mod.Memory().Read(uint32(p), uint32(n))
	if !ok {
		g.Err = errors.New("the engine answered outside its memory")
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

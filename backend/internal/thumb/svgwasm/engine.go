// Package svgwasm draws SVG files with resvg compiled to WebAssembly, run in
// the pure-Go wazero runtime: the SVG engine filex carries itself, so an SVG
// gets a thumbnail on every install (the full image, :slim, the bare binary)
// without rsvg-convert and without cgo.
//
// ⚠⚠ What contains it, and why each piece is there (docs/thumbnails.md,
// Design notes: SVG):
//
//   - The module imports NOTHING (rust/src/lib.rs, built for
//     wasm32-unknown-unknown). It has no file system, no socket and no clock
//     to reach: an SVG that names /etc/passwd or a URL in an <image> draws
//     nothing there, whatever the parser thinks of it. Embedded data: images
//     still draw.
//   - A fresh module instance per file. A file that traps the engine (a
//     stack overflow on deep nesting) or fills its memory cannot leave
//     anything behind for the next file.
//   - A memory ceiling per instance (memoryPages), a time limit per file (the
//     context closes the instance), a ceiling on the input (MaxInput; the
//     administrator's lower limits are applied by thumb/svg.go) and on how
//     many files are drawn at once (parallel): four renders of a hostile
//     file must not be able to take the server's memory.
//
// The module is compiled once per process, lazily, on the first SVG (about
// two seconds). engine.wasm.gz is built by build.sh; see NOTICE for licenses.
package svgwasm

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/brf-tech/filex/backend/internal/wasmguest"
)

//go:embed engine.wasm.gz
var engineGz []byte

const (
	// MaxInput is the largest SVG the engine accepts at all, whatever the
	// administrator's limit says: the ceiling of that setting
	// (thumb.SVGMaxMBSetting, 64 MB). The limit in force is lower (5 MB by
	// default) and is applied before a byte is read (thumb/svg.go).
	MaxInput = 64 << 20
	// memoryPages caps one instance's linear memory: 4096 × 64 KiB = 256 MiB.
	memoryPages = 4096
	// parallel is how many files are drawn at the same time, process-wide.
	parallel = 2
	// DefaultTimeout is the time one file may take when the caller's context
	// has no earlier deadline.
	DefaultTimeout = 30 * time.Second
)

// ErrTooLarge: the file is over MaxInput.
var ErrTooLarge = errors.New("svg: the file is larger than the built-in engine accepts")

var (
	compileOnce sync.Once
	wrt         wazero.Runtime
	compiled    wazero.CompiledModule
	compileErr  error
	slots       = make(chan struct{}, parallel)
)

func compile() {
	ctx := context.Background()
	zr, err := gzip.NewReader(bytes.NewReader(engineGz))
	if err != nil {
		compileErr = fmt.Errorf("svg engine: %w", err)
		return
	}
	wasm, err := io.ReadAll(zr)
	if err != nil {
		compileErr = fmt.Errorf("svg engine: %w", err)
		return
	}
	wrt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(memoryPages).
		WithCloseOnContextDone(true))
	compiled, compileErr = wrt.CompileModule(ctx, wasm)
	if compileErr != nil {
		compileErr = fmt.Errorf("svg engine: compile: %w", compileErr)
	}
}

// fonts are the faces text is drawn with: the Go fonts, embedded. An SVG
// seldom depends on a typeface at thumbnail size, and a font the file names
// that is not here falls back to these.
var fonts = [][]byte{goregular.TTF, gobold.TTF, goitalic.TTF, gobolditalic.TTF, gomono.TTF}

// generic maps the CSS generic families (sans-serif, serif, monospace) to
// the embedded faces, one per line.
var generic = strings.Join([]string{"Go", "Go", "Go Mono"}, string(rune(10)))

// Slot waits for one of the `parallel` places to draw in and returns its
// release. Callers that draw (thumb/svg.go) take a slot BEFORE they start a
// file's time limit: the wait for a slot is not the file's fault, and counted
// against its limit it would skip files for being slow that never ran.
func Slot(ctx context.Context) (release func(), err error) {
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Prepare compiles the engine if it has not been yet (about two seconds, once
// per process). A caller that puts a time limit on Render calls it first, so
// the compile is not counted against the first file's limit.
func Prepare() error {
	compileOnce.Do(compile)
	return compileErr
}

// Render draws svg to fit inside maxW × maxH, aspect kept. The image is
// premultiplied RGBA with the file's own transparency; the caller flattens it.
func Render(ctx context.Context, svg []byte, maxW, maxH int) (image.Image, error) {
	if len(svg) > MaxInput {
		return nil, ErrTooLarge
	}
	if maxW <= 0 || maxH <= 0 {
		return nil, errors.New("svg: no size to draw at")
	}
	compileOnce.Do(compile)
	if compileErr != nil {
		return nil, compileErr
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	// Anonymous (""), so any number of instances can exist side by side.
	mod, err := wrt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("svg engine: %w", err)
	}
	defer mod.Close(context.WithoutCancel(ctx))
	g := wasmguest.Guest{Ctx: ctx, Mod: mod}
	for _, f := range fonts {
		p := g.Put("fx_alloc", f)
		g.Call("fx_add_font", p, uint64(len(f)))
	}
	fam := []byte(generic)
	g.Call("fx_set_families", g.Put("fx_alloc", fam), uint64(len(fam)))
	src := g.Put("fx_alloc", svg)
	rc := g.Call("fx_render", src, uint64(len(svg)), uint64(maxW), uint64(maxH))
	if g.Err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("svg: %w", ctx.Err())
		}
		return nil, fmt.Errorf("svg engine: %w", g.Err)
	}
	if rc != 0 {
		msg := g.Read(g.Call("fx_err_ptr"), g.Call("fx_err_len"))
		return nil, fmt.Errorf("svg: %s", string(msg))
	}
	w, h := int(g.Call("fx_out_w")), int(g.Call("fx_out_h"))
	pix := g.Read(g.Call("fx_out_ptr"), g.Call("fx_out_len"))
	if g.Err != nil {
		return nil, fmt.Errorf("svg engine: %w", g.Err)
	}
	if w <= 0 || h <= 0 || w > maxW || h > maxH || len(pix) != w*h*4 {
		return nil, fmt.Errorf("svg engine: unexpected output %dx%d (%d bytes)", w, h, len(pix))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, pix)
	return img, nil
}

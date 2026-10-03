package thumb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png" // rsvg-convert's output
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb/svgwasm"
)

// svgOverLimit: the file is over one of the SVG limits (svglimits.go).
// GenerateThumb records it as `skipped` with reason, not `failed`: nothing is
// wrong with the file, and raising the limit draws it.
type svgOverLimit struct{ reason string }

func (e *svgOverLimit) Error() string { return "thumb: svg over a limit: " + e.reason }

// generateSVG draws an SVG with the built-in engine (svgwasm: resvg compiled
// to WebAssembly, run in-process with no file system, no network and a
// memory and time ceiling), and falls back to rsvg-convert only when the
// built-in engine could not draw the file and the binary is on PATH.
//
// ⚠ Built-in FIRST, on every install. 0.49 had only rsvg-convert, so the
// :slim image and the bare binary skipped every SVG, and the two paths drew
// the same file differently where both existed. The measurement that chose
// resvg (54 real-world files; oksvg and tdewolff/canvas drew many of them
// wrong or panicked) is in docs/thumbnails.md, Design notes: SVG.
//
// ⚠ The limits hold for both engines: a file over the size limit is never
// read past it, and one that runs out of time is not handed to the fallback
// to run out of time again.
func (p *Pipeline) generateSVG(ctx context.Context, node *model.Node, drv storage.Driver, lim SVGLimits) error {
	rc, err := p.openSource(ctx, drv, node)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(rc, lim.MaxBytes+1))
	rc.Close()
	if err != nil {
		return err
	}
	if int64(len(data)) > lim.MaxBytes {
		// The catalogue's size was behind the file: caught on the bytes.
		return &svgOverLimit{reason: skipReason(SkipSVGTooLarge, lim.MaxBytes)}
	}
	// The engine is compiled once per process (about two seconds): not on the
	// first file's clock, or a short limit would skip the first SVG drawn
	// after every restart.
	if err := svgwasm.Prepare(); err != nil {
		return fmt.Errorf("thumb: %w", err)
	}
	// Nor is the wait for a free place to draw in (svgwasm.Slot).
	release, err := svgwasm.Slot(ctx)
	if err != nil {
		return err
	}
	defer release()
	rctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()
	img, err := p.svgRender(rctx, data, thumbMaxWidth, thumbMaxHeight)
	if err == nil {
		return p.writeJPEG(node.ID, img, thumbQuality)
	}
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return &svgOverLimit{reason: skipReason(SkipSVGTimeout, lim.Timeout.Milliseconds())}
	}
	if !p.caps.RSVG || ctx.Err() != nil {
		return fmt.Errorf("thumb: %w", err)
	}
	fctx, fcancel := context.WithTimeout(ctx, lim.Timeout)
	defer fcancel()
	img, rerr := rsvgConvert(fctx, data)
	if rerr != nil {
		if fctx.Err() != nil && ctx.Err() == nil {
			return &svgOverLimit{reason: skipReason(SkipSVGTimeout, lim.Timeout.Milliseconds())}
		}
		return fmt.Errorf("thumb: %w; rsvg-convert: %v", err, rerr)
	}
	return p.writeJPEG(node.ID, img, thumbQuality)
}

// rsvgConvert is the fallback: librsvg's command-line tool, given the file
// alone in an empty directory (so a relative href has nothing to find).
func rsvgConvert(ctx context.Context, data []byte) (image.Image, error) {
	bin, err := exec.LookPath("rsvg-convert")
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "filex-svg-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	srcPath := filepath.Join(tmpDir, "input.svg")
	if err := os.WriteFile(srcPath, data, 0o600); err != nil {
		return nil, err
	}
	pngPath := filepath.Join(tmpDir, "out.png")
	cmd := exec.CommandContext(ctx, bin,
		"--width", fmt.Sprint(thumbMaxWidth),
		"--height", fmt.Sprint(thumbMaxHeight),
		"--keep-aspect-ratio",
		"--format", "png",
		"--output", pngPath,
		srcPath,
	)
	cmd.WaitDelay = time.Second
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%w (%s)", err, bytes.TrimSpace(combined))
	}
	f, err := os.Open(pngPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return img, nil
}

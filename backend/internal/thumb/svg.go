package thumb

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// generateSVG rasterises an SVG via librsvg's `rsvg-convert` binary,
// then re-encodes to JPEG so every cached thumbnail in cacheDir keeps
// the same file extension (the manager projection emits `<id>.jpg` for
// every state="ready" row).
//
// We deliberately don't use a pure-Go SVG library:
//   - srwiley/oksvg is the most popular, but it's reflection-based and
//     fails on real-world SVGs that lean on CSS or filter primitives.
//   - librsvg is the same engine GNOME ships, well-tested, and the
//     alpine package is ~12 MB compressed.
//
// The .Capabilities() probe wires `caps.SVG = true` only when
// rsvg-convert is on PATH, so this generator never runs without the
// dep present (the dispatcher in pipeline.go falls back to
// state="skipped" instead).
func (p *Pipeline) generateSVG(ctx context.Context, node *model.Node, drv storage.Driver) error {
	bin, _ := exec.LookPath("rsvg-convert")
	if bin == "" {
		return fmt.Errorf("thumb: rsvg-convert not in PATH")
	}

	tmpDir, err := os.MkdirTemp("", "filex-svg-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	srcName := filepath.Base(node.Name)
	if srcName == "" {
		srcName = "input.svg"
	}
	srcPath := filepath.Join(tmpDir, srcName)
	src, err := os.Create(srcPath)
	if err != nil {
		return err
	}
	rc, err := drv.Read(ctx, node.Path)
	if err != nil {
		src.Close()
		return err
	}
	if _, err := io.Copy(src, rc); err != nil {
		rc.Close()
		src.Close()
		return err
	}
	rc.Close()
	src.Close()

	pngPath := filepath.Join(tmpDir, "out.png")
	cmd := exec.CommandContext(ctx, bin,
		"--width", fmt.Sprint(thumbMaxWidth),
		"--height", fmt.Sprint(thumbMaxHeight),
		"--keep-aspect-ratio",
		"--format", "png",
		"--output", pngPath,
		srcPath,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("thumb: rsvg-convert: %w (%s)", err, string(combined))
	}

	// Decode the rasterised PNG and re-encode as JPEG into cacheDir.
	pngFile, err := os.Open(pngPath)
	if err != nil {
		return err
	}
	defer pngFile.Close()
	img, _, err := image.Decode(pngFile)
	if err != nil {
		return fmt.Errorf("thumb: decode rasterised svg: %w", err)
	}
	if err := os.MkdirAll(p.cacheDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID))
	dst, err := os.Create(out)
	if err != nil {
		return err
	}
	defer dst.Close()
	return jpeg.Encode(dst, img, &jpeg.Options{Quality: thumbQuality})
}

package thumb

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
)

// writeJPEG is the one way a Go-drawn thumbnail reaches the cache: drawn
// over a grey checkerboard, encoded, and renamed over the node's <id>.jpg.
//
//   - Over a checkerboard, because a JPEG has no alpha channel and the
//     encoder reads a transparent pixel as black: every transparent PNG and
//     every SVG with no background of its own became a picture on a black
//     slab (0.49). The checkerboard is what an image editor shows for "no
//     background", so the thumbnail says the picture is transparent where it
//     is, and its two mid greys keep a black logo and a white one legible, on
//     a light theme and a dark one; plain white would hide a white logo. An
//     opaque picture covers it entirely.
//   - Renamed, because a refreshed file keeps serving its old picture while the
//     new one is drawn (GenerateThumb). Writing the cache file in place would
//     let a request read it half written.
func (p *Pipeline) writeJPEG(nodeID int64, img image.Image, quality int) error {
	if err := os.MkdirAll(p.cacheDir, 0o755); err != nil {
		return err
	}
	b := img.Bounds()
	flat := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	drawChecker(flat)
	draw.Draw(flat, flat.Bounds(), img, b.Min, draw.Over)
	tmp, err := os.CreateTemp(p.cacheDir, fmt.Sprintf(".%d-*.tmp", nodeID))
	if err != nil {
		return err
	}
	if err := jpeg.Encode(tmp, flat, &jpeg.Options{Quality: quality}); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", nodeID))); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// The transparency checkerboard: squares of CheckerSize pixels, the top-left
// one CheckerLight.
const CheckerSize = 8

var (
	CheckerLight = color.RGBA{0xCC, 0xCC, 0xCC, 0xFF}
	CheckerDark  = color.RGBA{0x99, 0x99, 0x99, 0xFF}
)

func drawChecker(dst *image.RGBA) {
	light, dark := image.NewUniform(CheckerLight), image.NewUniform(CheckerDark)
	b := dst.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += CheckerSize {
		for x := b.Min.X; x < b.Max.X; x += CheckerSize {
			src := light
			if ((x-b.Min.X)/CheckerSize+(y-b.Min.Y)/CheckerSize)%2 == 1 {
				src = dark
			}
			draw.Draw(dst, image.Rect(x, y, x+CheckerSize, y+CheckerSize).Intersect(b), src, image.Point{}, draw.Src)
		}
	}
}

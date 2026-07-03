package thumb

import (
	"context"
	"fmt"
	"image"
	_ "image/gif" // register GIF decoder
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"os"
	"path/filepath"

	// Register additional image formats used by the SFC's example
	// fixtures + real-world S3 storage. Without these the pipeline
	// flags rows as state="failed" and the GridView falls back to
	// the file-type emoji forever.
	_ "golang.org/x/image/bmp"  // bmp
	_ "golang.org/x/image/tiff" // tiff (scan.tiff fixture)
	_ "golang.org/x/image/webp" // webp (photo.webp fixture)

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

const (
	thumbMaxWidth  = 320
	thumbMaxHeight = 320
	thumbQuality   = 80
)

// generateImage creates a thumbnail JPEG using the standard library.
//
// It does NOT pull in disintegration/imaging (saves ~1MB of binary) — for
// sub-square downsampling we use a simple nearest-neighbour-ish step that's
// fine for 320px previews. Use libvips for production-quality scaling.
func (p *Pipeline) generateImage(ctx context.Context, node *model.Node, drv storage.Driver) error {
	rc, err := drv.Read(ctx, node.Path)
	if err != nil {
		return err
	}
	defer rc.Close()
	src, _, err := image.Decode(io.LimitReader(rc, 50*1024*1024))
	if err != nil {
		return fmt.Errorf("thumb: decode: %w", err)
	}
	dst := scaleDown(src, thumbMaxWidth, thumbMaxHeight)

	if err := os.MkdirAll(p.cacheDir, 0o755); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID)))
	if err != nil {
		return err
	}
	defer out.Close()
	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return err
	}
	return nil
}

// scaleDown is a stdlib-only nearest-neighbour resize keeping aspect ratio.
// For higher fidelity, swap in golang.org/x/image/draw with BiLinear.
func scaleDown(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW && h <= maxH {
		return src
	}
	ratio := float64(w) / float64(h)
	tw, th := maxW, maxH
	if ratio > float64(maxW)/float64(maxH) {
		th = int(float64(maxW) / ratio)
	} else {
		tw = int(float64(maxH) * ratio)
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		sy := y * h / th
		for x := 0; x < tw; x++ {
			sx := x * w / tw
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

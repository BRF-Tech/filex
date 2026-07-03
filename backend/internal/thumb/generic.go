package thumb

import (
	"context"
	"crypto/md5"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/brf-tech/filex/backend/internal/model"
)

// generateGeneric writes a deterministic placeholder thumbnail for kinds
// the rich pipeline can't handle directly (3D models, archives, code,
// markdown, etc). The image is a tinted card with the extension text
// centered — small enough that grid views don't look broken, and the
// colour is hashed from the extension so similar files cluster visually.
func (p *Pipeline) generateGeneric(_ context.Context, node *model.Node) error {
	if err := os.MkdirAll(p.cacheDir, 0o755); err != nil {
		return err
	}
	ext := strings.ToUpper(strings.TrimPrefix(extOf(node.Name), "."))
	if ext == "" {
		ext = "FILE"
	}
	const w, h = thumbMaxWidth, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	bg, fg := colourForExt(ext)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, bg)
		}
	}
	// Center the extension text using the bundled basicfont (7×13 glyphs).
	face := basicfont.Face7x13
	advance := font.MeasureString(face, ext).Round()
	dx := (w - advance) / 2
	dy := (h + face.Metrics().Ascent.Round()) / 2
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(fg),
		Face: face,
		Dot:  fixed.P(dx, dy),
	}
	d.DrawString(ext)

	out, err := os.Create(filepath.Join(p.cacheDir, fmt.Sprintf("%d.jpg", node.ID)))
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, img, &jpeg.Options{Quality: 78})
}

// colourForExt hashes the extension to a stable tint with a luminance-
// aware foreground so the text stays readable on every colour.
func colourForExt(ext string) (color.Color, color.Color) {
	h := md5.Sum([]byte(strings.ToLower(ext)))
	r := 60 + int(h[0])%160
	g := 60 + int(h[1])%160
	b := 60 + int(h[2])%160
	bg := color.RGBA{uint8(r), uint8(g), uint8(b), 0xff}
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if lum > 130 {
		return bg, color.RGBA{0x1a, 0x1e, 0x27, 0xff}
	}
	return bg, color.RGBA{0xf6, 0xf8, 0xfb, 0xff}
}

func extOf(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 {
		return ""
	}
	return name[idx:]
}

package thumb

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// A thumbnail made of lines of text: a text file's first lines (text.go), an
// archive's entry names (archive.go). Landscape, the shape of the cards and of
// the prints a folder shows, drawn in the Go fonts filex embeds, on a page
// that reads on a light theme and a dark one.
const (
	linesW      = 320
	linesH      = 240
	linesMargin = 14
	linesSize   = 13
	linesLead   = 16
)

var (
	linesPaper = color.RGBA{0xFB, 0xFB, 0xF8, 0xFF}
	linesInk   = color.RGBA{0x2A, 0x2E, 0x36, 0xFF}
	linesMuted = color.RGBA{0x7A, 0x80, 0x8A, 0xFF}
	linesRule  = color.RGBA{0xE2, 0xE4, 0xE8, 0xFF}
)

// textLine is one line to draw: bold for a heading or a folder, muted for a
// note under the list (how many more).
type textLine struct {
	Text  string
	Bold  bool
	Muted bool
}

var (
	fontsOnce             sync.Once
	regularFont, boldFont *opentype.Font
	fontsErr              error
)

// faces returns a regular and a bold face for ONE drawing.
//
// ⚠⚠ The parsed fonts are shared, the faces are not. "A Face is not safe to
// use concurrently" (golang.org/x/image/font/opentype): it keeps a glyph
// buffer of its own. Text and archive thumbnails are drawn on whichever
// goroutine finished the upload, so two are drawn at once all the time, and
// one package-wide Face raced on that buffer: a full e2e run on 2026-10-01
// died with "index out of range [3] with length 0" in sfnt.LoadGlyph under
// drawLines, and the panic took the whole server down. Parsing is the costly
// part and an opentype.Font is safe to share; a Face is a small struct, so
// every drawing gets its own pair and thumbnails still draw in parallel.
func faces() (font.Face, font.Face, error) {
	fontsOnce.Do(func() {
		if regularFont, fontsErr = opentype.Parse(gomono.TTF); fontsErr != nil {
			return
		}
		boldFont, fontsErr = opentype.Parse(gomonobold.TTF)
	})
	if fontsErr != nil {
		return nil, nil, fontsErr
	}
	opts := &opentype.FaceOptions{Size: linesSize, DPI: 72, Hinting: font.HintingFull}
	regular, err := opentype.NewFace(regularFont, opts)
	if err != nil {
		return nil, nil, err
	}
	bold, err := opentype.NewFace(boldFont, opts)
	if err != nil {
		return nil, nil, err
	}
	return regular, bold, nil
}

// drawLines draws lines top down, each cut to the page's width with an
// ellipsis, as many as fit. header, when not empty, is a strip across the
// top (an archive's kind and count).
func drawLines(header string, lines []textLine) (image.Image, error) {
	reg, bld, err := faces()
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, linesW, linesH))
	draw.Draw(img, img.Bounds(), image.NewUniform(linesPaper), image.Point{}, draw.Src)
	y := linesMargin
	if header != "" {
		draw.Draw(img, image.Rect(0, 0, linesW, linesMargin+linesLead+4), image.NewUniform(linesRule), image.Point{}, draw.Src)
		put(img, bld, linesMuted, linesMargin, y+linesSize-2, fit(bld, header, linesW-2*linesMargin))
		y += linesLead + 10
	}
	for _, ln := range lines {
		if y+linesLead > linesH-linesMargin/2 {
			break
		}
		face, ink := reg, linesInk
		if ln.Bold {
			face = bld
		}
		if ln.Muted {
			ink = linesMuted
		}
		put(img, face, ink, linesMargin, y+linesSize-2, fit(face, ln.Text, linesW-2*linesMargin))
		y += linesLead
	}
	return img, nil
}

func put(dst draw.Image, face font.Face, ink color.Color, x, baseline int, s string) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(ink), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}

// fit cuts s to width pixels, ending in an ellipsis when it had to cut.
func fit(face font.Face, s string, width int) string {
	if font.MeasureString(face, s).Round() <= width {
		return s
	}
	const ell = "…"
	for len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
		if font.MeasureString(face, s+ell).Round() <= width {
			return strings.TrimRight(s, " ") + ell
		}
	}
	return ell
}

// Package thumbkit helps an app draw thumbnails (filex 0.50 and later): the
// answer's encoding, and a card of lines in the look filex's own archive and
// text thumbnails have - what an app that lists a file's contents (an
// archive kind filex does not read, a package, a playlist) wants to draw.
//
//	pluginkit.Run(&pluginkit.Plugin{
//		Manifest:  manifest,
//		Thumbnail: func(in *wire.ThumbnailInput) (*wire.ThumbnailOutput, error) {
//			src, err := pluginkit.ThumbnailSource(in)
//			if err != nil {
//				return nil, err
//			}
//			defer src.Close()
//			names, err := listMyFormat(src)
//			if err != nil {
//				return nil, err // filex asks the next handler
//			}
//			return thumbkit.PNG(thumbkit.ListCard("MYFMT · 12 files", names, 0))
//		},
//	})
//
// It is plain Go (image, image/png and the 7x13 bitmap face of
// golang.org/x/image), so it works the same off-wasm, in a test.
package thumbkit

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// PNG encodes img as a thumbnail answer, refusing what filex would refuse: an
// image wider or taller than wire.ThumbnailMaxPixels, or one heavier than
// wire.ThumbnailMaxOutputBytes once encoded.
func PNG(img image.Image) (*wire.ThumbnailOutput, error) {
	if img == nil {
		return nil, errors.New("thumbkit: no image")
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > wire.ThumbnailMaxPixels || b.Dy() > wire.ThumbnailMaxPixels {
		return nil, errors.New("thumbkit: an image is 1.." + strconv.Itoa(wire.ThumbnailMaxPixels) + " pixels on each side")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	if buf.Len() > wire.ThumbnailMaxOutputBytes {
		return nil, errors.New("thumbkit: the encoded image is over " + strconv.Itoa(wire.ThumbnailMaxOutputBytes) + " bytes")
	}
	return &wire.ThumbnailOutput{Image: buf.Bytes()}, nil
}

// The card ListCard draws: landscape, a header strip, lines in a monospaced
// face, a note under them - on a page that reads on a light theme and a dark
// one (filex lays it on its transparency checkerboard; this one is opaque).
const (
	cardW      = 320
	cardH      = 240
	cardMargin = 12
	cardLead   = 16
)

var (
	paper = color.RGBA{0xFB, 0xFB, 0xF8, 0xFF}
	ink   = color.RGBA{0x2A, 0x2E, 0x36, 0xFF}
	muted = color.RGBA{0x7A, 0x80, 0x8A, 0xFF}
	strip = color.RGBA{0xE2, 0xE4, 0xE8, 0xFF}
)

// ListCard draws header across the top and lines under it, as many as fit,
// each cut to the card's width; more > 0 adds "+N more" under the list.
// Letters outside ASCII are drawn as "?": the face is a 7x13 bitmap, small
// enough for every app to carry.
func ListCard(header string, lines []string, more int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, cardW, cardH))
	draw.Draw(img, img.Bounds(), image.NewUniform(paper), image.Point{}, draw.Src)
	face := basicfont.Face7x13
	maxChars := (cardW - 2*cardMargin) / 7
	y := cardMargin
	if header != "" {
		draw.Draw(img, image.Rect(0, 0, cardW, cardMargin+cardLead), image.NewUniform(strip), image.Point{}, draw.Src)
		write(img, face, ink, cardMargin, y+11, cut(header, maxChars))
		y += cardLead + 6
	}
	shown := 0
	for _, l := range lines {
		if y+cardLead > cardH-cardMargin-cardLead {
			break
		}
		write(img, face, ink, cardMargin, y+11, cut(l, maxChars))
		y += cardLead
		shown++
	}
	if rest := more + len(lines) - shown; rest > 0 {
		write(img, face, muted, cardMargin, y+11, "+"+strconv.Itoa(rest)+" more")
	}
	return img
}

func write(dst *image.RGBA, face font.Face, c color.Color, x, baseline int, s string) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(ascii(s))
}

// ascii replaces what the bitmap face cannot draw.
func ascii(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		out = append(out, r)
	}
	return string(out)
}

// cut shortens s to n characters, the last one an ellipsis of dots.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n < 4 {
		return s
	}
	return string(r[:n-3]) + "..."
}

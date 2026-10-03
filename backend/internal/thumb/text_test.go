package thumb

import (
	"bytes"
	"context"
	"image/color"
	"image/jpeg"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// A text file's thumbnail is its own first lines (text.go), drawn on a light
// page, not the extension's placeholder card.

func TestTextLines(t *testing.T) {
	lines := textLines("\n\n# Başlık\n\tgirintili\x07satır\r\n## Alt\nnormal #değil\n\xff\xfe bozuk\n", true)
	var got []string
	for _, l := range lines {
		if l.Bold {
			got = append(got, "*"+l.Text)
		} else {
			got = append(got, l.Text)
		}
	}
	require.Equal(t, []string{"*Başlık", "    girintilisatır", "*Alt", "normal #değil", "� bozuk"}, got)

	plain := textLines("# not a heading in a .txt\n", false)
	require.Equal(t, "# not a heading in a .txt", plain[0].Text)
	require.False(t, plain[0].Bold)

	var long bytes.Buffer
	for i := 0; i < 40; i++ {
		long.WriteString("line\n")
	}
	require.Len(t, textLines(long.String(), false), 16, "a page of lines, not the whole file")
}

func TestIsTextThumb(t *testing.T) {
	for name, want := range map[string]bool{
		"notes.txt": true, "README.MD": true, "plan.markdown": true, "a.text": true,
		"main.go": false, "data.csv": false, "README": false, "a.txt.zip": false,
	} {
		require.Equal(t, want, isTextThumb(name), name)
	}
}

// A .txt and a .md are drawn as their lines on the light page; the corner is
// the page's paper, not the placeholder card's tint.
func TestText_DrawnAsItsLines(t *testing.T) {
	for _, name := range []string{"tutanak.txt", "plan.md"} {
		t.Run(name, func(t *testing.T) {
			f := newArchiveFixture(t, nil)
			n := f.put(name, []byte("# Plan\n\nBirinci hafta\nİkinci hafta\n"))
			n.Mime = "text/plain; charset=utf-8"
			require.NoError(t, f.p.GenerateThumb(context.Background(), n))
			require.Equal(t, "ready", f.row(n.ID).State)
			b, err := os.ReadFile(f.p.CachePath(n.ID))
			require.NoError(t, err)
			img, err := jpeg.Decode(bytes.NewReader(b))
			require.NoError(t, err)
			require.Equal(t, linesW, img.Bounds().Dx())
			r, g, bl, _ := img.At(linesW-3, linesH-3).RGBA()
			corner := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 0xff}
			require.Greater(t, int(corner.R), 240, "the corner is the paper, not a tinted card: %v", corner)
			require.Greater(t, int(corner.B), 235)
			// Somewhere in the first line there is ink.
			dark := false
			for x := linesMargin; x < linesMargin+60 && !dark; x++ {
				for y := linesMargin; y < linesMargin+linesLead; y++ {
					if r, _, _, _ := img.At(x, y).RGBA(); r>>8 < 120 {
						dark = true
						break
					}
				}
			}
			require.True(t, dark, "no line was drawn")
		})
	}
}

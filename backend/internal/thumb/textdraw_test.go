package thumb

import (
	"bytes"
	"fmt"
	"image"
	"sync"
	"testing"
)

// Text and archive thumbnails are drawn on whichever goroutine finished the
// upload, so two are drawn at once all the time. They shared ONE font.Face,
// and an opentype.Face is not safe to use concurrently (it keeps a glyph
// buffer of its own): a full e2e run on 2026-10-01 died with "index out of
// range [3] with length 0" in sfnt.LoadGlyph under drawLines, the panic took
// the whole server down, and every spec after it failed to connect.
//
// Every concurrent drawing must come out pixel for pixel the same as the same
// lines drawn alone. Run with -race to see the shared face reported; without
// it the shared version panics or smears glyphs within these iterations.
func TestDrawLines_SameUnderConcurrency(t *testing.T) {
	header := "archive.zip - 12 entries"
	lines := []textLine{
		{Text: "Documents/", Bold: true},
		{Text: "report-2026-final-version.docx"},
		{Text: "Invoices/2026/Q3/supplier-0042.pdf"},
		{Text: "photos/IMG_20260930_101112.jpg"},
		{Text: "notes.txt - the quick brown fox jumps over the lazy dog"},
		{Text: "and 7 more", Muted: true},
	}
	want, err := drawLines(header, lines)
	if err != nil {
		t.Fatalf("drawLines: %v", err)
	}
	ref := want.(*image.RGBA).Pix

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				got, err := drawLines(header, lines)
				if err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(got.(*image.RGBA).Pix, ref) {
					errs <- fmt.Errorf("goroutine %d, drawing %d: the pixels differ from the same lines drawn alone", g, i)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

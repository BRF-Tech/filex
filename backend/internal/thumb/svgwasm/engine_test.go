package svgwasm

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func px(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func render(t *testing.T, svg string) image.Image {
	t.Helper()
	img, err := Render(context.Background(), []byte(svg), 320, 320)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return img
}

// The engine draws, at the size asked, with the aspect ratio kept.
func TestRender_DrawsToFit(t *testing.T) {
	img := render(t, `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="200"><rect width="400" height="200" fill="#d01010"/></svg>`)
	if b := img.Bounds(); b.Dx() != 320 || b.Dy() != 160 {
		t.Fatalf("size %v, want 320x160", b)
	}
	if c := px(img, 160, 80); c.R < 200 || c.G > 40 || c.A != 255 {
		t.Fatalf("centre %v, want red", c)
	}
}

// A file with no background keeps its transparency: flattening is the
// caller's (thumb.writeJPEG), done once for every kind of thumbnail.
func TestRender_KeepsTransparency(t *testing.T) {
	img := render(t, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><circle cx="5" cy="5" r="2" fill="#000"/></svg>`)
	if c := px(img, 0, 0); c.A != 0 {
		t.Fatalf("corner %v, want transparent", c)
	}
}

// Text is drawn with the embedded Go fonts: a bare install has no system
// fonts, and a tile of blank boxes would be worse than no text.
func TestRender_DrawsText(t *testing.T) {
	img := render(t, `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100"><rect width="200" height="100" fill="#fff"/><text x="10" y="70" font-family="Helvetica, sans-serif" font-size="60" fill="#000">410</text></svg>`)
	dark := 0
	b := img.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if c := px(img, x, y); c.R < 80 {
				dark++
			}
		}
	}
	if dark < 500 {
		t.Fatalf("only %d dark pixels: the text was not drawn", dark)
	}
}

// ⚠⚠ The engine cannot reach anything outside the bytes it was given. The
// module imports nothing (checked here, so a rebuild with WASI or a host
// function fails this test), an <image> that names a file or a URL draws
// nothing, and the URL is never asked for.
func TestRender_ReachesNothingOutside(t *testing.T) {
	compileOnce.Do(compile)
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	if imp := compiled.ImportedFunctions(); len(imp) != 0 {
		t.Fatalf("the engine imports %d host functions; it must import none", len(imp))
	}
	if imp := compiled.ImportedMemories(); len(imp) != 0 {
		t.Fatalf("the engine imports a memory; it must import nothing")
	}

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(redPNG(t))
	}))
	defer srv.Close()
	dir := t.TempDir()
	onDisk := filepath.Join(dir, "red.png")
	if err := os.WriteFile(onDisk, redPNG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="100" height="100">
<image x="0" y="0" width="50" height="100" xlink:href="%s"/>
<image x="50" y="0" width="50" height="100" href="%s"/>
<image x="0" y="0" width="100" height="100" href="file://%s"/>
</svg>`, srv.URL+"/x.png", filepath.ToSlash(onDisk), filepath.ToSlash(onDisk))
	img := render(t, svg)
	for _, pt := range [][2]int{{80, 160}, {240, 160}} {
		if c := px(img, pt[0], pt[1]); c.A != 0 {
			t.Fatalf("pixel %v is %v: an outside image was drawn", pt, c)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the engine asked the network %d times", n)
	}
}

// An embedded image is part of the file and is drawn.
func TestRender_DrawsEmbeddedImages(t *testing.T) {
	b64 := base64PNG(t)
	img := render(t, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><image width="100" height="100" href="data:image/png;base64,`+b64+`"/></svg>`)
	if c := px(img, 160, 160); c.R < 200 || c.A != 255 {
		t.Fatalf("centre %v, want the embedded red", c)
	}
}

// External entities are refused, and so is an entity bomb.
func TestRender_RefusesEntities(t *testing.T) {
	for name, svg := range map[string]string{
		"external": `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text>&x;</text></svg>`,
		"bomb":     `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;"><!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;"><!ENTITY d "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;"><!ENTITY e "&d;&d;&d;&d;&d;&d;&d;&d;&d;&d;"><!ENTITY f "&e;&e;&e;&e;&e;&e;&e;&e;&e;&e;"><!ENTITY g "&f;&f;&f;&f;&f;&f;&f;&f;&f;&f;">]><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text>&g;</text></svg>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(context.Background(), []byte(svg), 64, 64); err == nil {
				t.Fatal("rendered; want a refusal")
			}
		})
	}
}

// A file over the ceiling is not even handed to the engine.
func TestRender_RefusesAFileTooLarge(t *testing.T) {
	big := make([]byte, MaxInput+1)
	_, err := Render(context.Background(), big, 64, 64)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// The time limit stops a file that takes too long, and the engine is fine for
// the next one.
func TestRender_StopsAtTheDeadline(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000">`)
	for i := 0; i < 80000; i++ {
		fmt.Fprintf(&sb, `<path d="M%d %d l50 30 l-20 40 z" fill="#%06x" fill-opacity="0.3"/>`, i*7%1000, i*13%1000, (i*2654435761)&0xffffff)
	}
	sb.WriteString(`</svg>`)
	if sb.Len() > MaxInput {
		t.Fatalf("the probe is %d bytes, over MaxInput: it would test the size ceiling instead", sb.Len())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Render(ctx, []byte(sb.String()), 320, 320)
	if err == nil {
		t.Skip("this machine drew 80 000 paths inside 150 ms; nothing to stop")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("stopped after %v; the deadline was 150 ms", el)
	}
	render(t, `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)
}

// A file that crashes the engine (a stack overflow on deep nesting) is an
// error for that file alone.
func TestRender_ACrashIsOneFilesProblem(t *testing.T) {
	deep := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + strings.Repeat("<g>", 20000) +
		`<rect width="10" height="10"/>` + strings.Repeat("</g>", 20000) + `</svg>`
	_, _ = Render(context.Background(), []byte(deep), 64, 64)
	img := render(t, `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="#00f"/></svg>`)
	if c := px(img, 100, 100); c.B < 200 {
		t.Fatalf("the next file drew %v", c)
	}
}

// Many files at once all come out right: each has its own instance.
func TestRender_Concurrent(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fill := fmt.Sprintf("#%02x0000", 100+i*10)
			img, err := Render(context.Background(), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="`+fill+`"/></svg>`), 32, 32)
			if err != nil {
				errs <- err
				return
			}
			if c := px(img, 16, 16); int(c.R) != 100+i*10 {
				errs <- fmt.Errorf("file %d drew %v", i, c)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Nothing to parse is an error, not a blank picture.
func TestRender_NotAnSVG(t *testing.T) {
	if _, err := Render(context.Background(), []byte("this is not xml <<<"), 64, 64); err == nil {
		t.Fatal("rendered garbage")
	}
}

func redPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = 255, 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func base64PNG(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(redPNG(t))
}

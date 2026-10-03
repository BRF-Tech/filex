// Package fakemagick writes a stand-in for ImageMagick: a shell script that
// answers the way an ImageMagick of a given kind does, so the code that runs
// ImageMagick (enginebin.HEIC, thumb.generateHEIF) is tested through a real
// exec, on a machine that has no ImageMagick at all.
//
// The kind that matters is NoHEVC: ImageMagick on Ubuntu 24.04, whose libheif1
// lists HEIC and decodes none of it until libheif-plugin-libde265 is
// installed. Its message is the one measured in ubuntu:24.04 (ImageMagick
// 6.9.12, libheif 1.17.6) on the sample enginebin decodes at boot.
//
// Unix only: the stand-in is a /bin/sh script. On Windows every constructor
// skips the test.
package fakemagick

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Unsupported is what ImageMagick 6 on Ubuntu 24.04 says for a HEIC when
// libheif has no HEVC decoder (`%s` is the file).
const Unsupported = "convert-im6.q16: Unsupported feature: Unsupported codec (4.3000) `%s' @ error/heic.c/IsHEIFSuccess/139."

// SampleColour is the colour of enginebin's HEIC sample, and of the picture
// a decoding stand-in hands back for every file.
var SampleColour = color.RGBA{R: 220, G: 60, B: 40, A: 255}

// Picture is a w x h PNG of one colour.
func Picture(t testing.TB, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Decoding is an ImageMagick that draws every picture: it hands back a 64x64
// PNG of SampleColour, which is what the HEIC sample decodes to.
func Decoding(t testing.TB) string {
	return Custom(t, `cat "$d/picture.png"`)
}

// NoHEVC is an ImageMagick whose libheif has no HEVC decoder: a file whose
// brand is a HEVC one (heic, heix, heim, heis, hevc, hevx, mif1) fails with
// Unsupported, and anything else (an AVIF: its AV1 decoder is another
// plugin, which libheif1 depends on) is drawn.
func NoHEVC(t testing.TB) string {
	return Custom(t, `brand=$(dd if="$src" bs=1 skip=8 count=4 2>/dev/null)
case "$brand" in
heic|heix|heim|heis|hevc|hevx|mif1)
	cat "$d/unsupported.txt" >&2
	exit 1
	;;
esac
cat "$d/picture.png"`)
}

// Custom is an ImageMagick that runs body (sh) after the bookkeeping every
// stand-in does: each run is a line in runs.log, its arguments go to
// args.txt, its input file (the first argument without "[0]") is copied to
// input. In body, $d is the stand-in's folder, $src the input file, and
// $d/picture.png the 64x64 SampleColour PNG.
func Custom(t testing.TB, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the ImageMagick stand-in is a shell script")
	}
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "picture.png"), Picture(t, 64, 64, SampleColour), 0o644); err != nil {
		t.Fatal(err)
	}
	// From a file, not from the script: the message holds a backquote, which
	// sh would run as a command inside double quotes.
	if err := os.WriteFile(filepath.Join(d, "unsupported.txt"), []byte(fmt.Sprintf(Unsupported, "input.heic")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Rewrite(t, filepath.Join(d, "magick"), body)
}

// Rewrite puts body in the stand-in at path (made by Custom), or makes one
// there: the binary changes on disk, as an upgrade changes it.
func Rewrite(t testing.TB, path, body string) string {
	t.Helper()
	d := filepath.Dir(path)
	script := "#!/bin/sh\n" +
		"d='" + d + "'\n" +
		`echo run >> "$d/runs.log"` + "\n" +
		`printf '%s\n' "$@" > "$d/args.txt"` + "\n" +
		`src="${1%\[0\]}"` + "\n" +
		`cp "$src" "$d/input" 2>/dev/null` + "\n" +
		body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// WriteBeside puts a file in the stand-in's folder, for a body to hand back
// ("$d/<name>").
func WriteBeside(t testing.TB, path, name string, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Runs is how many times the stand-in at path has been run.
func Runs(t testing.TB, path string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "runs.log"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

// Args are the arguments of the stand-in's last run.
func Args(t testing.TB, path string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// Input is the file the stand-in's last run was given.
func Input(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "input"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

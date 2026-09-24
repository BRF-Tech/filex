package enginebin

import (
	"errors"
	"testing"
)

// fakeHost is a PATH and a set of version banners.
type fakeHost struct {
	path    map[string]string // binary name → resolved path
	banners map[string]string // resolved path → `-version` output
	ran     []string          // paths whose banner was asked for
}

func (h *fakeHost) prober(systemRoot string) prober {
	return prober{
		lookPath: func(name string) (string, error) {
			if p, ok := h.path[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		banner: func(bin string) []byte {
			h.ran = append(h.ran, bin)
			return []byte(h.banners[bin])
		},
		systemRoot: systemRoot,
	}
}

const imBanner = "Version: ImageMagick 7.1.1-38 Q16-HDRI x64 https://imagemagick.org"

// The release-candidate sweep, 2026-09-21: on Windows `convert` is
// C:\Windows\System32\convert.exe, the FAT→NTFS disk converter. It must
// never count as ImageMagick — and it must not even be RUN to find out.
func TestTheWindowsDiskToolIsNeverImageMagick(t *testing.T) {
	for _, path := range []string{
		`C:\Windows\System32\convert.exe`,
		`c:\windows\system32\CONVERT.EXE`,
		`C:/Windows/System32/convert.exe`,
		`D:\Win\SysWOW64\convert.exe`,
	} {
		h := &fakeHost{
			path: map[string]string{"convert": path},
			// even a banner that claims to be ImageMagick does not rescue it
			banners: map[string]string{path: imBanner},
		}
		s := h.prober(`D:\Win`).probe()
		if s.Has(ImageMagick) {
			t.Errorf("%s was taken for ImageMagick", path)
		}
		if len(h.ran) != 0 {
			t.Errorf("%s was run to ask its version; a system disk tool is refused by where it lives", path)
		}
	}
}

// ImageMagick 7's `magick` is preferred to the legacy `convert` — even when
// the `convert` beside it is a genuine ImageMagick 6 that would also pass.
func TestMagickIsProbedFirst(t *testing.T) {
	h := &fakeHost{
		path: map[string]string{
			"magick":  `C:\Program Files\ImageMagick-7\magick.exe`,
			"convert": `C:\Program Files\ImageMagick-6\convert.exe`,
		},
		banners: map[string]string{
			`C:\Program Files\ImageMagick-7\magick.exe`:  imBanner,
			`C:\Program Files\ImageMagick-6\convert.exe`: "Version: ImageMagick 6.9.13-10 Q16 x64",
		},
	}
	s := h.prober(`C:\Windows`).probe()
	if got := s.Path(ImageMagick); got != `C:\Program Files\ImageMagick-7\magick.exe` {
		t.Fatalf("ImageMagick resolved to %q, want magick.exe", got)
	}
}

// A `convert` elsewhere that is not ImageMagick (its banner says so) is not
// the engine either; a genuine ImageMagick 6 `convert` is.
func TestConvertCountsOnlyWhenItSaysImageMagick(t *testing.T) {
	h := &fakeHost{
		path:    map[string]string{"convert": "/usr/local/bin/convert"},
		banners: map[string]string{"/usr/local/bin/convert": "convert: some other tool 1.0"},
	}
	if h.prober("").probe().Has(ImageMagick) {
		t.Fatal("a convert that does not say ImageMagick was accepted")
	}
	h.banners["/usr/local/bin/convert"] = "Version: ImageMagick 6.9.11-60 Q16 x86_64"
	if !h.prober("").probe().Has(ImageMagick) {
		t.Fatal("a genuine ImageMagick 6 convert was refused")
	}
}

// The other engines have no namesake to guard against; they are taken as
// found, without running anything.
func TestOtherEnginesAreTakenAsFound(t *testing.T) {
	h := &fakeHost{path: map[string]string{
		"ffmpeg": "/usr/bin/ffmpeg", "soffice": "/usr/bin/soffice", "gs": "/usr/bin/gs",
		"pdftoppm": "/usr/bin/pdftoppm", "rsvg-convert": "/usr/bin/rsvg-convert",
	}}
	s := h.prober("").probe()
	for _, e := range []string{FFmpeg, LibreOffice, Ghostscript, Poppler, RSVG} {
		if !s.Has(e) {
			t.Errorf("%s not found", e)
		}
	}
	if s.Has(ImageMagick) {
		t.Error("no ImageMagick on this PATH")
	}
	if len(h.ran) != 0 {
		t.Errorf("nothing needed running: %v", h.ran)
	}
	if got := s.Missing([]string{FFmpeg, ImageMagick}); len(got) != 1 || got[0] != ImageMagick {
		t.Errorf("missing %v", got)
	}
}

// Probe is ONE answer for the process; SetForTest swaps it and puts it back.
func TestProbeIsOneAnswer(t *testing.T) {
	restore := SetForTest(map[string]string{FFmpeg: "/x/ffmpeg"})
	if !Probe().Has(FFmpeg) || Probe().Has(ImageMagick) {
		t.Fatalf("the answer set for the test is not the one read: %v", Probe().Available())
	}
	if Probe() != Probe() {
		t.Fatal("two reads, two answers")
	}
	restore()
}

func TestDisplayNames(t *testing.T) {
	for id, want := range map[string]string{ImageMagick: "ImageMagick", FFmpeg: "FFmpeg", RSVG: "librsvg", "unknown": "unknown"} {
		if got := DisplayName(id); got != want {
			t.Errorf("%s → %q, want %q", id, got, want)
		}
	}
}

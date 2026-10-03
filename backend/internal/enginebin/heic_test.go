package enginebin

import (
	"bytes"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/testutil/fakemagick"
)

// The HEIC decode probe (heic.go): ImageMagick being installed is not
// ImageMagick decoding a HEIC. Found in the 0.50 test phase: on Ubuntu 24.04
// libheif1 comes without its HEVC decoder plugin, ImageMagick lists HEIC as a
// format, the capabilities said HEIC could be drawn, and every HEIC file
// ended `failed` in a thumbnail repair.

// withMagick makes path the ImageMagick this process resolved, with the
// probe's answer forgotten, and the probe's clocks put back afterwards.
func withMagick(t *testing.T, path string) {
	t.Helper()
	engines := map[string]string{}
	if path != "" {
		engines[ImageMagick] = path
	}
	restore := SetForTest(engines)
	timeout, recheck, retry := heicProbeTimeout, heicRecheck, heicRetry
	forgetHEIC()
	t.Cleanup(func() {
		restore()
		heicProbeTimeout, heicRecheck, heicRetry = timeout, recheck, retry
		forgetHEIC()
	})
}

func forgetHEIC() {
	heicAnswer.mu.Lock()
	heicAnswer.bin, heicAnswer.ident, heicAnswer.status = "", "", HEICStatus{}
	heicAnswer.looked, heicAnswer.retryAt = time.Time{}, time.Time{}
	heicAnswer.mu.Unlock()
}

// An ImageMagick that draws the sample decodes HEIC - and it was asked with
// the thumbnailer's own arguments, about the sample compiled into the binary.
func TestHEIC_AnImageMagickThatDrawsTheSampleDecodes(t *testing.T) {
	bin := fakemagick.Decoding(t)
	withMagick(t, bin)

	got := HEIC()
	if !got.Decodes {
		t.Fatalf("HEIC() = %+v, want Decodes", got)
	}
	if !bytes.Equal(fakemagick.Input(t, bin), heicProbeSample) {
		t.Fatal("ImageMagick was not handed the HEIC sample")
	}
	args := fakemagick.Args(t, bin)
	want := MagickThumbArgs("x", heicProbeSize, heicProbeSize)
	if len(args) != len(want) || !strings.HasSuffix(args[0], "[0]") || strings.Join(args[1:], " ") != strings.Join(want[1:], " ") {
		t.Fatalf("ImageMagick was run with %q, not the thumbnailer's arguments %q", args, want)
	}
}

// Ubuntu 24.04 without libheif-plugin-libde265: ImageMagick is installed, and
// the answer is no, in ImageMagick's own words. Before the probe the answer
// was "yes" (ImageMagick on PATH).
func TestHEIC_NoHEVCDecoderIsNo(t *testing.T) {
	bin := fakemagick.NoHEVC(t)
	withMagick(t, bin)

	got := HEIC()
	if got.Decodes {
		t.Fatal("an ImageMagick that cannot decode HEVC was taken for one that decodes HEIC")
	}
	if !strings.Contains(got.Detail, "Unsupported codec") {
		t.Fatalf("Detail %q does not carry ImageMagick's words", got.Detail)
	}
}

// Exiting 0 is not decoding: no picture, a picture of another size, or one of
// the wrong colour (a decoder that hands back black) all say no.
func TestHEIC_APictureThatIsNotTheSampleIsNo(t *testing.T) {
	for name, body := range map[string]string{
		"nothing":     `exit 0`,
		"not a png":   `echo "not a picture"`,
		"other size":  `cat "$d/small.png"`,
		"black":       `cat "$d/black.png"`,
		"exit status": `cat "$d/picture.png"; exit 1`,
	} {
		t.Run(name, func(t *testing.T) {
			bin := fakemagick.Custom(t, body)
			fakemagick.WriteBeside(t, bin, "small.png", fakemagick.Picture(t, 32, 32, fakemagick.SampleColour))
			fakemagick.WriteBeside(t, bin, "black.png", fakemagick.Picture(t, 64, 64, color.Black))
			withMagick(t, bin)
			if got := HEIC(); got.Decodes || got.Detail == "" {
				t.Fatalf("HEIC() = %+v, want no with a reason", got)
			}
		})
	}
}

// Once, not on every question: the answer is read for every HEIC thumbnail.
// Asked again only when the binary changes (another file at the path, as an
// upgrade leaves it, or another path).
func TestHEIC_ProbedOnceAndAgainWhenTheBinaryChanges(t *testing.T) {
	bin := fakemagick.NoHEVC(t)
	withMagick(t, bin)

	for i := 0; i < 5; i++ {
		if HEIC().Decodes {
			t.Fatal("NoHEVC decoded")
		}
	}
	if n := fakemagick.Runs(t, bin); n != 1 {
		t.Fatalf("ImageMagick ran %d times for five questions, want 1", n)
	}

	// The identity is looked at again (heicRecheck), and it is the same file:
	// no new run.
	heicRecheck = 0
	HEIC()
	if n := fakemagick.Runs(t, bin); n != 1 {
		t.Fatalf("an unchanged binary was probed again (%d runs)", n)
	}

	// The binary changes on disk (an upgrade that brought the decoder):
	// probed again, and the new answer is the one given.
	fakemagick.Rewrite(t, bin, `cat "$d/picture.png" # upgraded`)
	if !HEIC().Decodes {
		t.Fatal("the upgraded ImageMagick decodes, and the old answer was kept")
	}
	if n := fakemagick.Runs(t, bin); n != 2 {
		t.Fatalf("ImageMagick ran %d times after it changed, want 2", n)
	}

	// Another ImageMagick altogether.
	other := fakemagick.NoHEVC(t)
	restore := SetForTest(map[string]string{ImageMagick: other})
	defer restore()
	if HEIC().Decodes {
		t.Fatal("the answer about the previous ImageMagick was given for another")
	}
	if n := fakemagick.Runs(t, other); n != 1 {
		t.Fatalf("the other ImageMagick ran %d times, want 1", n)
	}
}

// A run that hangs is cut at the time limit, the answer is no, and it is not
// kept until the binary changes: a busy host at boot must not cost HEIC for
// the life of the process.
func TestHEIC_AHangIsCutAndAskedAgain(t *testing.T) {
	bin := fakemagick.Custom(t, `exec sleep 30`)
	withMagick(t, bin)
	heicProbeTimeout = 300 * time.Millisecond
	// "A minute later" is now: the retry is due at the next question.
	heicRetry = 0

	start := time.Now()
	got := HEIC()
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the probe waited %s for a hung ImageMagick", took)
	}
	if got.Decodes || !strings.Contains(got.Detail, "within") {
		t.Fatalf("HEIC() = %+v, want no, timed out", got)
	}

	HEIC()
	if n := fakemagick.Runs(t, bin); n != 2 {
		t.Fatalf("a timed-out probe was not asked again (%d runs)", n)
	}

	// A settled no (ImageMagick answered) is kept: not asked again while the
	// binary is the same, however often the question comes.
	fakemagick.Rewrite(t, bin, `echo "no decode delegate for this image format" >&2; exit 1`)
	HEIC()
	HEIC()
	if n := fakemagick.Runs(t, bin); n != 3 {
		t.Fatalf("a settled answer was asked again (%d runs, want 3)", n)
	}
}

// No ImageMagick: no, and nothing is run.
func TestHEIC_NoImageMagickIsNo(t *testing.T) {
	withMagick(t, "")
	got := HEIC()
	if got.Decodes || !strings.Contains(got.Detail, "not installed") {
		t.Fatalf("HEIC() = %+v", got)
	}
}

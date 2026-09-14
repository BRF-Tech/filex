package thumb

// What a card actually gets to draw.
//
// These are the two failures the thumbnail engine had that no green test ever
// noticed, because both of them END IN A ZERO EXIT CODE:
//
//   - ffmpeg asked to seek past the end of a clip prints "Output file is
//     empty, nothing was encoded" and exits 0. The pipeline then recorded
//     state=ready for a file that was never written, and the card 404ed.
//   - ghostscript renders A4 at 816×1056 and exits 0. Nothing downstream
//     scaled it, so a 184×108 card was fed a 358 KB full-page JPEG.
//
// So every assertion below is about the FILE, never about the exit status.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

/* ------------------------------------------------------------ page scaling */

// writeJPEG puts a real JPEG of the given size on disk — a gradient, so the
// encoder cannot collapse it to a handful of bytes and make a size assertion
// meaningless.
func writeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x + y) % 256), A: 0xff})
		}
	}
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, jpeg.Encode(f, img, &jpeg.Options{Quality: 80}))
	require.NoError(t, f.Close())
}

func decodeSize(t *testing.T, path string) (int, int) {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	return img.Bounds().Dx(), img.Bounds().Dy()
}

// A rendered page comes out of ghostscript at 816×1056. It must not reach the
// cache at that size: the card is 184 CSS px wide and can show no more detail
// than that, so the extra pixels are bytes on the wire bought for nothing.
//
// ⚠ RED on the old code — there was no scaling step at all between the
// renderer and the cache, in EITHER the PDF path or the office one.
func TestFitToThumb_ScalesARenderedPageDown(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "page.jpg")
	writeJPEG(t, page, 816, 1056)
	before, err := os.Stat(page)
	require.NoError(t, err)

	require.NoError(t, fitToThumb(page))

	w, h := decodeSize(t, page)
	require.LessOrEqual(t, w, thumbMaxWidth, "a page reached the cache wider than a thumbnail")
	require.LessOrEqual(t, h, thumbMaxHeight, "a page reached the cache taller than a thumbnail")
	require.Greater(t, w, 0)
	// The aspect ratio of the page survives — a page squashed to a square
	// would be a different bug wearing this fix's clothes.
	require.InDelta(t, 816.0/1056.0, float64(w)/float64(h), 0.02, "page aspect ratio was not preserved")

	after, err := os.Stat(page)
	require.NoError(t, err)
	require.Less(t, after.Size(), before.Size()/2,
		"scaling a full page down should cost well under half the bytes (was %d, now %d)", before.Size(), after.Size())
}

// An image already within the thumbnail box is left exactly as it is —
// re-encoding it would lose quality every time a backfill ran over it.
func TestFitToThumb_LeavesASmallImageAlone(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.jpg")
	writeJPEG(t, small, 200, 150)
	before, err := os.ReadFile(small)
	require.NoError(t, err)

	require.NoError(t, fitToThumb(small))

	after, err := os.ReadFile(small)
	require.NoError(t, err)
	require.Equal(t, before, after, "a thumbnail-sized image was re-encoded for no reason")
}

/* ------------------------------------------------------- the zero-exit trap */

func TestWroteFrame(t *testing.T) {
	dir := t.TempDir()
	require.False(t, wroteFrame(filepath.Join(dir, "nothing-here.jpg")), "a missing file is not a frame")

	empty := filepath.Join(dir, "empty.jpg")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	require.False(t, wroteFrame(empty), "a zero-byte file is exactly what ffmpeg leaves behind when it encodes nothing")

	real := filepath.Join(dir, "real.jpg")
	writeJPEG(t, real, 32, 32)
	require.True(t, wroteFrame(real))
}

/* ------------------------------------------------------------ first frame */

// ffmpegOrSkip keeps this file honest on a machine without ffmpeg. A test that
// passed because the binary was missing would claim to cover the one path that
// has actually been broken in production.
func ffmpegOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not in PATH; the first-frame rules are unproven on this machine")
	}
}

// makeClip renders a synthetic clip with ffmpeg itself, so the fixture is a
// real container with real frames rather than a blob checked into the repo.
func makeClip(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	full := append([]string{"-y", "-loglevel", "error"}, args...)
	full = append(full, "-pix_fmt", "yuv420p", path)
	out, err := exec.CommandContext(ctx, "ffmpeg", full...).CombinedOutput()
	require.NoError(t, err, "building the fixture failed: %s", string(out))
	return path
}

// meanLuma is how bright the produced thumbnail is, 0-255. Video black sits at
// 16, so anything at or under ~20 is a black card.
func meanLuma(t *testing.T, path string) float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	bounds := img.Bounds()
	var sum float64
	var n float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
			n++
		}
	}
	return sum / n
}

// A clip shorter than one second must still get a card.
//
// ⚠ RED on the old code, and silently so: `ffmpeg -ss 1` on a 0.4s clip
// decodes nothing and exits 0, so the pipeline marked the row ready and the
// browser got a 404 from /api/files/thumb/{id}.
func TestExtractFirstFrame_ClipShorterThanASecond(t *testing.T) {
	ffmpegOrSkip(t)
	dir := t.TempDir()
	src := makeClip(t, dir, "tiny.mp4", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=30", "-t", "0.4")
	out := filepath.Join(dir, "tiny.jpg")

	require.NoError(t, extractFirstFrame(context.Background(), src, out))
	require.True(t, wroteFrame(out), "a real video ended up with no thumbnail at all")
	require.Greater(t, meanLuma(t, out), 20.0, "the frame is black")
}

// A clip that opens on black — a fade-in, a slate, a camera leader — must show
// what the video is OF, not the fade.
//
// ⚠ RED on the old code for a different reason than the test above: seeking
// to a fixed one second lands INSIDE a 1.6s fade, and the fixture produced a
// 581-byte pure-black JPEG that the pipeline recorded as ready.
func TestExtractFirstFrame_SkipsABlackOpening(t *testing.T) {
	ffmpegOrSkip(t)
	dir := t.TempDir()
	src := makeClip(t, dir, "fade.mp4",
		"-f", "lavfi", "-i", "color=c=black:s=320x180:r=30:d=1.6",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=30:duration=3",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1[v]", "-map", "[v]")
	out := filepath.Join(dir, "fade.jpg")

	require.NoError(t, extractFirstFrame(context.Background(), src, out))
	require.True(t, wroteFrame(out))
	require.Greater(t, meanLuma(t, out), 20.0,
		"the card shows the fade-in rather than the video — a black rectangle says no more than the type tile it replaced")
}

// A video that really is black all the way through still gets a frame, and
// that frame is honestly black. The search for something brighter finds
// nothing and the literal first frame is the right answer — what must NOT
// happen is the pipeline reporting success with nothing on disk.
func TestExtractFirstFrame_AllBlackClipStillGetsAFrame(t *testing.T) {
	ffmpegOrSkip(t)
	dir := t.TempDir()
	src := makeClip(t, dir, "black.mp4", "-f", "lavfi", "-i", "color=c=black:s=320x180:r=30:d=2")
	out := filepath.Join(dir, "black.jpg")

	require.NoError(t, extractFirstFrame(context.Background(), src, out))
	require.True(t, wroteFrame(out), "the fallback pass wrote nothing")
	require.Less(t, meanLuma(t, out), 25.0, "an all-black clip should produce a black frame, not something invented")
}

// A file that is not a video at all fails, and says so. The pipeline records
// state=failed from the returned error; returning nil here is what used to
// leave a row claiming ready with no file behind it.
func TestExtractFirstFrame_NotAVideoFails(t *testing.T) {
	ffmpegOrSkip(t)
	dir := t.TempDir()
	junk := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(junk, []byte("this is not a video"), 0o600))
	out := filepath.Join(dir, "junk.jpg")

	err := extractFirstFrame(context.Background(), junk, out)
	require.Error(t, err, "a 19-byte file called .mp4 must not be reported as a successful thumbnail")
	require.False(t, wroteFrame(out))
}

/* ------------------------------------------------------------- page render */

// gsOrSkip gates the PDF rules on a machine that can rasterise one.
func gsOrSkip(t *testing.T) string {
	t.Helper()
	gs, err := exec.LookPath("gs")
	if err != nil {
		t.Skip("ghostscript not in PATH; the page-render rules are unproven on this machine")
	}
	return gs
}

// makePDF builds a real multi-page PDF with ghostscript, so the fixture is a
// document a renderer accepts rather than a hand-typed blob that only looks
// like one. Twelve pages on purpose: pdftoppm pads its output page number to
// the width of the document's page COUNT, so a two-digit document is the case
// where the old single-digit rename silently missed its own output.
func makePDF(t *testing.T, dir string, pages int) string {
	t.Helper()
	gs := gsOrSkip(t)
	ps := filepath.Join(dir, "src.ps")
	var b bytes.Buffer
	for i := 1; i <= pages; i++ {
		fmt.Fprintf(&b, "/Helvetica findfont 36 scalefont setfont 72 700 moveto (Page %d of %d) show showpage\n", i, pages)
	}
	require.NoError(t, os.WriteFile(ps, b.Bytes(), 0o600))

	pdf := filepath.Join(dir, "doc.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gs,
		"-sDEVICE=pdfwrite", "-dNOPAUSE", "-dBATCH", "-dQUIET", "-o", pdf, ps).CombinedOutput()
	require.NoError(t, err, "building the PDF fixture failed: %s", string(out))
	return pdf
}

// Page one of a real multi-page document reaches the cache, and it reaches it
// at thumbnail size.
//
// ⚠ The size assertion is the regression guard. Measured on the live backend
// on 2026-09-12, before this change: a one-page fixture produced an 816×1056,
// 358 KB JPEG — per PDF, per office document, behind a 184×108 card.
func TestRenderPDFPage1_WritesAThumbnailSizedPage(t *testing.T) {
	gsOrSkip(t)
	dir := t.TempDir()
	pdf := makePDF(t, dir, 12)
	out := filepath.Join(dir, "page.jpg")

	require.NoError(t, renderPDFPage1(context.Background(), pdf, out))
	require.True(t, wroteFrame(out), "the renderer reported success and wrote nothing")

	w, h := decodeSize(t, out)
	require.LessOrEqual(t, w, thumbMaxWidth, "a full-resolution page reached the cache")
	require.LessOrEqual(t, h, thumbMaxHeight)
	// Portrait in, portrait out.
	require.Greater(t, h, w, "a portrait page came back landscape")

	st, err := os.Stat(out)
	require.NoError(t, err)
	require.Less(t, st.Size(), int64(120*1024), "a card-sized page should not cost six figures of bytes")

	// Nothing left behind: pdftoppm writes its own name and the renderer has
	// to clean up after itself or the cache fills with orphans.
	strays, _ := filepath.Glob(filepath.Join(dir, "page-*.jpg"))
	require.Empty(t, strays, "renderer left its intermediate files in the cache directory")
}

// A file that is not a PDF fails loudly rather than leaving a ready row with
// no picture behind it.
func TestRenderPDFPage1_NotAPDFFails(t *testing.T) {
	gsOrSkip(t)
	dir := t.TempDir()
	junk := filepath.Join(dir, "not.pdf")
	require.NoError(t, os.WriteFile(junk, []byte("%PDF-1.4 and then nothing at all"), 0o600))
	out := filepath.Join(dir, "out.jpg")

	require.Error(t, renderPDFPage1(context.Background(), junk, out))
	require.False(t, wroteFrame(out))
}

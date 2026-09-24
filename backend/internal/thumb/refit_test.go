package thumb_test

// Thumbnails cached at full page size by a version before 0.41.0.
//
// Until then the PDF and office generators wrote the rasterised first page at
// `gs -r96` size straight into the cache — 794×1123 for A4, 100–400 KB — and
// fitToThumb only changed what NEW renders produce. An upgraded install kept
// serving every older page at full size: on one install that was 14,706 of
// 43,467 ready thumbnails (1.68 GB of a 2.2 GB cache), and a folder of a few
// hundred office documents had the explorer fetch and decode ~140 of them at
// ~3.6 MB each until a browser tab on a smaller machine ran out of memory.
//
// RED PROOF (unfixed code): the 794×1123 page below stayed 794×1123.

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// jpegAt writes a w×h JPEG into dir and, unless fresh, backdates it past the
// grace window so the test measures the decision and not the clock.
func jpegAt(t *testing.T, dir, name string, w, h int, fresh bool) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}))
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o644))
	if !fresh {
		old := time.Now().Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(p, old, old))
	}
	return p
}

func dimsOf(t *testing.T, p string) [2]int {
	t.Helper()
	f, err := os.Open(p)
	require.NoError(t, err)
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	require.NoError(t, err)
	return [2]int{cfg.Width, cfg.Height}
}

func snapshot(t *testing.T, paths ...string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		out[p] = b
	}
	return out
}

func requireUnchanged(t *testing.T, before map[string][]byte) {
	t.Helper()
	for p, b := range before {
		now, err := os.ReadFile(p)
		require.NoError(t, err)
		require.Equal(t, b, now, "%s must be left as it was", filepath.Base(p))
	}
}

// The headline: a page cached at full size comes out at the size a new render
// gets from fitToThumb.
func TestRefitOversized_ShrinksAPageCachedAtFullSize(t *testing.T) {
	_, p, dir, _ := reapFixture(t)
	page := jpegAt(t, dir, "101.jpg", 794, 1123, false)
	before, err := os.Stat(page)
	require.NoError(t, err)

	res, err := p.RefitOversized(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res.Refitted)
	require.Equal(t, [2]int{226, 320}, dimsOf(t, page), "A4 fits the 320×320 box")

	after, err := os.Stat(page)
	require.NoError(t, err)
	require.Less(t, after.Size(), before.Size())
	require.Equal(t, before.Size()-after.Size(), res.Freed)
	require.Equal(t, before.Mode().Perm(), after.Mode().Perm(), "the rewrite keeps the file's permissions")
}

// What today's generators write stays byte for byte: every one of them bounds
// the WIDTH at 320, and a portrait video frame is 320 wide and taller than that.
func TestRefitOversized_LeavesWhatTheCurrentGeneratorsWrite(t *testing.T) {
	_, p, dir, _ := reapFixture(t)
	before := snapshot(t,
		jpegAt(t, dir, "201.jpg", 320, 569, false), // portrait video, scale=320:-1
		jpegAt(t, dir, "202.jpg", 226, 320, false), // PDF/office page after fitToThumb
		jpegAt(t, dir, "203.jpg", 320, 200, false), // generic card
		jpegAt(t, dir, "204.jpg", 320, 180, false), // landscape video
	)

	res, err := p.RefitOversized(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, res.Refitted)
	require.Equal(t, 4, res.Scanned)
	requireUnchanged(t, before)
}

// Nothing it cannot judge is touched: a file written moments ago (a render may
// still be finishing), a name that is not `<id>.jpg`, bytes that are not a JPEG.
func TestRefitOversized_LeavesWhatItCannotJudge(t *testing.T) {
	_, p, dir, _ := reapFixture(t)
	garbage := filepath.Join(dir, "303.jpg")
	require.NoError(t, os.WriteFile(garbage, []byte("not a jpeg at all"), 0o644))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(garbage, old, old))
	before := snapshot(t,
		jpegAt(t, dir, "301.jpg", 794, 1123, true),
		jpegAt(t, dir, "notes.jpg", 794, 1123, false),
		jpegAt(t, dir, "302.jpg.bak", 794, 1123, false),
		garbage,
	)

	res, err := p.RefitOversized(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, res.Refitted)
	require.Equal(t, 0, res.Failed)
	requireUnchanged(t, before)
}

// A header that claims far more pixels than any page render is not decoded:
// the pass runs at every boot, and the decoder allocates for every pixel a
// header claims before it finds out the rest of the file is not there.
//
// RED PROOF (before the bound): the file below was decoded — Failed=1 after
// allocating for 144 million pixels — instead of being skipped unread.
func TestRefitOversized_DoesNotDecodeAHeaderClaimingAGiantImage(t *testing.T) {
	_, p, dir, _ := reapFixture(t)
	path := jpegAt(t, dir, "401.jpg", 16, 16, false)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	sof := bytes.Index(b, []byte{0xFF, 0xC0})
	require.Positive(t, sof, "the encoder writes a baseline SOF0")
	// SOF0: marker, length (2), precision (1), height (2), width (2).
	b[sof+5], b[sof+6] = 0x2E, 0xE0 // 12000
	b[sof+7], b[sof+8] = 0x2E, 0xE0 // 12000
	require.NoError(t, os.WriteFile(path, b, 0o644))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	require.Equal(t, [2]int{12000, 12000}, dimsOf(t, path))
	before := snapshot(t, path)

	res, err := p.RefitOversized(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res.Skipped)
	require.Equal(t, 0, res.Failed, "skipped unread, not attempted")
	require.Equal(t, 0, res.Refitted)
	requireUnchanged(t, before)
}

// Wired into the reaper: the pass runs once at boot, after the orphan sweep,
// so an upgraded install serves small thumbnails without anyone asking.
func TestRunReaper_RefitsLegacyPagesAtBoot(t *testing.T) {
	store, p, dir, sid := reapFixture(t)
	n := newNode(t, store, sid, "rapor.pdf")
	page := jpegAt(t, dir, strconv.FormatInt(n.ID, 10)+".jpg", 794, 1123, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.RunReaper(ctx, time.Hour)

	require.Eventually(t, func() bool {
		f, err := os.Open(page)
		if err != nil {
			return false
		}
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		return err == nil && cfg.Width == 226 && cfg.Height == 320
	}, 10*time.Second, 20*time.Millisecond, "the boot pass re-fits a page cached at full size")
}

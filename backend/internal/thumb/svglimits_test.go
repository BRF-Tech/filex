package thumb

// The SVG limits (GitHub #79 follow-up): a file over the size limit is
// skipped without the engine ever running and without its bytes being read
// past the limit; a file that takes longer than the time limit is cut off,
// skipped, and the next file is drawn as usual; raising a limit draws them.

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

type limitsFixture struct {
	store   db.Store
	p       *Pipeline
	st      *model.Storage
	root    string
	renders atomic.Int64
}

func newLimitsFixture(t *testing.T, maxMB, timeoutS int) *limitsFixture {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertSetting(ctx, SVGMaxMBSetting.Key, itoa(maxMB)))
	require.NoError(t, store.UpsertSetting(ctx, SVGTimeoutSetting.Key, itoa(timeoutS)))
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "yerel", Driver: "local", MountPath: "/yerel", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
	require.NoError(t, err)
	f := &limitsFixture{store: store, st: st, root: root}
	f.p = New(store, t.TempDir(), Capabilities{Image: true, SVG: true})
	f.p.AttachStorage(st.ID, drv)
	f.p.AttachSettings(store)
	real := f.p.svgRender
	f.p.svgRender = func(ctx context.Context, svg []byte, w, h int) (image.Image, error) {
		f.renders.Add(1)
		return real(ctx, svg, w, h)
	}
	return f
}

func itoa(n int) string { return strconv.Itoa(n) }

func (f *limitsFixture) file(t *testing.T, name string, body []byte, size int64) *model.Node {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.root, name), body, 0o644))
	mt := time.Now().UTC().Truncate(time.Millisecond)
	n, err := f.store.CreateNode(context.Background(), &model.Node{StorageID: f.st.ID, Name: name, Path: "/" + name,
		PathHash: pathkey.Hash(f.st.ID, "/"+name), Type: model.NodeTypeFile, Size: size, BackendMtime: &mt})
	require.NoError(t, err)
	return n
}

func (f *limitsFixture) row(t *testing.T, id int64) *model.Thumbnail {
	t.Helper()
	r, err := f.store.GetThumbnail(context.Background(), id)
	require.NoError(t, err)
	return r
}

// bigSVG is a valid SVG of at least n bytes (padded with a comment).
func bigSVG(n int) []byte {
	head := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="#0a0"/><!--`
	tail := `--></svg>`
	pad := n - len(head) - len(tail)
	if pad < 0 {
		pad = 0
	}
	return []byte(head + strings.Repeat("x", pad) + tail)
}

func TestSVGLimits_TheDefaults(t *testing.T) {
	d := DefaultSVGLimits()
	require.Equal(t, int64(5<<20), d.MaxBytes)
	require.Equal(t, 10*time.Second, d.Timeout)
}

// Over the limit by the catalogue's size: skipped before the engine runs and
// before a byte is read. The file is removed from disk to prove the second:
// a read would fail and record `failed`, not `skipped`.
func TestSVGLimits_TooLargeIsSkippedWithoutTheEngine(t *testing.T) {
	f := newLimitsFixture(t, 1, 10)
	body := bigSVG(1<<20 + 10)
	n := f.file(t, "harita.svg", body, int64(len(body)))
	require.NoError(t, os.Remove(filepath.Join(f.root, "harita.svg")))

	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	require.Zero(t, f.renders.Load(), "the engine ran for a file over the limit")
	r := f.row(t, n.ID)
	require.Equal(t, "skipped", r.State)
	code, lim := ParseSkip(r.Error)
	require.Equal(t, SkipSVGTooLarge, code)
	require.Equal(t, int64(1<<20), lim)
}

// The catalogue's size was behind the file: the limit is held on the bytes.
func TestSVGLimits_TooLargeCaughtOnTheBytes(t *testing.T) {
	f := newLimitsFixture(t, 1, 10)
	n := f.file(t, "gec.svg", bigSVG(1<<20+10), 100)
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	require.Zero(t, f.renders.Load())
	code, _ := ParseSkip(f.row(t, n.ID).Error)
	require.Equal(t, SkipSVGTooLarge, code)
}

// A render past the time limit is cut off and skipped with the limit; the
// next file is drawn as usual by the same pipeline.
//
// ⚠ The render's own wait is what is timed, not GenerateThumb. Compiling the
// engine (svgwasm.Prepare, about two seconds) and waiting for a free slot are
// kept off the limit's clock on purpose (svg.go). Timing the whole call
// counted them: under -race in the 0.50 final run it took 10.8 s and failed
// "< 5 s", while the render itself is cut at 1 s.
func TestSVGLimits_TimeLimitCutsItOff(t *testing.T) {
	f := newLimitsFixture(t, 5, 1)
	real := f.p.svgRender
	var waited atomic.Int64
	f.p.svgRender = func(ctx context.Context, svg []byte, w, h int) (image.Image, error) {
		if strings.Contains(string(svg), "YAVAS") {
			began := time.Now()
			<-ctx.Done()
			waited.Store(int64(time.Since(began)))
			return nil, ctx.Err()
		}
		return real(ctx, svg, w, h)
	}
	heavy := f.file(t, "agir.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><!--YAVAS--></svg>`), 90)
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), heavy), ErrSkipped)
	cut := time.Duration(waited.Load())
	require.GreaterOrEqual(t, cut, 900*time.Millisecond, "cut by the limit, not by something sooner")
	require.Less(t, cut, 5*time.Second, "the time limit is 1 s")
	code, lim := ParseSkip(f.row(t, heavy.ID).Error)
	require.Equal(t, SkipSVGTimeout, code)
	require.Equal(t, int64(1000), lim)

	light := f.file(t, "hafif.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`), 90)
	require.NoError(t, f.p.GenerateThumb(context.Background(), light))
	require.Equal(t, "ready", f.row(t, light.ID).State)
}

// The real engine under a real limit: a machine-made SVG of many paths is
// stopped at the limit, and the engine draws the next file.
func TestSVGLimits_TheRealEngineStopsAtTheLimit(t *testing.T) {
	f := newLimitsFixture(t, 5, 1)
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000">`)
	for i := 0; sb.Len() < 4<<20; i++ {
		sb.WriteString(`<path d="M` + itoa(i) + ` ` + itoa(i+3) + ` l50 30 l-20 40 z" fill-opacity="0.3"/>`)
	}
	sb.WriteString(`</svg>`)
	body := []byte(sb.String())
	n := f.file(t, "makine.svg", body, int64(len(body)))
	start := time.Now()
	err := f.p.GenerateThumb(context.Background(), n)
	if err == nil {
		t.Skip("this machine drew the file inside 1 s; nothing to cut off")
	}
	require.ErrorIs(t, err, ErrSkipped)
	require.Less(t, time.Since(start), 10*time.Second)
	code, _ := ParseSkip(f.row(t, n.ID).Error)
	require.Equal(t, SkipSVGTimeout, code)
	ok := f.file(t, "sonra.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`), 90)
	require.NoError(t, f.p.GenerateThumb(context.Background(), ok))
}

// Raising a limit draws what the old one skipped: the listing asks
// (Assess), and the repair tool's Fix asks too (Wanted).
func TestSVGLimits_RaisingALimitDrawsItAgain(t *testing.T) {
	f := newLimitsFixture(t, 1, 10)
	body := bigSVG(1<<20 + 10)
	n := f.file(t, "harita.svg", body, int64(len(body)))
	require.ErrorIs(t, f.p.GenerateThumb(context.Background(), n), ErrSkipped)
	r := f.row(t, n.ID)
	later := time.Now().Add(time.Hour)
	require.Equal(t, Leave, f.p.Assess(n, r, later), "same limit: not asked again")

	require.NoError(t, f.store.UpsertSetting(context.Background(), SVGMaxMBSetting.Key, "2"))
	f.p.ForgetSettings()
	require.Equal(t, Render, f.p.Assess(n, r, later), "the limit was raised past the file")
	require.True(t, f.p.Wanted(n, r, Selection{Stale: true}))

	timedOut := &model.Thumbnail{State: "skipped", Error: skipReason(SkipSVGTimeout, 10000), SourceSig: n.ContentFingerprint()}
	require.Equal(t, Leave, f.p.Assess(n, timedOut, later))
	require.NoError(t, f.store.UpsertSetting(context.Background(), SVGTimeoutSetting.Key, "20"))
	f.p.ForgetSettings()
	require.Equal(t, Render, f.p.Assess(n, timedOut, later))
}

// The folder switch is read through the same cache as the limits: on by
// default, off once the setting says so and the cache is dropped.
func TestFolderPreviews_TheSwitch(t *testing.T) {
	f := newLimitsFixture(t, 5, 10)
	require.True(t, f.p.FolderPreviews(), "on by default")
	require.NoError(t, f.store.UpsertSetting(context.Background(), FolderPreviewsSetting.Key, "false"))
	require.True(t, f.p.FolderPreviews(), "cached for a few seconds")
	f.p.ForgetSettings()
	require.False(t, f.p.FolderPreviews())
	require.True(t, (*Pipeline)(nil).FolderPreviews(), "no pipeline: the default")
}

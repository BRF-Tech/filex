package thumb_test

// What a render records, and what a re-render of a changed file does to the
// picture that is being served meanwhile (docs/thumbnails.md, Design notes).

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// localPipeline is a pipeline over a real local storage in a temp dir.
func localPipeline(t *testing.T, caps thumb.Capabilities) (db.Store, *thumb.Pipeline, *model.Storage, string) {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "yerel", Driver: "local", MountPath: "/yerel", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`),
	})
	require.NoError(t, err)
	pipe := thumb.New(store, t.TempDir(), caps)
	pipe.AttachStorage(st.ID, drv)
	return store, pipe, st, root
}

func writeFile(t *testing.T, store db.Store, st *model.Storage, root, name string, body []byte) *model.Node {
	t.Helper()
	return writeFileAs(t, store, st, root, name, "", body)
}

// writeFileAs is writeFile with the catalogue type mime (writeFile leaves it
// empty, which the pipeline reads as "the extension's").
func writeFileAs(t *testing.T, store db.Store, st *model.Storage, root, name, mime string, body []byte) *model.Node {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, name), body, 0o644))
	mt := time.Now().UTC().Truncate(time.Millisecond)
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: name, Path: "/" + name, Mime: mime,
		PathHash: pathkey.Hash(st.ID, "/"+name), Type: model.NodeTypeFile, Size: int64(len(body)),
		BackendMtime: &mt,
	})
	require.NoError(t, err)
	return n
}

func solidPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func cachedPixel(t *testing.T, pipe *thumb.Pipeline, id int64) color.RGBA {
	t.Helper()
	b, err := os.ReadFile(pipe.CachePath(id))
	require.NoError(t, err)
	img, err := jpeg.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	r, g, bl, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 0xff}
}

func cachedCorner(t *testing.T, pipe *thumb.Pipeline, id int64) color.RGBA {
	t.Helper()
	return cachedAt(t, pipe, id, 1, 1)
}

func cachedAt(t *testing.T, pipe *thumb.Pipeline, id int64, x, y int) color.RGBA {
	t.Helper()
	b, err := os.ReadFile(pipe.CachePath(id))
	require.NoError(t, err)
	img, err := jpeg.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	r, g, bl, _ := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), 0xff}
}

// near: c is within 12 of want on every channel (JPEG moves a flat colour a
// little).
func near(c, want color.RGBA) bool {
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return d(c.R, want.R) <= 12 && d(c.G, want.G) <= 12 && d(c.B, want.B) <= 12
}

// requireChecker: the pixels at (x, y) and one square to the right are the
// checkerboard's two greys, light first.
func requireChecker(t *testing.T, pipe *thumb.Pipeline, id int64, x, y int) {
	t.Helper()
	first, next := cachedAt(t, pipe, id, x, y), cachedAt(t, pipe, id, x+thumb.CheckerSize, y)
	require.True(t, near(first, thumb.CheckerLight), "(%d,%d) is %v, not the light square", x, y, first)
	require.True(t, near(next, thumb.CheckerDark), "(%d,%d) is %v, not the dark square", x+thumb.CheckerSize, y, next)
}

// Every render records the content it was drawn from and when it started.
func TestRender_RecordsTheSourceSignature(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "kirmizi.png", solidPNG(t, 40, 40, color.RGBA{220, 20, 20, 255}))

	require.NoError(t, pipe.GenerateThumb(context.Background(), n))
	row, err := store.GetThumbnail(context.Background(), n.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", row.State)
	require.Equal(t, n.ContentFingerprint(), row.SourceSig)
	require.NotNil(t, row.AttemptedAt)
	require.Equal(t, thumb.Leave, pipe.Assess(n, row, time.Now().Add(time.Hour)), "a fresh render is not stale")
}

// A failure records the signature too: the same content is not asked for
// again on every listing, a different one is.
func TestRender_AFailureRecordsTheSignature(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "bozuk.png", []byte("not a png at all"))

	require.Error(t, pipe.GenerateThumb(context.Background(), n))
	row, err := store.GetThumbnail(context.Background(), n.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", row.State)
	require.Equal(t, n.ContentFingerprint(), row.SourceSig)
	later := time.Now().Add(time.Hour)
	require.Equal(t, thumb.Leave, pipe.Assess(n, row, later))
	n.Size++
	require.Equal(t, thumb.Render, pipe.Assess(n, row, later))
}

// The file changes on disk (outside filex); the catalogue learns it; the
// re-render replaces the picture — and the old picture stays servable until
// then (0.49 wrote `pending` first, which blanked the card).
func TestRender_AChangedFileGetsANewPicture(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "foto.png", solidPNG(t, 40, 40, color.RGBA{220, 20, 20, 255}))
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	require.Greater(t, int(cachedPixel(t, pipe, n.ID).R), 150, "first render is red")

	// Somebody replaces the file behind filex's back; the sync records it.
	blue := solidPNG(t, 50, 40, color.RGBA{20, 20, 220, 255})
	require.NoError(t, os.WriteFile(filepath.Join(root, "foto.png"), blue, 0o644))
	mt := time.Now().Add(2 * time.Second).UTC().Truncate(time.Millisecond)
	require.NoError(t, store.UpdateNodeMeta(ctx, n.ID, int64(len(blue)), "", "", mt))
	changed, err := store.GetNode(ctx, n.ID)
	require.NoError(t, err)

	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, thumb.Render, pipe.Assess(changed, row, time.Now().Add(time.Hour)), "the change is seen")

	require.NoError(t, pipe.GenerateThumb(ctx, changed))
	px := cachedPixel(t, pipe, n.ID)
	require.Greater(t, int(px.B), 150, "the picture is the new content: %v", px)
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.Equal(t, changed.ContentFingerprint(), row.SourceSig)
}

// While a changed file is being drawn again, its row stays `ready` with its
// key: the listing keeps handing out the old picture instead of an icon.
func TestRender_ARefreshKeepsServingTheOldPicture(t *testing.T) {
	ctx := context.Background()
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "foto.png", solidPNG(t, 40, 40, color.RGBA{220, 20, 20, 255}))
	require.NoError(t, pipe.GenerateThumb(ctx, n))

	// A refresh that fails half way: the bytes are gone. What matters is the
	// row between "started" and "ended", which the store records as it goes.
	seen := make(chan *model.Thumbnail, 8)
	spy := &rowSpy{Store: store, seen: seen}
	pipe2 := thumb.New(spy, pipe.CacheDir(), thumb.Capabilities{Image: true})
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	pipe2.AttachStorage(st.ID, drv)
	require.NoError(t, pipe2.GenerateThumb(ctx, n))
	close(seen)
	for r := range seen {
		require.NotEqual(t, "pending", r.State, "a ready row was blanked while it was drawn again")
	}
}

type rowSpy struct {
	db.Store
	seen chan *model.Thumbnail
}

func (s *rowSpy) UpsertThumbnail(ctx context.Context, t *model.Thumbnail) error {
	cp := *t
	s.seen <- &cp
	return s.Store.UpsertThumbnail(ctx, t)
}

// A transparent picture is drawn over a grey checkerboard, the pattern an
// image editor shows for "no background": a JPEG has no alpha, and the
// encoder alone turns every transparent pixel black, a logo on a black slab
// (0.49). An opaque picture covers the pattern.
func TestRender_TransparencyShowsAChecker(t *testing.T) {
	store, pipe, st, root := localPipeline(t, thumb.Capabilities{Image: true})
	n := writeFile(t, store, st, root, "seffaf.png", solidPNG(t, 40, 40, color.NRGBA{0, 0, 0, 0}))
	require.NoError(t, pipe.GenerateThumb(context.Background(), n))
	requireChecker(t, pipe, n.ID, 2, 2)
	requireChecker(t, pipe, n.ID, 2, 2+2*thumb.CheckerSize)
	// One row of squares down, the colours swap.
	require.True(t, near(cachedAt(t, pipe, n.ID, 2, 2+thumb.CheckerSize), thumb.CheckerDark))

	opaque := writeFile(t, store, st, root, "kirmizi.png", solidPNG(t, 40, 40, color.RGBA{220, 20, 20, 255}))
	require.NoError(t, pipe.GenerateThumb(context.Background(), opaque))
	for _, x := range []int{2, 2 + thumb.CheckerSize} {
		px := cachedAt(t, pipe, opaque.ID, x, 2)
		require.Greater(t, int(px.R), 180, "an opaque picture shows no checker: %v", px)
	}
}

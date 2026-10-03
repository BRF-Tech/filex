package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// The one thumbnail walk (BackfillThumbs), as the repair tool and the CLI
// use it: a path narrows it to one file or one folder with everything in it,
// the selection decides what is drawn, and counting walks the same way.

type walkFixture struct {
	s     *Server
	store db.Store
	st    *model.Storage
	root  string
	nodes map[string]*model.Node
}

func pngOf(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return b.Bytes()
}

// newWalkFixture builds a storage with /a.png, /Tatil/b.png, /Tatil/2024/c.png
// and a file under .versions that nobody can see.
func newWalkFixture(t *testing.T) *walkFixture {
	t.Helper()
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	synced := time.Now().Add(-time.Hour)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "arsiv", Driver: "local", MountPath: "/arsiv", Enabled: true, LastSyncAt: &synced,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`),
	})
	require.NoError(t, err)
	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true, SVG: true})
	pipe.AttachStorage(st.ID, drv)
	f := &walkFixture{
		s:     &Server{store: store, pipeline: pipe, resolver: func(int64) (storage.Driver, error) { return drv, nil }},
		store: store, st: st, root: root, nodes: map[string]*model.Node{},
	}
	dir := func(p string, parent *int64) *int64 {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(p)), 0o755))
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, ParentID: parent, Name: filepath.Base(p),
			Path: p, PathHash: pathkey.Hash(st.ID, p), Type: model.NodeTypeDirectory})
		require.NoError(t, err)
		return &n.ID
	}
	file := func(p string, parent *int64) {
		body := pngOf(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), body, 0o644))
		mt := time.Now().UTC().Truncate(time.Millisecond)
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, ParentID: parent, Name: filepath.Base(p),
			Path: p, PathHash: pathkey.Hash(st.ID, p), Type: model.NodeTypeFile, Size: int64(len(body)), BackendMtime: &mt})
		require.NoError(t, err)
		f.nodes[p] = n
	}
	file("/a.png", nil)
	tatil := dir("/Tatil", nil)
	file("/Tatil/b.png", tatil)
	y := dir("/Tatil/2024", tatil)
	file("/Tatil/2024/c.png", y)
	v := dir("/.versions", nil)
	file("/.versions/a.png.1", v)
	return f
}

func (f *walkFixture) state(t *testing.T, p string) string {
	t.Helper()
	row, err := f.store.GetThumbnail(context.Background(), f.nodes[p].ID)
	if err != nil || row == nil {
		return ""
	}
	return row.State
}

func TestBackfill_AFolderAndEverythingInIt(t *testing.T) {
	f := newWalkFixture(t)
	ctx := context.Background()
	opts := BackfillOptions{StorageIDs: []int64{f.st.ID}, Path: "/Tatil", Stale: true}

	n, err := f.s.CountBackfill(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, 2, n, "b.png and 2024/c.png")

	st, err := f.s.BackfillThumbs(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, 2, st.Processed)
	require.Equal(t, 2, st.OK)
	require.Equal(t, "ready", f.state(t, "/Tatil/b.png"))
	require.Equal(t, "ready", f.state(t, "/Tatil/2024/c.png"))
	require.Equal(t, "", f.state(t, "/a.png"), "outside the folder: untouched")
	require.Equal(t, "", f.state(t, "/.versions/a.png.1"), "filex's own folders: never")
}

func TestBackfill_OneFile(t *testing.T) {
	f := newWalkFixture(t)
	st, err := f.s.BackfillThumbs(context.Background(), BackfillOptions{StorageIDs: []int64{f.st.ID}, Path: "/a.png"})
	require.NoError(t, err)
	require.Equal(t, 1, st.Processed)
	require.Equal(t, "ready", f.state(t, "/a.png"))
	require.Equal(t, "", f.state(t, "/Tatil/b.png"))
}

func TestBackfill_APathThatIsNotThere(t *testing.T) {
	f := newWalkFixture(t)
	_, err := f.s.BackfillThumbs(context.Background(), BackfillOptions{StorageIDs: []int64{f.st.ID}, Path: "/Yok"})
	require.ErrorIs(t, err, ErrPathNotFound)
	_, err = f.s.BackfillThumbs(context.Background(), BackfillOptions{StorageIDs: []int64{f.st.ID}, Path: "/.versions"})
	require.ErrorIs(t, err, ErrPathNotFound, "filex's own folders are not a scope")
	_, err = f.s.BackfillThumbs(context.Background(), BackfillOptions{Path: "/Tatil"})
	require.ErrorIs(t, err, ErrPathNeedsOneStorage)
}

// Fix draws what is wrong and leaves what is right; Rebuild draws all.
func TestBackfill_FixAndRebuild(t *testing.T) {
	f := newWalkFixture(t)
	ctx := context.Background()
	all := BackfillOptions{StorageIDs: []int64{f.st.ID}}
	_, err := f.s.BackfillThumbs(ctx, all)
	require.NoError(t, err)

	// The file changed on disk and the catalogue knows it: stale.
	require.NoError(t, f.store.UpdateNodeMeta(ctx, f.nodes["/a.png"].ID, 999, "", "", time.Now().Add(time.Minute)))
	fix, _ := optionsFor(ops.ThumbRepairJob{StorageID: f.st.ID, Mode: ops.ThumbRepairFix})
	n, err := f.s.CountBackfill(ctx, fix)
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the changed file")

	rebuild, _ := optionsFor(ops.ThumbRepairJob{StorageID: f.st.ID, Mode: ops.ThumbRepairRebuild})
	n, err = f.s.CountBackfill(ctx, rebuild)
	require.NoError(t, err)
	require.Equal(t, 3, n, "every file anybody can see")
}

// A job reaches only what its asker reaches: a storage outside a tenant's
// reach is not walked, and a tenant with no storage walks none.
func TestRepairOptions_StayInsideTheReach(t *testing.T) {
	_, ok := optionsFor(ops.ThumbRepairJob{StorageID: 5, Reach: []int64{1, 2}, Mode: ops.ThumbRepairFix})
	require.False(t, ok)
	_, ok = optionsFor(ops.ThumbRepairJob{Reach: []int64{}, Mode: ops.ThumbRepairFix})
	require.False(t, ok)
	o, ok := optionsFor(ops.ThumbRepairJob{Reach: []int64{1, 2}, Mode: ops.ThumbRepairFix})
	require.True(t, ok)
	require.Equal(t, []int64{1, 2}, o.StorageIDs)
	o, ok = optionsFor(ops.ThumbRepairJob{Mode: ops.ThumbRepairFix})
	require.True(t, ok)
	require.Nil(t, o.StorageIDs, "unscoped: every enabled storage")
}

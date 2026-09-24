package sync_test

// A full scan is the only way to make the catalogue look at a storage again,
// and on a large one it is expensive: 169k rows took 23 minutes and rewrote
// seen_at on every one of them, to learn about one folder. Worker.RescanFolder
// rescans a single catalogued folder's subtree with the same walk, the same
// settle and the same tombstone rules — bounded to that subtree, compared
// against that subtree's own size, and without touching the storage's sync
// history or its last-synced time.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func syncRunCount(t *testing.T, store db.Store, storageID int64) int {
	t.Helper()
	runs, err := store.ListSyncRuns(context.Background(), storageID, 500)
	require.NoError(t, err)
	return len(runs)
}

// newWorkerFor registers the storage on a fresh worker, as the server does.
func newWorkerFor(t *testing.T, store db.Store, st *model.Storage) *filexsync.Worker {
	t.Helper()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(context.Background(), st))
	t.Cleanup(w.Stop)
	return w
}

func TestScopePath(t *testing.T) {
	for raw, want := range map[string]string{
		"":             "",
		"/":            "",
		"docs":         "/docs",
		"/docs/":       "/docs",
		"a/./b//c":     "/a/b/c",
		"Müşteri/2026": "/Müşteri/2026",
	} {
		got, err := filexsync.ScopePath(raw)
		require.NoError(t, err, "ScopePath(%q)", raw)
		assert.Equal(t, want, got, "ScopePath(%q)", raw)
	}
	// filex's own trees at any depth (syspath.Sealed): the walk never goes there.
	for _, raw := range []string{"..", "../etc", "docs/../../x", "docs/..", ".versions", "/.versions/42", ".filex-trash/x", ".thumbs", "docs/.versions/notes", "a\x00b"} {
		_, err := filexsync.ScopePath(raw)
		assert.ErrorIs(t, err, filexsync.ErrScopeInvalid, "ScopePath(%q) must be refused", raw)
	}
}

func TestRescanFolderRefreshesOnlyThatSubtree(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	for _, rel := range []string{"a/keep1.txt", "a/keep2.txt", "a/keep3.txt", "a/gone.txt", "a/alt/derin.txt", "b/keep.txt"} {
		writeUnder(t, root, rel, "x")
	}
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	before, err := store.GetStorage(ctx, st.ID)
	require.NoError(t, err)
	runsBefore := syncRunCount(t, store, st.ID)

	waitPastSecondBoundary()
	writeUnder(t, root, "a/yeni.txt", "new")
	writeUnder(t, root, "a/alt/yeni.txt", "new")
	writeUnder(t, root, "b/yeni.txt", "new")
	require.NoError(t, os.Remove(filepath.Join(root, "a", "gone.txt")))
	require.NoError(t, os.Remove(filepath.Join(root, "b", "keep.txt")))

	res, err := w.RescanFolder(ctx, st.ID, "/a")
	require.NoError(t, err)
	assert.Equal(t, "/a", res.Path)
	assert.Equal(t, 2, res.Added, "the new file and the new file one level down")
	assert.Equal(t, 1, res.Removed, "the file deleted inside the folder")
	assert.Equal(t, 7, res.Scanned, "keep1-3, yeni, alt, alt/derin, alt/yeni")

	assert.NotNil(t, node(t, store, st.ID, "/a/yeni.txt"))
	assert.NotNil(t, node(t, store, st.ID, "/a/alt/yeni.txt"))
	assert.Nil(t, node(t, store, st.ID, "/a/gone.txt"))
	// Outside the folder nothing moved: not the new file, not the deleted one.
	assert.Nil(t, node(t, store, st.ID, "/b/yeni.txt"), "a rescan of /a must not catalogue /b")
	assert.NotNil(t, node(t, store, st.ID, "/b/keep.txt"), "a rescan of /a must not remove anything in /b")

	assert.Equal(t, runsBefore, syncRunCount(t, store, st.ID), "a folder rescan is not a sync run")
	after, err := store.GetStorage(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, before.LastSyncAt, after.LastSyncAt, "the storage's last full sync did not happen now")
}

func TestRescanFolderRefusesWhatItCannotRescan(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "docs/a.txt", "x")
	w, _ := runSync(t, store, st)

	_, err := w.RescanFolder(ctx, st.ID, "/yok")
	assert.ErrorIs(t, err, filexsync.ErrFolderNotCatalogued)
	_, err = w.RescanFolder(ctx, st.ID, "/docs/a.txt")
	assert.ErrorIs(t, err, filexsync.ErrNotAFolder)
	_, err = w.RescanFolder(ctx, st.ID, "/.versions")
	assert.ErrorIs(t, err, filexsync.ErrScopeInvalid)
	_, err = w.RescanFolder(ctx, st.ID+99, "/docs")
	assert.Error(t, err, "an unknown storage")
}

// One run at a time per storage, and a folder rescan is a run: started while a
// full scan walks the same rows, it would race that scan's seen_at.
func TestRescanFolderSharesTheOneRunLock(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	gate, entered, release := newGate(t)
	st := treeStorage(t, store, "gate-test", map[string]any{"gate": gate})
	seedRow(t, store, st, nil, "/klasor", model.NodeTypeDirectory, 0)
	w := newWorkerFor(t, store, st)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	first := make(chan error, 1)
	go func() { first <- w.Trigger(ctx, st.ID) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the full run never reached the backend")
	}
	_, err := w.RescanFolder(ctx, st.ID, "/klasor")
	require.ErrorIs(t, err, filexsync.ErrRunInProgress)
	close(release)
	require.NoError(t, <-first)
}

// The 70% guard compares the subtree with ITSELF. Against the storage's last
// full count any folder is "a massive loss"; against nothing, half a folder
// missing from one listing would go to the trash.
func TestRescanFolderGuardsItsSubtreeByItsOwnCount(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	for i := 0; i < 10; i++ {
		writeUnder(t, root, "arsiv/f"+string(rune('a'+i))+".txt", "x")
	}
	w, _ := runSync(t, store, st)
	for i := 0; i < 5; i++ {
		require.NoError(t, os.Remove(filepath.Join(root, "arsiv", "f"+string(rune('a'+i))+".txt")))
	}
	waitPastSecondBoundary()
	res, err := w.RescanFolder(ctx, st.ID, "/arsiv")
	require.NoError(t, err)
	assert.Equal(t, 0, res.Removed, "5 of 10 seen is under 70%: nothing may be removed")
	assert.NotEmpty(t, res.RemovalSkipped)
	assert.Empty(t, trashedPaths(t, store, st.ID))
}

// The stale query is bounded by the folder's exact prefix: a non-ASCII name,
// and a sibling whose name only STARTS like it, are what a sloppy bound gets
// wrong.
func TestRescanFolderRemovesOnlyInsideAFolderWithANonASCIIName(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	for _, rel := range []string{"Müşteri/a.txt", "Müşteri/b.txt", "Müşteri/c.txt", "Müşteri/d.txt", "Müşteri2/x.txt"} {
		writeUnder(t, root, rel, "x")
	}
	w, _ := runSync(t, store, st)
	require.NoError(t, os.Remove(filepath.Join(root, "Müşteri", "d.txt")))
	require.NoError(t, os.Remove(filepath.Join(root, "Müşteri2", "x.txt")))
	waitPastSecondBoundary()

	res, err := w.RescanFolder(ctx, st.ID, "Müşteri")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Removed)
	assert.Nil(t, node(t, store, st.ID, "/Müşteri/d.txt"))
	assert.NotNil(t, node(t, store, st.ID, "/Müşteri2/x.txt"), "a sibling that only starts with the name is outside the folder")
}

func TestRescanFolderUsesTheOnePassListingForItsSubtree(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t, "docs/a.txt", "docs/alt/b.txt", "diger/c.txt")
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})
	w, _ := runSync(t, store, st)
	b := treeBucketOf(bucket)
	b.objs["docs/alt/yeni.txt"] = 7
	b.objs["diger/yeni.txt"] = 7
	walks, lists := b.walks.Load(), b.lists.Load()

	res, err := w.RescanFolder(ctx, st.ID, "/docs")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Added)
	assert.Equal(t, walks+1, b.walks.Load(), "the subtree is listed in one pass")
	assert.Equal(t, lists, b.lists.Load(), "and never directory by directory")
	assert.NotNil(t, node(t, store, st.ID, "/docs/alt/yeni.txt"))
	assert.Nil(t, node(t, store, st.ID, "/diger/yeni.txt"))
}

func TestRescanFolderSettlesAStagedUploadInside(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "gelen/keep.txt", "x")
	w, _ := runSync(t, store, st)
	committed := time.Now().Add(-time.Hour)
	writeUnder(t, root, "gelen/teklif.pdf", "the whole uploaded file")
	n := seedUnstored(t, store, st, "/gelen/teklif.pdf", model.TransferStateStaged, 23, &committed)

	res, err := w.RescanFolder(ctx, st.ID, "/gelen")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Reconciled)
	assert.Equal(t, model.TransferStateStored, transferState(t, store, n.ID))
}

// ---------------------------------------------------------------------------
// a listing that fails part-way must not remove anything
// ---------------------------------------------------------------------------

// failListAt makes flakysub-test's List fail for exactly one directory — a
// connection reset in the middle of a walk.
var failListAt atomic.Value

type flakySubDriver struct{ *local.Driver }

func init() {
	storage.Register("flakysub-test", func() storage.Driver { return &flakySubDriver{Driver: &local.Driver{}} })
}

func (d *flakySubDriver) Name() string { return "flakysub-test" }
func (d *flakySubDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	if bad, _ := failListAt.Load().(string); bad != "" && path.Clean("/"+p) == bad {
		return nil, errors.New("connection reset by peer (simulated)")
	}
	return d.Driver.List(ctx, p)
}

// The walk carries on past a subfolder it could not list, and everything under
// that subfolder then looks unseen. Files survive the tombstone pass on their
// own Stat; a FOLDER row has nothing to Stat and would go to the trash — where
// purging it deletes the folder on the backend. A rescan whose listing was
// partial therefore removes nothing.
func TestRescanFolderRemovesNothingWhenPartOfItsListingFailed(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "kirilgan", Driver: "flakysub-test", MountPath: "/kirilgan", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	for _, rel := range []string{"a/k1.txt", "a/k2.txt", "a/k3.txt", "a/k4.txt", "a/bozuk/alt/f.txt"} {
		writeUnder(t, root, rel, "x")
	}
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	alt := node(t, store, st.ID, "/a/bozuk/alt")
	require.NotNil(t, alt)

	failListAt.Store("/a/bozuk")
	t.Cleanup(func() { failListAt.Store("") })
	waitPastSecondBoundary()
	res, err := w.RescanFolder(ctx, st.ID, "/a")
	require.NoError(t, err)
	assert.Equal(t, 0, res.Removed)
	assert.NotEmpty(t, res.RemovalSkipped, "the caller must be told why nothing was removed")

	got, err := store.GetNode(ctx, alt.ID)
	require.NoError(t, err)
	assert.Nil(t, got.DeletedAt, "a folder the walk could not look into is not a deleted folder")
	assert.Empty(t, trashedPaths(t, store, st.ID))
}

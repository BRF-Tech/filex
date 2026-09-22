package sync_test

// filex keeps three trees of its own at the root of every storage:
// `.filex-trash/` (deleted items), `.versions/<node>/<n>` (version history)
// and `.thumbs/`. They are bookkeeping, not catalogue content.
//
// The walk skipped only the trash. A full scan of an object-store storage
// therefore minted a row for `/.versions`, one per `/.versions/<node id>`
// and one per snapshot: system-owned rows no listing shows, counted in the
// storage's file totals, indexed for search. Worse, they were a trap: once
// they are unseen, the tombstone pass moves the DIRECTORY rows into the trash
// in place (a directory has no object to Stat), and purging a trashed
// directory deletes its prefix on the backend — every version of every file.
//
// These tests pin the three rules that replace it: the walk never enters the
// trees, rows an earlier scan minted there are dropped from the catalogue
// (never from the backend), and the tombstone pass never touches a row inside
// them, whatever else went wrong.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// writeUnder writes body at root/rel, creating parents.
func writeUnder(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

// seedRow writes a catalogue row exactly the way the old walk minted one:
// system-owned, storage_key = path, synced, under its parent.
func seedRow(t *testing.T, store db.Store, st *model.Storage, parent *int64, p string, typ model.NodeType, size int64) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID:  st.ID,
		ParentID:   parent,
		Name:       filepath.Base(p),
		Path:       p,
		PathHash:   pathkey.Hash(st.ID, p),
		StorageKey: p,
		Type:       typ,
		Size:       size,
		SyncState:  model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

// rowAnywhere reports whether ANY row — live or trashed — sits at p.
func rowAnywhere(t *testing.T, store db.Store, storageID int64, p string) *model.Node {
	t.Helper()
	n, err := store.GetNodeByPathIncludingDeleted(context.Background(), storageID, pathkey.Hash(storageID, p))
	if err != nil {
		return nil
	}
	return n
}

// trashedPaths lists every trashed row of the storage by path.
func trashedPaths(t *testing.T, store db.Store, storageID int64) []string {
	t.Helper()
	rows, _, err := store.ListTrashed(context.Background(), &storageID, 500, 0)
	require.NoError(t, err)
	out := []string{}
	for _, n := range rows {
		out = append(out, n.Path)
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. the walk never enters the trees
// ---------------------------------------------------------------------------

func TestSyncNeverCataloguesFilexsOwnTrees_DirectoryWalk(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, ".versions/42/1", "eski surum")
	writeUnder(t, root, ".thumbs/ab/kapak.jpg", "jpeg")
	writeUnder(t, root, ".filex-trash/1700000000-abc123__silinen.txt", "cop")
	writeUnder(t, root, "belgeler/rapor.txt", "gercek dosya")
	// Anchored at the ROOT: a user's own folder that happens to be called
	// .versions is the user's, and so is the desktop's open-with scratch.
	writeUnder(t, root, "belgeler/.versions/notlar.txt", "kullanicinin")
	writeUnder(t, root, ".filex-open/a1-teklif.docx", "acik belge")

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	assert.Equal(t, []string{
		"/.filex-open",
		"/.filex-open/a1-teklif.docx",
		"/belgeler",
		"/belgeler/.versions",
		"/belgeler/.versions/notlar.txt",
		"/belgeler/rapor.txt",
	}, livePaths(t, store, st.ID))
	for _, p := range []string{"/.versions", "/.versions/42", "/.versions/42/1", "/.thumbs", "/.thumbs/ab/kapak.jpg", "/.filex-trash"} {
		assert.Nil(t, rowAnywhere(t, store, st.ID, p), "no row may be minted for %s", p)
	}
}

func TestSyncNeverCataloguesFilexsOwnTrees_OnePassListing(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t,
		".versions/221093/1", ".versions/221093/2", ".thumbs/x.jpg",
		".filex-trash/1700000000-x__old.txt", "docs/a.txt",
	)
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.Equal(t, int32(0), treeBucketOf(bucket).lists.Load(), "fixture check: the one-pass listing was used")
	assert.Equal(t, []string{"/docs", "/docs/a.txt"}, livePaths(t, store, st.ID))
	assert.Equal(t, 2, run.SeenCount, "objects inside filex's own trees are not part of what a pass saw")
}

// ---------------------------------------------------------------------------
// 2. rows an earlier scan minted are dropped — from the catalogue only
// ---------------------------------------------------------------------------

// runSyncIndexed is runSync with a search index attached, the way the server
// wires it, so the cleanup's index half is observable.
func runSyncIndexed(t *testing.T, store db.Store, st *model.Storage, idx *search.Index) *model.SyncRun {
	t.Helper()
	ctx := context.Background()
	w := filexsync.New(store)
	w.AttachIndex(idx)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	return run
}

func TestSyncDropsRowsAnEarlierScanMintedInsideFilexsOwnTrees(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	// A real file with one real version, recorded the way versioning does.
	writeUnder(t, root, "rapor.txt", "guncel")
	runSyncIndexed(t, store, st, idx)
	real := node(t, store, st.ID, "/rapor.txt")
	require.NotNil(t, real)
	versionKey := ".versions/" + itoa(real.ID) + "/1"
	writeUnder(t, root, versionKey, "eskisurumicerigi")
	_, err = store.CreateNodeVersion(ctx, &model.NodeVersion{NodeID: real.ID, VersionN: 1, StorageKey: versionKey, Size: 16})
	require.NoError(t, err)

	// The rows the old walk minted for it: live ones...
	vRoot := seedRow(t, store, st, nil, "/.versions", model.NodeTypeDirectory, 0)
	vNode := seedRow(t, store, st, &vRoot.ID, "/.versions/"+itoa(real.ID), model.NodeTypeDirectory, 0)
	vFile := seedRow(t, store, st, &vNode.ID, "/"+versionKey, model.NodeTypeFile, 16)
	thumbs := seedRow(t, store, st, nil, "/.thumbs", model.NodeTypeDirectory, 0)
	writeUnder(t, root, ".thumbs/eskikapak.jpg", "jpeg")
	thumb := seedRow(t, store, st, &thumbs.ID, "/.thumbs/eskikapak.jpg", model.NodeTypeFile, 4)
	// ...and ones an earlier tombstone pass already moved into the trash IN
	// PLACE, after version retention had deleted their snapshot: the most
	// dangerous shape, because a purge of that folder row deletes its prefix.
	writeUnder(t, root, ".versions/99/2", "baskadosyaninsurumu")
	gone := seedRow(t, store, st, &vRoot.ID, "/.versions/99", model.NodeTypeDirectory, 0)
	goneFile := seedRow(t, store, st, &gone.ID, "/.versions/99/1", model.NodeTypeFile, 3)
	require.NoError(t, store.SoftDeleteNode(ctx, gone.ID))
	require.NoError(t, store.SoftDeleteNode(ctx, goneFile.ID))
	for _, n := range []*model.Node{vRoot, vNode, vFile, thumbs, thumb, gone, goneFile} {
		fresh, err := store.GetNode(ctx, n.ID)
		require.NoError(t, err)
		require.NoError(t, idx.IndexNode(ctx, fresh))
	}
	require.NotEmpty(t, idx.SafeSearch(ctx, "eskikapak", 10), "fixture check: the phantom row is searchable")

	waitPastSecondBoundary()
	run := runSyncIndexed(t, store, st, idx)
	require.Equal(t, "ok", run.Status, run.Error)

	for _, p := range []string{"/.versions", "/.versions/" + itoa(real.ID), "/" + versionKey,
		"/.thumbs", "/.thumbs/eskikapak.jpg", "/.versions/99", "/.versions/99/1"} {
		assert.Nil(t, rowAnywhere(t, store, st.ID, p), "the row at %s must be gone, live or trashed", p)
	}
	assert.Empty(t, trashedPaths(t, store, st.ID),
		"nothing of filex's own trees may be left in the trash, where a purge would delete its prefix")
	assert.Empty(t, idx.SafeSearch(ctx, "eskikapak", 10), "the dropped rows' search documents must go too")

	// The backend is untouched: every byte of version history is still there,
	// and the version row still points at it.
	for _, rel := range []string{versionKey, ".versions/99/2", ".thumbs/eskikapak.jpg"} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		assert.NoError(t, err, "the cleanup must never touch the backend (%s)", rel)
	}
	versions, err := store.ListNodeVersions(ctx, real.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1, "version history rows belong to the real file and must survive")
	assert.Equal(t, versionKey, versions[0].StorageKey)
	assert.NotNil(t, node(t, store, st.ID, "/rapor.txt"), "the real file is untouched")
}

// ---------------------------------------------------------------------------
// 3. the tombstone pass never trashes a row inside the trees
// ---------------------------------------------------------------------------

// blindCleanupStore makes the cleanup's query fail, so the rows it would have
// dropped are still there when the tombstone pass runs — the state a transient
// DB error leaves behind.
type blindCleanupStore struct{ db.Store }

func (s *blindCleanupStore) ListNodesUnder(context.Context, int64, string, bool) ([]*model.Node, error) {
	return nil, errors.New("simulated: the cleanup query failed")
}

func TestTombstonePassNeverTrashesARowInsideFilexsOwnTrees(t *testing.T) {
	ctx := context.Background()
	_, real := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, real)
	writeUnder(t, root, "a.txt", "a")
	writeUnder(t, root, "b.txt", "b")
	runSync(t, real, st)

	writeUnder(t, root, ".versions/7/1", "surum")
	vRoot := seedRow(t, real, st, nil, "/.versions", model.NodeTypeDirectory, 0)
	vNode := seedRow(t, real, st, &vRoot.ID, "/.versions/7", model.NodeTypeDirectory, 0)
	vFile := seedRow(t, real, st, &vNode.ID, "/.versions/7/1", model.NodeTypeFile, 5)

	waitPastSecondBoundary()
	store := &blindCleanupStore{Store: real}
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := real.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)

	for _, n := range []*model.Node{vRoot, vNode, vFile} {
		got, err := real.GetNode(ctx, n.ID)
		require.NoError(t, err)
		assert.Nil(t, got.DeletedAt, "the tombstone pass moved %s into the trash; a purge of it deletes the prefix", got.Path)
	}
	for _, p := range trashedPaths(t, real, st.ID) {
		assert.False(t, strings.HasPrefix(p, "/.versions"), "trash entry inside .versions: %s", p)
	}
}

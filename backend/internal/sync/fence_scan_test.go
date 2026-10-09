package sync_test

// sec055 follow-up: a folder moved or trashed object by object (an object
// store) fences its prefixes (rowgate.FenceCtx) instead of holding the
// storage's row gate for as long as it runs. The scan goes on everywhere
// else, leaves the fenced prefixes alone - neither the objects that have
// arrived at the destination nor the rows still at the source are judged -
// and once the fence opens, the next pass sees the folder where the change
// put it, with its own rows. The fixtures (besideStorage, childNames) are in
// rename_beside_scan_test.go.

import (
	"context"
	"os"
	"path/filepath"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/testutil/gatetest"
)

// shortGateWait makes a deferred scan say so quickly: on code that holds the
// storage's gate for the whole long change, the scan below steps back for
// this long and is deferred instead of waiting minutes.
func shortGateWait(t *testing.T) {
	t.Helper()
	was := filexsync.GateWait
	filexsync.GateWait = 300 * time.Millisecond
	t.Cleanup(func() { filexsync.GateWait = was })
}

// scanOnce runs one full pass over st and answers its record.
func scanOnce(t *testing.T, store db.Store, st *model.Storage) *model.SyncRun {
	t.Helper()
	ctx := context.Background()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	return run
}

// A folder moved object by object: half way, a.txt is at the destination and
// b.txt still at the source, and the rows have not moved. A full pass then
// runs to its end (it is not deferred), catalogues a file that landed in
// another folder meanwhile, catalogues nothing at the destination and drops
// nothing at the source. Once the move has finished and its fence opened,
// the next pass sees the folder at its new name, with its own rows.
//
// Break: hold the storage's gate for the whole move (rowgate.MoveCtx in
// protocolsync's hold) - the pass is deferred ("aborted") and baska/yeni.txt
// is not catalogued; or take the fence check out of listDir - the pass
// catalogues hedef/a.txt as a NEW row; or out of tombstoneRows - it drops
// tasinan's row.
func TestAScanGoesOnBesideAFolderMovedObjectByObject(t *testing.T) {
	shortGateWait(t)
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	d, st, root := besideStorage(t, store)
	writeUnder(t, root, "tasinan/a.txt", "a")
	writeUnder(t, root, "tasinan/b.txt", "b")
	writeUnder(t, root, "baska/x.txt", "x")
	fillers(t, root, 20)
	first := scanOnce(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	dir := mustNode(t, store, st.ID, "/tasinan")
	a := mustNode(t, store, st.ID, "/tasinan/a.txt")
	b := mustNode(t, store, st.ID, "/tasinan/b.txt")
	waitPastSecondBoundary()

	sy := protocolsync.New(store, nil, nil, "test").WithResolver(func(int64) (storage.Driver, error) {
		return gatetest.AsObjectStore(d), nil
	})
	halfWay := make(chan struct{})
	letGo := make(chan struct{})
	var once gosync.Once
	finish := func() { once.Do(func() { close(letGo) }) }
	// The move finishes and lets go of its fence however the test ends: the
	// fences are process-wide, and the next test's storage id is the same.
	t.Cleanup(finish)
	moved := make(chan error, 1)
	go func() {
		moved <- sy.Relocate(ctx, st, "tasinan", "hedef", func(context.Context) error {
			if err := os.MkdirAll(filepath.Join(root, "hedef"), 0o755); err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(root, "tasinan", "a.txt"), filepath.Join(root, "hedef", "a.txt")); err != nil {
				return err
			}
			close(halfWay)
			<-letGo
			if err := os.Rename(filepath.Join(root, "tasinan", "b.txt"), filepath.Join(root, "hedef", "b.txt")); err != nil {
				return err
			}
			return os.Remove(filepath.Join(root, "tasinan"))
		})
	}()
	select {
	case <-halfWay:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture: the move never started")
	}
	writeUnder(t, root, "baska/yeni.txt", "yeni")

	mid := scanOnce(t, store, st)
	require.Equal(t, "ok", mid.Status, "the pass was deferred by one folder's move: %s", mid.Error)
	assert.Zero(t, mid.Deleted, "the pass dropped rows while the move was half way")
	assert.NotNil(t, node(t, store, st.ID, "/baska/yeni.txt"), "the rest of the storage was not scanned beside the move")
	assert.Nil(t, node(t, store, st.ID, "/hedef"), "the move's destination was catalogued half way")
	assert.Nil(t, node(t, store, st.ID, "/hedef/a.txt"), "an object at the move's destination was catalogued as a new file")
	for _, n := range []*model.Node{dir, a, b} {
		kept, err := store.GetNode(ctx, n.ID)
		require.NoError(t, err, "%s was dropped half way", n.Path)
		require.NotNil(t, kept)
		assert.Nil(t, kept.DeletedAt, "%s was retired half way", n.Path)
		assert.Equal(t, n.Path, kept.Path, "%s moved before the move's rows did", n.Path)
	}

	finish()
	select {
	case err := <-moved:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("fixture: the move never finished")
	}
	require.True(t, rowgate.Fences(st.ID).Empty(), "the fence is still there after the move")
	waitPastSecondBoundary()

	after := scanOnce(t, store, st)
	require.Equal(t, "ok", after.Status, after.Error)
	assert.Zero(t, after.Deleted)
	assert.Nil(t, node(t, store, st.ID, "/tasinan"), "a row is left at the old name")
	assert.Equal(t, dir.ID, mustNode(t, store, st.ID, "/hedef").ID, "the moved folder is another row")
	assert.Equal(t, a.ID, mustNode(t, store, st.ID, "/hedef/a.txt").ID, "a.txt is another row")
	assert.Equal(t, b.ID, mustNode(t, store, st.ID, "/hedef/b.txt").ID, "b.txt is another row")
	assert.Equal(t, []string{"a.txt", "b.txt"}, childNames(t, store, st.ID, dir.ID))
}

// The same for the trash: a folder trashed object by object has left its
// path while its rows are still live. A full pass half way runs to its end,
// catalogues the rest of the storage and drops none of the folder's rows; once
// the delete has finished, they are in the trash, where the person can
// restore them from.
//
// Break: hold the storage's gate for the whole delete - the pass is deferred;
// or take the fence check out of listDir and tombstoneRows - the pass drops
// the folder's rows, and the trash entry with them.
func TestAScanGoesOnBesideAFolderTrashedObjectByObject(t *testing.T) {
	shortGateWait(t)
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	d, st, root := besideStorage(t, store)
	writeUnder(t, root, "silinen/a.txt", "a")
	writeUnder(t, root, "silinen/b.txt", "b")
	fillers(t, root, 20)
	first := scanOnce(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	dir := mustNode(t, store, st.ID, "/silinen")
	a := mustNode(t, store, st.ID, "/silinen/a.txt")
	waitPastSecondBoundary()

	g := gatetest.Wrap(d.Driver)
	t.Cleanup(g.Finish)
	sy := protocolsync.New(store, nil, nil, "test")
	trashed := make(chan error, 1)
	go func() {
		out, err := sy.Discard(ctx, st, g.ObjectStore(), "silinen")
		if err == nil && !out.Trashed {
			err = os.ErrNotExist
		}
		trashed <- err
	}()
	select {
	case <-g.Reached():
	case <-time.After(10 * time.Second):
		t.Fatal("fixture: the delete never moved its bytes")
	}
	writeUnder(t, root, "kalici/yeni.txt", "yeni")

	mid := scanOnce(t, store, st)
	require.Equal(t, "ok", mid.Status, "the pass was deferred by one folder's delete: %s", mid.Error)
	assert.Zero(t, mid.Deleted, "the pass dropped rows while the delete was half way")
	assert.NotNil(t, node(t, store, st.ID, "/kalici/yeni.txt"), "the rest of the storage was not scanned beside the delete")
	for _, n := range []*model.Node{dir, a} {
		kept, err := store.GetNode(ctx, n.ID)
		require.NoError(t, err, "%s was dropped half way", n.Path)
		require.NotNil(t, kept)
	}

	g.Finish()
	select {
	case err := <-trashed:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("fixture: the delete never finished")
	}
	require.True(t, rowgate.Fences(st.ID).Empty(), "the fence is still there after the delete")
	kept, err := store.GetNode(ctx, dir.ID)
	require.NoError(t, err, "the folder's row was dropped")
	require.NotNil(t, kept)
	assert.NotNil(t, kept.DeletedAt, "the folder's row is not in the trash")
	waitPastSecondBoundary()
	after := scanOnce(t, store, st)
	require.Equal(t, "ok", after.Status, after.Error)
	kept, err = store.GetNode(ctx, dir.ID)
	require.NoError(t, err, "the next pass dropped the trash entry")
	require.NotNil(t, kept)
}

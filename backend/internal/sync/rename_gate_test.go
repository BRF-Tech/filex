package sync_test

// Issue #192, the gate (internal/rowgate): a rename, a move, a delete and a
// restore hold the storage's row gate from the first byte to the last row,
// and the scan lists and judges only while no such change is half way. The
// fixtures (besideStorage, childNames) are in rename_beside_scan_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// A scan that arrives while a rename is half way - the bytes moved, the rows
// not yet - waits for the rename, then sees the storage and the catalogue
// agree.
//
// Break: take rowgate.Judge out of listDir - the scan runs at once, catalogues
// the folder at its new name as a NEW row, and the rename's own row is
// dropped as gone.
func TestAScanWaitsForARenameCaughtHalfWay(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	_, st, root := besideStorage(t, store)
	writeUnder(t, root, "klasor/icinde.txt", "icinde")
	fillers(t, root, 20)
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	dir := mustNode(t, store, st.ID, "/klasor")
	file := mustNode(t, store, st.ID, "/klasor/icinde.txt")
	waitPastSecondBoundary()

	// The rename's first step, under the gate. The cleanup lets go of the
	// gate even when the test stops half way: it is process-wide, and the
	// storage ids of the next tests' databases are the same numbers.
	release := rowgate.Move(st.ID)
	t.Cleanup(release)
	require.NoError(t, os.Rename(filepath.Join(root, "klasor"), filepath.Join(root, "yeni")))

	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	scanned := make(chan error, 1)
	go func() { scanned <- w.Trigger(ctx, st.ID) }()
	select {
	case err := <-scanned:
		t.Fatalf("the scan ran while a rename was half way (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	// The rename's second step, then the gate opens.
	require.True(t, protocolsync.New(store, nil, nil, "test").MoveRows(ctx, st, "klasor", "yeni"))
	release()
	select {
	case err := <-scanned:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the scan never ran once the rename finished")
	}

	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.Zero(t, run.Deleted)
	assert.Nil(t, node(t, store, st.ID, "/klasor"), "a row is left at the old name")
	now := mustNode(t, store, st.ID, "/yeni")
	assert.Equal(t, dir.ID, now.ID, "the renamed folder is another row")
	assert.Equal(t, file.ID, mustNode(t, store, st.ID, "/yeni/icinde.txt").ID, "its file is another row")
	assert.Equal(t, []string{"icinde.txt"}, childNames(t, store, st.ID, dir.ID))
}

// The tombstone pass judges under the gate too. A delete that starts after
// the walk - its bytes in the trash, its row not retagged yet - is waited for:
// judged in between, the row names a path its bytes have just left, is
// confirmed gone and dropped, and the trash entry it was about to become is
// lost with it.
//
// Break: take rowgate.Judge out of tombstone - the pass judges while the
// delete is half way, drops the row and finishes before the delete does.
func TestTheTombstonePassWaitsForADeleteCaughtHalfWay(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	d, st, root := besideStorage(t, store)
	writeUnder(t, root, "silinen.txt", "x")
	fillers(t, root, 20)
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	row := mustNode(t, store, st.ID, "/silinen.txt")
	trashRel := ".filex-trash/1700000000-test__silinen.txt"
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".filex-trash"), 0o755))
	waitPastSecondBoundary()

	moved := make(chan struct{})      // the delete's bytes are in the trash
	rowsFollow := make(chan struct{}) // the test lets the delete finish
	var letGo sync.Once
	finish := func() { letGo.Do(func() { close(rowsFollow) }) }
	// Whatever happens below, the delete finishes and lets go of the gate:
	// it is process-wide.
	t.Cleanup(finish)
	moverDone := make(chan struct{})

	d.mu.Lock()
	// The listing misses the file, so the pass judges it.
	d.hide["silinen.txt"] = true
	// The delete starts while the walk lists the last folder: it gets the gate
	// as soon as the walk lets go of it, before the pass judges.
	d.beforeList["kalici"] = func() {
		go func() {
			defer close(moverDone)
			release := rowgate.Move(st.ID)
			defer release()
			if err := os.Rename(filepath.Join(root, "silinen.txt"), filepath.Join(root, filepath.FromSlash(trashRel))); err != nil {
				return
			}
			close(moved)
			<-rowsFollow
			_ = store.SoftDeleteAndRetag(ctx, row.ID, "/"+trashRel, pathkey.Hash(st.ID, "/"+trashRel), "/silinen.txt")
		}()
	}
	// Without the gate the pass asks about the file only once its bytes have
	// left, so the break above fails the same way every time.
	d.beforeStat["silinen.txt"] = func() {
		select {
		case <-moved:
		case <-time.After(10 * time.Second):
		}
	}
	d.mu.Unlock()

	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	scanned := make(chan error, 1)
	go func() { scanned <- w.Trigger(ctx, st.ID) }()
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture: the delete never moved its bytes")
	}
	select {
	case err := <-scanned:
		t.Fatalf("the pass judged while a delete was half way (err %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	finish()
	select {
	case <-moverDone:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture: the delete never finished")
	}
	select {
	case err := <-scanned:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the scan never judged once the delete finished")
	}

	kept, err := store.GetNode(ctx, row.ID)
	require.NoError(t, err, "the row the delete was putting in the trash was dropped")
	require.NotNil(t, kept)
	assert.NotNil(t, kept.DeletedAt, "fixture: the delete's catalogue step ran")
	assert.Equal(t, "/"+trashRel, kept.Path, "the trash entry is not where the delete put it")
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	assert.Zero(t, run.Deleted)
}

package sync_test

// Issue #104, item 6: an entry the storage could not answer for.
//
// The tombstone pass drops a row only when the storage's own Stat says "not
// found". A storage plugin whose Stat answers a folder with anything else (it
// does not speak Stat for folders, it lacks a permission, its backend failed)
// used to leave the row exactly as it was: live, clickable, and saying nothing.
// A folder deleted outside filex stayed in the catalogue for good.
//
// Now the row is kept AND marked (model.Node.Unavailable, with the storage's
// answer), the server is told after the pass (Worker.AttachEntryState: the
// plugin's log, the audit trail on a change), and the next pass that gets an
// answer settles it: Stat succeeds or the walk lists it - the mark goes; Stat
// says "not found" - the row goes.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// errNoAnswer is what a plugin that cannot Stat a folder answers: not
// storage.ErrNotFound, so nothing can tell whether the folder is there.
var errNoAnswer = errors.New("plugin: stat is not implemented for folders (http 500)")

// heard collects what the sync told AttachEntryState.
type heard struct {
	mu     sync.Mutex
	states []filexsync.EntryState
}

func (h *heard) add(_ context.Context, e filexsync.EntryState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.states = append(h.states, e)
}

func (h *heard) take() []filexsync.EntryState {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.states
	h.states = nil
	return out
}

// runSyncHeard is runSync with the entry-state hook attached.
func runSyncHeard(t *testing.T, store db.Store, st *model.Storage, h *heard) *model.SyncRun {
	t.Helper()
	ctx := context.Background()
	w := filexsync.New(store)
	w.AttachEntryState(h.add)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, run.Error)
	return run
}

func row(t *testing.T, store db.Store, id int64) *model.Node {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err, "the row is gone")
	return n
}

func TestUnansweredFolder_IsKeptMarkedAndSettledByTheNextAnswer(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{
		list:    map[string][]storage.Object{"": {}},
		statErr: map[string]error{"Arsiv": errNoAnswer},
	}
	st := scriptStorage(t, store, d)
	dir := seedRowUnder(t, store, st, "/Arsiv", model.NodeTypeDirectory, nil)
	h := &heard{}

	// 1. The walk did not list it and Stat gave no answer: kept, marked, told.
	waitPastSecondBoundary()
	run := runSyncHeard(t, store, st, h)
	n := row(t, store, dir.ID)
	assert.Nil(t, n.DeletedAt, "an entry nobody could check was removed")
	assert.True(t, n.Unavailable, "an entry the storage could not answer for is not marked")
	assert.Contains(t, n.UnavailableReason, "stat is not implemented for folders")
	assert.NotNil(t, n.UnavailableAt)
	assert.Zero(t, run.Deleted)
	got := h.take()
	require.Len(t, got, 1, "the server is told once about the entry")
	assert.Equal(t, dir.ID, got[0].NodeID)
	assert.Equal(t, "/Arsiv", got[0].Path)
	assert.Contains(t, got[0].Reason, "stat is not implemented")
	assert.True(t, got[0].Changed, "the first answer changes the entry's state")

	// 2. The same answer again: still marked, and told it is the same.
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	assert.True(t, row(t, store, dir.ID).Unavailable)
	got = h.take()
	require.Len(t, got, 1)
	assert.False(t, got[0].Changed, "the same answer is not a change (no new audit row)")

	// 3. The storage answers for it again: the mark goes.
	d.mu.Lock()
	delete(d.statErr, "Arsiv")
	d.mu.Unlock()
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	n = row(t, store, dir.ID)
	assert.False(t, n.Unavailable, "an entry the storage answers for again stays marked")
	assert.Empty(t, n.UnavailableReason)
	got = h.take()
	require.Len(t, got, 1)
	assert.True(t, got[0].Changed)
	assert.Empty(t, got[0].Reason, "an answered entry carries no reason")

	// 4. Marked again, then a definite "not found": the row goes, as before.
	d.mu.Lock()
	d.statErr["Arsiv"] = errNoAnswer
	d.mu.Unlock()
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	require.True(t, row(t, store, dir.ID).Unavailable)
	d.mu.Lock()
	d.statErr["Arsiv"] = storage.ErrNotFound
	d.mu.Unlock()
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	assert.True(t, gone(t, store, dir.ID), "an entry the storage says is gone is not dropped")
}

// A file is marked the same way: "file or folder, it does not matter".
func TestUnansweredFile_IsMarkedToo(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{
		list:    map[string][]storage.Object{"": {}},
		statErr: map[string]error{"rapor.pdf": errors.New("permission denied")},
	}
	st := scriptStorage(t, store, d)
	f := seedRowUnder(t, store, st, "/rapor.pdf", model.NodeTypeFile, nil)
	h := &heard{}
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	n := row(t, store, f.ID)
	assert.True(t, n.Unavailable)
	assert.Equal(t, "permission denied", n.UnavailableReason)
}

// The walk listing the entry again is an answer too: the mark goes without
// Stat being asked about it at all.
func TestAMarkedEntryListedAgainIsAvailable(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{
		list:    map[string][]storage.Object{"": {}},
		statErr: map[string]error{"rapor.pdf": errNoAnswer},
	}
	st := scriptStorage(t, store, d)
	f := seedRowUnder(t, store, st, "/rapor.pdf", model.NodeTypeFile, nil)
	h := &heard{}
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	require.True(t, row(t, store, f.ID).Unavailable)
	h.take()

	d.mu.Lock()
	d.list[""] = []storage.Object{{Path: "rapor.pdf", Name: "rapor.pdf", Kind: storage.KindFile, Size: 1, Mtime: time.Now()}}
	d.mu.Unlock()
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	assert.False(t, row(t, store, f.ID).Unavailable, "the walk listed it and it stayed marked")
	got := h.take()
	require.Len(t, got, 1)
	assert.True(t, got[0].Changed)
}

// A row the storage's Stat finds is not marked at all (the listing missed it).
func TestAnEntryStatFindsIsNotMarked(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	d := &scriptDriver{list: map[string][]storage.Object{"": {}}, statErr: map[string]error{}}
	st := scriptStorage(t, store, d)
	f := seedRowUnder(t, store, st, "/var.txt", model.NodeTypeFile, nil)
	h := &heard{}
	waitPastSecondBoundary()
	runSyncHeard(t, store, st, h)
	assert.False(t, row(t, store, f.ID).Unavailable)
	assert.Empty(t, h.take(), "nothing to tell about an ordinary row")
}

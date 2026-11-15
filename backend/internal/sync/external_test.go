package sync_test

// What the catalogue does with a change a filex-watch agent saw on the machine
// that owns the disk (sync/external.go, docs/FILEX-WATCH.md): on a storage
// that is not lazy, the folder's subtree is rescanned - from the nearest
// catalogued folder, the root being a full scan - and what the scan never
// looks at is never looked at for a report either.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestExternalChanges_RescansTheReportedFolderAlone(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "a/old.txt", "x")
	writeUnder(t, root, "b/old.txt", "x")
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	runs := syncRunCount(t, store, st.ID)

	waitPastSecondBoundary()
	writeUnder(t, root, "a/new.txt", "new")
	writeUnder(t, root, "b/new.txt", "new")

	later, err := w.ExternalChanges(ctx, st.ID, []string{"/a"})
	require.NoError(t, err)
	assert.Empty(t, later)
	assert.NotNil(t, node(t, store, st.ID, "/a/new.txt"), "the reported folder was looked at")
	assert.Nil(t, node(t, store, st.ID, "/b/new.txt"), "a folder nobody reported was not")
	assert.Equal(t, runs, syncRunCount(t, store, st.ID), "a folder's rescan is not a sync run")
}

// A folder made outside filex has no row yet: the nearest catalogued folder
// above it is rescanned, and that finds it with everything in it.
func TestExternalChanges_ANewFolderClimbsToItsCataloguedParent(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "a/old.txt", "x")
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)

	writeUnder(t, root, "a/yeni/derin/dosya.txt", "new")
	later, err := w.ExternalChanges(ctx, st.ID, []string{"/a/yeni/derin"})
	require.NoError(t, err)
	assert.Empty(t, later)
	assert.NotNil(t, node(t, store, st.ID, "/a/yeni"))
	assert.NotNil(t, node(t, store, st.ID, "/a/yeni/derin/dosya.txt"))
}

// The root has no folder rescan: it is a full scan, started in the
// background, and a report while one walks is handed back for later.
func TestExternalChanges_TheRootIsAFullScanInTheBackground(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "old.txt", "x")
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	runs := syncRunCount(t, store, st.ID)

	writeUnder(t, root, "new.txt", "new")
	later, err := w.ExternalChanges(ctx, st.ID, []string{"/"})
	require.NoError(t, err)
	assert.Empty(t, later)
	require.Eventually(t, func() bool { return node(t, store, st.ID, "/new.txt") != nil }, 20*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return !w.Running(st.ID) }, 20*time.Second, 50*time.Millisecond)
	assert.Equal(t, runs+1, syncRunCount(t, store, st.ID), "the root's change is a full scan")
}

// filex's own trees and the storage's scan exclusions are not looked at for a
// report, exactly as the scan does not look at them.
func TestExternalChanges_LeavesOutWhatTheScanNeverWalks(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg, err := json.Marshal(map[string]any{"root": root, "scan_exclude": ".git\nincomplete/**"})
	require.NoError(t, err)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "haric", Driver: "local", MountPath: "/haric", ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	writeUnder(t, root, "docs/a.txt", "a")
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	runs := syncRunCount(t, store, st.ID)

	writeUnder(t, root, "docs/.git/HEAD", "ref")
	writeUnder(t, root, "incomplete/part.bin", "x")
	later, err := w.ExternalChanges(ctx, st.ID, []string{"/docs/.git", "/incomplete", "/.versions/12", "/.filex-trash"})
	require.NoError(t, err)
	assert.Empty(t, later)
	assert.Nil(t, node(t, store, st.ID, "/docs/.git/HEAD"))
	assert.Nil(t, node(t, store, st.ID, "/incomplete/part.bin"))
	assert.False(t, w.Running(st.ID), "nothing was started for them")
	assert.Equal(t, runs, syncRunCount(t, store, st.ID))
}

// A storage with no syncer (gone, switched off) is said so; nothing looks.
func TestExternalChanges_NoSyncer(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	w := filexsync.New(store)
	t.Cleanup(w.Stop)
	_, err := w.ExternalChanges(context.Background(), 404, []string{"/a"})
	assert.ErrorIs(t, err, filexsync.ErrNoSyncer)
	_, err = w.ExternalResync(context.Background(), 404, []string{"/"})
	assert.ErrorIs(t, err, filexsync.ErrNoSyncer)
}

// On a storage that is not lazy a resync is the same rescan: a subtree the
// agent could not watch is read whole.
func TestExternalResync_RescansTheSubtree(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	writeUnder(t, root, "buyuk/a/old.txt", "x")
	w, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)

	writeUnder(t, root, "buyuk/a/b/c/new.txt", "new")
	later, err := w.ExternalResync(ctx, st.ID, []string{"/buyuk"})
	require.NoError(t, err)
	assert.Empty(t, later)
	assert.NotNil(t, node(t, store, st.ID, "/buyuk/a/b/c/new.txt"))
}

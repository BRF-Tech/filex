package sync_test

// Issue #192: a folder renamed through the queue opened EMPTY for a while
// (e2e 159 and 172, WebKit, the 0.53 run under load), and its files came back
// on the next scan as new files - without their shares, versions or comments.
//
// What happened, read off the run's trace and server log: a full scan
// (fsnotify, two seconds after the folder was written) ran beside the queued
// rename. The scan listed the folder's PARENT before the rename moved the
// bytes, and listed the folder itself after: the listing said "not found", the
// walk read that as "listed, nothing in it", every row below the folder looked
// unseen, the tombstone pass confirmed each one gone by a Stat of the path the
// row still named, and dropped it - while the rename's own catalogue step was
// about to re-home those very rows. Nothing was logged: a drop was silent.
//
// The fix has three parts; the tests here break the first two, and
// rename_gate_test.go the third:
//
//   - a folder its parent's listing showed but that is gone when the walk
//     reaches it is a folder the walk could not list, never an empty one
//     (poll.go walkFrom);
//   - a row is read again before it is dropped, and kept when it moved after
//     it was confirmed gone (tombstone.go dropRows);
//   - a rename, a move, a delete and a restore hold the storage's row gate
//     from the first byte to the last row, and the scan judges only while no
//     such change is half way (internal/rowgate).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// besideDriver is a real disk with a hand on two of the scan's questions: a
// step of a rename can be made to land exactly before the scan lists a
// directory (beforeList) or asks about one path (beforeStat). Keys are
// storage paths without the leading slash. hide leaves names out of the
// listing of the directory that holds them, as a listing that missed them.
type besideDriver struct {
	*local.Driver
	mu         gosync.Mutex
	beforeList map[string]func()
	beforeStat map[string]func()
	hide       map[string]bool
}

func (d *besideDriver) take(hooks map[string]func(), p string) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := strings.Trim(p, "/")
	hook := hooks[k]
	delete(hooks, k)
	return hook
}

func (d *besideDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	if hook := d.take(d.beforeList, p); hook != nil {
		hook()
	}
	objs, err := d.Driver.List(ctx, p)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	kept := objs[:0]
	for _, o := range objs {
		if !d.hide[strings.Trim(o.Path, "/")] {
			kept = append(kept, o)
		}
	}
	return kept, nil
}

func (d *besideDriver) Stat(ctx context.Context, p string) (storage.Object, error) {
	if hook := d.take(d.beforeStat, p); hook != nil {
		hook()
	}
	return d.Driver.Stat(ctx, p)
}

// besideStorage is a local storage served through a besideDriver registered
// under a name of its own. SyncModeOnDemand: no pass runs but the test's.
func besideStorage(t *testing.T, store db.Store) (*besideDriver, *model.Storage, string) {
	t.Helper()
	root := t.TempDir()
	base := &local.Driver{}
	require.NoError(t, base.Init(context.Background(), map[string]any{"root": root}))
	d := &besideDriver{
		Driver:     base,
		beforeList: map[string]func(){},
		beforeStat: map[string]func(){},
		hide:       map[string]bool{},
	}
	name := fmt.Sprintf("beside-%d", time.Now().UnixNano())
	storage.Register(name, func() storage.Driver { return d })
	cfg, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: name, MountPath: "/" + name, ConfigJSON: cfg,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	return d, st, root
}

// childNames lists the names of the live rows whose parent is dir — what the
// explorer's listing of the folder shows (vfIndex: ListNodesByParent).
func childNames(t *testing.T, store db.Store, storageID, dir int64) []string {
	t.Helper()
	kids, err := store.ListNodesByParent(context.Background(), storageID, &dir)
	require.NoError(t, err)
	names := []string{}
	for _, k := range kids {
		if k.DeletedAt == nil {
			names = append(names, k.Name)
		}
	}
	sort.Strings(names)
	return names
}

// captureLog runs f with the default logger writing into a buffer.
func captureLog(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	f()
	return buf.String()
}

// The failure itself, step by step: the scan lists the root (the folder is
// there), the rename moves the bytes, the scan lists the folder (not found),
// the scan judges, and only then does the rename's catalogue step run.
//
// Break: in walkFrom, read a folder whose listing says "not found" as listed
// and empty again (return 0, nil for errDirGone at the recursion) - the
// folder's three rows are dropped, the run reports three deletions, and after
// the rename the folder lists nothing.
func TestAFolderRenamedBesideTheScanKeepsItsContents(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	d, st, root := besideStorage(t, store)
	writeUnder(t, root, "klasor/icinde.txt", "icinde")
	writeUnder(t, root, "klasor/alt/derin.txt", "derin")
	fillers(t, root, 20)
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	dir := mustNode(t, store, st.ID, "/klasor")
	file := mustNode(t, store, st.ID, "/klasor/icinde.txt")
	sub := mustNode(t, store, st.ID, "/klasor/alt")
	deep := mustNode(t, store, st.ID, "/klasor/alt/derin.txt")

	var renameErr error
	d.mu.Lock()
	d.beforeList["klasor"] = func() {
		renameErr = os.Rename(filepath.Join(root, "klasor"), filepath.Join(root, "yeni"))
	}
	d.mu.Unlock()

	waitPastSecondBoundary()
	var run *model.SyncRun
	logged := captureLog(t, func() { _, run = runSync(t, store, st) })
	require.NoError(t, renameErr, "fixture: the rename's first step")
	require.Equal(t, "ok", run.Status, run.Error)
	// The rename's second step, after the scan judged.
	require.True(t, protocolsync.New(store, nil, nil, "test").MoveRows(ctx, st, "klasor", "yeni"),
		"fixture: the rename's catalogue step found its row")

	assert.Zero(t, run.Deleted, "the scan dropped rows of a folder it could not list")
	for _, n := range []*model.Node{file, sub, deep} {
		assert.False(t, gone(t, store, n.ID), "the row of %s was dropped while its folder was being renamed", n.Path)
	}
	assert.Equal(t, []string{"alt", "icinde.txt"}, childNames(t, store, st.ID, dir.ID),
		"the renamed folder opens empty")
	moved := mustNode(t, store, st.ID, "/yeni/icinde.txt")
	assert.Equal(t, file.ID, moved.ID, "the file came back as another row, without its shares, versions and comments")
	assert.Contains(t, logged, "was gone when the walk reached it",
		"the server log says why nothing below the folder was judged")
}

// A row a move re-homes between the scan's Stat of its old path and the drop
// is the moved file, not a deleted one. This is the gap the row gate does not
// cover: a surface that moves without it, or a second filex process on the
// same database.
//
// Break: in dropRows, drop without reading the row again (or without
// comparing where it stands) - the moved file's row is dropped by its id.
func TestARowMovedBetweenItsStatAndItsDropIsKept(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	d, st, root := besideStorage(t, store)
	writeUnder(t, root, "a.txt", "tasinan")
	fillers(t, root, 20)
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	row := mustNode(t, store, st.ID, "/a.txt")

	var moveErr error
	d.mu.Lock()
	// The listing misses it, so it is a candidate; the move lands while the
	// pass is asking about it.
	d.hide["a.txt"] = true
	d.beforeStat["a.txt"] = func() {
		if moveErr = os.Rename(filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")); moveErr != nil {
			return
		}
		moveErr = store.MoveNode(ctx, row.ID, nil, "b.txt", "/b.txt", pathkey.Hash(st.ID, "/b.txt"))
	}
	d.mu.Unlock()

	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.NoError(t, moveErr, "fixture: the move")
	require.Equal(t, "ok", run.Status, run.Error)

	assert.Zero(t, run.Deleted, "the pass dropped a row that had moved")
	moved := node(t, store, st.ID, "/b.txt")
	require.NotNil(t, moved, "the moved file has no row")
	assert.Equal(t, row.ID, moved.ID, "the moved file is not the row it was")
}

// A drop is said, not silent: the server log of the 0.53 run had nothing to
// tell a dropped row from a bug.
//
// Break: take the INFO line out of dropRows.
func TestADroppedRowIsSaidInTheLog(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	_, st, root := besideStorage(t, store)
	writeUnder(t, root, "silinecek.txt", "x")
	fillers(t, root, 20)
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	gonePath := mustNode(t, store, st.ID, "/silinecek.txt")
	require.NoError(t, os.Remove(filepath.Join(root, "silinecek.txt")))

	waitPastSecondBoundary()
	var run *model.SyncRun
	logged := captureLog(t, func() { _, run = runSync(t, store, st) })
	require.Equal(t, "ok", run.Status, run.Error)
	require.True(t, gone(t, store, gonePath.ID), "fixture: a file deleted outside filex is dropped")
	assert.Contains(t, logged, "dropped the row of an object gone from the storage")
	assert.Contains(t, logged, "/silinecek.txt")
}

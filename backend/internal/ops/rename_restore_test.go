package ops_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// A rename and a restore as jobs of the queue.
//
// ⚠⚠ Why (2026-09-26). Both ran inside the request. A folder renamed, or
// brought back from the trash, on an object store is one request per object:
// the dialog waited with nothing on screen until the proxy gave up (nginx after
// 60 s, Cloudflare after 100 s), then said it had failed while the server
// carried on. As jobs they answer at once and show in the operations centre,
// and the one worker runs them, so a second press cannot overlap the first.

func TestOpsWorker_Rename_GivesAFolderExactlyTheNewName(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	f.seedIn(t, "Leon", "b.txt", "b")

	op := f.runOp(t, ops.OpRename, []string{"Leon"}, "Leo")
	require.Equal(t, ops.StatusOK, op.Status, "rename op error: %s", op.Error)

	assert.Equal(t, "a", readAll(t, f, "Leo/a.txt"))
	assert.Equal(t, "b", readAll(t, f, "Leo/b.txt"))
	_, err := f.drv.Stat(context.Background(), "Leon")
	assert.Error(t, err, "the old name is still on the storage")
	assert.NotNil(t, f.nodeAt(t, "Leo"), "the catalogue did not follow the rename")
	assert.NotNil(t, f.nodeAt(t, "Leo/a.txt"), "the catalogue did not follow the rename into the folder")
	assert.Nil(t, f.nodeAt(t, "Leon"), "the catalogue still has the old name")
}

// A rename never replaces what has the name, and is not given another one the
// way a move is: the person chose this name, and the explorer's undo assumes
// the item landed exactly there.
func TestOpsWorker_Rename_OntoATakenName_FailsAndKeepsBoth(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "a.txt", "incoming")
	f.seedFile(t, "b.txt", "was-here")

	op := f.runOp(t, ops.OpRename, []string{"a.txt"}, "b.txt")
	require.Equal(t, ops.StatusFailed, op.Status, "a rename onto a taken name must fail")
	assert.Contains(t, op.Error, "already exists")
	assert.Equal(t, "was-here", readAll(t, f, "b.txt"), "the file that had the name was replaced")
	assert.Equal(t, "incoming", readAll(t, f, "a.txt"), "the item left its name anyway")
	_, err := f.drv.Stat(context.Background(), "b-copy.txt")
	assert.Error(t, err, "a rename was given another name, the way a move is")
}

// A live catalogue row holds its name even when its bytes went missing: the
// storage sees a free name, but renaming onto it would make the catalogue drop
// that row, with its versions, shares and comments.
func TestOpsWorker_Rename_OntoARowWhoseBytesAreGone_Fails(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "a.txt", "incoming")
	f.seedFile(t, "b.txt", "gone")
	require.NoError(t, f.drv.Delete(context.Background(), "b.txt"))

	op := f.runOp(t, ops.OpRename, []string{"a.txt"}, "b.txt")
	require.Equal(t, ops.StatusFailed, op.Status, "the rename took the name of a live row")
	assert.Contains(t, op.Error, "already exists")
	assert.Equal(t, "incoming", readAll(t, f, "a.txt"))
	assert.NotNil(t, f.nodeAt(t, "b.txt"), "the row that held the name is gone")
}

// Changing only the case is not a collision with itself, on a disk that
// ignores case as much as on one that does not.
func TestOpsWorker_Rename_OnlyTheCase(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "rapor.txt", "x")

	op := f.runOp(t, ops.OpRename, []string{"rapor.txt"}, "Rapor.txt")
	require.Equal(t, ops.StatusOK, op.Status, "rename op error: %s", op.Error)
	objs, err := f.drv.List(context.Background(), "")
	require.NoError(t, err)
	var names []string
	for _, o := range objs {
		names = append(names, o.Name)
	}
	assert.Contains(t, names, "Rapor.txt")
	assert.NotContains(t, names, "rapor.txt")
}

func TestSubmit_ARenameIsOneItemAndItsNewPath(t *testing.T) {
	f := newOpsFixture(t)
	ctx := context.Background()
	_, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"a.txt", "b.txt"}, "c.txt")
	refused(t, err, "two items renamed to one name")
	_, err = f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"a.txt"}, "")
	refused(t, err, "a rename with no new name")
}

// withRestorer wires the trash handler as the queue's Restorer, the way
// routes.go does.
func (f *opsFixture) withRestorer() {
	resolve := func(int64) (storage.Driver, error) { return f.drv, nil }
	f.svc.SetRestorer(handlers.NewTrash(trash.New(f.store, resolve, nil), f.store))
}

// trashAll puts rels in the trash through the queue and returns their node ids.
func (f *opsFixture) trashAll(t *testing.T, rels ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(rels))
	for _, rel := range rels {
		n := f.nodeAt(t, rel)
		require.NotNil(t, n, "no row at %s", rel)
		ids = append(ids, strconv.FormatInt(n.ID, 10))
	}
	op := f.runOp(t, ops.OpDelete, rels, "")
	require.Equal(t, ops.StatusOK, op.Status, "delete op error: %s", op.Error)
	return ids
}

func TestOpsWorker_Restore_BringsAFolderBack(t *testing.T) {
	f := newOpsFixture(t)
	f.withRestorer()
	f.seedDir(t, "Leon")
	f.seedFile(t, "Leon/a.txt", "a")
	f.seedFile(t, "Leon/b.txt", "b")
	ids := f.trashAll(t, "Leon")

	op := f.runOp(t, ops.OpRestore, ids, "")
	require.Equal(t, ops.StatusOK, op.Status, "restore op error: %s", op.Error)

	assert.Equal(t, "a", readAll(t, f, "Leon/a.txt"))
	assert.Equal(t, "b", readAll(t, f, "Leon/b.txt"))
	id, _ := strconv.ParseInt(ids[0], 10, 64)
	n, err := f.store.GetNode(context.Background(), id)
	require.NoError(t, err)
	assert.Nil(t, n.DeletedAt, "the catalogue still has the folder in the trash")
}

// One entry whose place is taken does not stop the others; it fails on its
// own, and says why.
func TestOpsWorker_Restore_ATakenPlaceFailsThatEntryAlone(t *testing.T) {
	f := newOpsFixture(t)
	f.withRestorer()
	f.seedFile(t, "a.txt", "old-a")
	f.seedFile(t, "b.txt", "old-b")
	ids := f.trashAll(t, "a.txt", "b.txt")
	f.seedFile(t, "a.txt", "new-a")

	op := f.runOp(t, ops.OpRestore, ids, "")
	require.Equal(t, ops.StatusPartial, op.Status, "restore op error: %s", op.Error)
	assert.Equal(t, 1, op.Done)
	assert.Equal(t, 1, op.Failed)
	assert.Contains(t, op.Error, "already exists")
	assert.Equal(t, "new-a", readAll(t, f, "a.txt"), "the file that holds the place was replaced")
	assert.Equal(t, "old-b", readAll(t, f, "b.txt"))
}

func TestOpsWorker_Restore_WithNoRestorerWiredFails(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "a.txt", "a")
	ids := f.trashAll(t, "a.txt")

	op := f.runOp(t, ops.OpRestore, ids, "")
	assert.Equal(t, ops.StatusFailed, op.Status)
}

func TestSubmit_ARestoreNamesTrashEntriesByID(t *testing.T) {
	f := newOpsFixture(t)
	ctx := context.Background()
	for _, bad := range [][]string{{"a.txt"}, {"0"}, {"-3"}, {"12", "x"}} {
		_, err := f.svc.Submit(ctx, ops.OpRestore, f.st.ID, bad, "")
		refused(t, err, strings.Join(bad, ","))
	}
}

// refused is an error for the reason the test names — not the queue failing to
// know the kind at all.
func refused(t *testing.T, err error, what string) {
	t.Helper()
	if assert.Error(t, err, what) {
		assert.NotContains(t, err.Error(), "unknown kind", what)
	}
}

// seedIn is seedFile for a file inside a seeded folder, with its row under the
// folder's: the catalogue follows a rename down parent_id, as it finds a real
// tree.
func (f *opsFixture) seedIn(t *testing.T, dir, name, body string) {
	t.Helper()
	ctx := context.Background()
	parent := f.nodeAt(t, dir)
	require.NotNil(t, parent, "no row at %s", dir)
	rel := dir + "/" + name
	require.NoError(t, f.drv.Write(ctx, rel, strings.NewReader(body), int64(len(body))))
	_, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, ParentID: &parent.ID, Name: name, Path: rel,
		PathHash: opsPathHash(f.st.ID, rel), Type: model.NodeTypeFile, Size: int64(len(body)),
	})
	require.NoError(t, err)
}

// A running rename cannot be cancelled: stopped between two objects, a folder
// is left in two places. While it waits in the queue it still can be.
func TestOpsWorker_ARunningRenameIsNotStoppedHalfWay(t *testing.T) {
	f := newPoolFixture(t, 1, &movingDriver{hold: 300 * time.Millisecond})
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
	require.NoError(t, err)
	assert.True(t, op.Cancellable, "a rename waiting in the queue can still be cancelled")

	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	deadline := time.Now().Add(3 * time.Second)
	for {
		cur, err := f.svc.Get(ctx, op.ID)
		require.NoError(t, err)
		if cur.Status == ops.StatusRunning {
			assert.False(t, cur.Cancellable, "a running rename is advertised as cancellable")
			break
		}
		require.True(t, time.Now().Before(deadline), "the rename never started (status %s)", cur.Status)
		time.Sleep(5 * time.Millisecond)
	}
	ok, err := f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	assert.False(t, ok, "a running rename was cancelled")

	done := waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "rename op error: %s", done.Error)
	assert.Equal(t, "a", readAll(t, f, "Leo/a.txt"))
}

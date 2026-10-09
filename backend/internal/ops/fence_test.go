package ops_test

// sec055 follow-up: a queued rename, move or delete of a folder on an object
// store moves it one object at a time, for as long as the folder is large. It
// fences the folder's prefixes (rowgate.FenceCtx, holdFor) instead of holding
// the storage's row gate: the storage's scan goes on everywhere else while it
// runs. A cancelled job lets its fence go.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// objectMoving is a movingDriver seen as an object store (storage.TreeWalker).
type objectMoving struct{ *movingDriver }

func (o objectMoving) WalkTree(ctx context.Context, p string, fn func(storage.Object) error) error {
	objs, err := o.List(ctx, p)
	if err != nil {
		return nil
	}
	for _, obj := range objs {
		if err := fn(obj); err != nil {
			return err
		}
		if obj.Kind == storage.KindDirectory {
			if err := o.WalkTree(ctx, obj.Path, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// newObjectStoreFixture is newPoolFixture whose storage is an object store.
func newObjectStoreFixture(t *testing.T, drv *movingDriver) *opsFixture {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	dir := t.TempDir()
	base := &local.Driver{}
	require.NoError(t, base.Init(ctx, map[string]any{"root": dir}))
	drv.Driver = base
	cfg, _ := json.Marshal(map[string]any{"root": dir})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "bucket", Driver: "local", MountPath: "/bucket", Enabled: true, ConfigJSON: cfg,
	})
	require.NoError(t, err)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return objectMoving{drv}, nil
	}
	svc := ops.New(sqlDB, resolver)
	require.NoError(t, svc.Migrate(ctx))
	svc.SetSync(handlers.NewManager(store, resolver))
	return &opsFixture{svc: svc, store: store, drv: base, st: st}
}

// halfWayMover is a movingDriver that stops once its first Move has moved the
// bytes, until finish.
func halfWayMover(t *testing.T) (drv *movingDriver, moved <-chan struct{}, finish func()) {
	m := make(chan struct{})
	letGo := make(chan struct{})
	var movedOnce, letGoOnce sync.Once
	finish = func() { letGoOnce.Do(func() { close(letGo) }) }
	// Fences are process-wide: whatever happens below, the change finishes.
	t.Cleanup(finish)
	drv = &movingDriver{afterMove: func() {
		movedOnce.Do(func() { close(m) })
		<-letGo
	}}
	return drv, m, finish
}

// scanHalfWay asks for the storage's gate the way a scan does, half way
// through a long change: it must get it at once, and see fenced fenced and
// free not.
func scanHalfWay(t *testing.T, storageID int64, fenced []string, free string) {
	t.Helper()
	// The change is parked half way (halfWayMover) until the test lets it
	// go: a change that held the gate would hold it for all of this wait, so
	// a generous one proves the same and does not race a loaded host.
	release, err := rowgate.JudgeWithin(context.Background(), storageID, 5*time.Second)
	require.NoError(t, err, "a folder moved object by object held the whole storage's row gate: the rest of it could not be scanned")
	defer release()
	f := rowgate.Fences(storageID)
	for _, p := range fenced {
		assert.True(t, f.Covers(p), "%s is not fenced half way", p)
	}
	assert.False(t, f.Covers(free), "%s is fenced, but the change does not touch it", free)
}

// Break: hold rowgate.Move in runRename whatever holdFor says - the scan does
// not get the gate half way.
func TestOpsWorker_RenamingAFolderOnAnObjectStoreFencesItsPrefixes(t *testing.T) {
	drv, moved, finish := halfWayMover(t)
	f := newObjectStoreFixture(t, drv)
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	f.seedDir(t, "Baska")
	ctx := context.Background()

	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("the rename never moved its bytes")
	}
	scanHalfWay(t, f.st.ID, []string{"/Leon", "/Leon/a.txt", "/Leo", "/Leo/a.txt"}, "/Baska")

	finish()
	done := waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "rename op error: %s", done.Error)
	assert.True(t, rowgate.Fences(f.st.ID).Empty(), "the fence is still there after the rename")
	n, _ := f.store.GetNodeByPath(ctx, f.st.ID, opsPathHash(f.st.ID, "Leo/a.txt"))
	assert.NotNil(t, n, "the rows did not follow the rename")
}

// A queued move of a folder into another fences the folder and its
// destination; cancelled half way, the job ends and lets its fence go.
//
// Break: release the fence without defer in runOne's OpMove - the cancelled
// job leaves it standing, and the prefixes are never scanned again.
func TestOpsWorker_ACancelledFolderMoveOnAnObjectStoreLetsItsFenceGo(t *testing.T) {
	drv, moved, finish := halfWayMover(t)
	f := newObjectStoreFixture(t, drv)
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	f.seedDir(t, "Hedef")
	f.seedDir(t, "Baska")
	ctx := context.Background()

	// "Hedef/": INTO the folder (the handlers' form, ops.go). "Hedef" alone
	// names the destination itself, and an existing Hedef makes it
	// Hedef-copy (MoveDest).
	op, err := f.svc.Submit(ctx, ops.OpMove, f.st.ID, []string{"Leon"}, "Hedef/")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("the move never moved its bytes")
	}
	scanHalfWay(t, f.st.ID, []string{"/Leon", "/Hedef/Leon"}, "/Baska")

	_, err = f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	finish()
	waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.True(t, rowgate.Fences(f.st.ID).Empty(), "a cancelled move left its fence standing")
}

// A queued delete of a folder on an object store fences the folder while it
// goes to the trash, object by object.
func TestOpsWorker_TrashingAFolderOnAnObjectStoreFencesIt(t *testing.T) {
	drv, moved, finish := halfWayMover(t)
	f := newObjectStoreFixture(t, drv)
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	f.seedDir(t, "Baska")
	ctx := context.Background()

	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Leon"}, "")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("the delete never moved its bytes")
	}
	scanHalfWay(t, f.st.ID, []string{"/Leon", "/Leon/a.txt"}, "/Baska")

	finish()
	done := waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "delete op error: %s", done.Error)
	assert.True(t, rowgate.Fences(f.st.ID).Empty(), "the fence is still there after the delete")
}

// A queued restore of a folder on an object store brings it back one object
// at a time: it fences the folder's trash key and its original path
// (ops.RestoreFencer, handlers.Trash.RestoreFence) instead of holding the
// storage's gate.
//
// Break: hold rowgate.Move in runRestore whatever the restorer's fence says -
// the scan does not get the gate half way.
func TestOpsWorker_RestoringAFolderOnAnObjectStoreFencesIt(t *testing.T) {
	var armed atomic.Bool
	moved := make(chan struct{})
	letGo := make(chan struct{})
	var movedOnce, letGoOnce sync.Once
	finish := func() { letGoOnce.Do(func() { close(letGo) }) }
	t.Cleanup(finish)
	drv := &movingDriver{afterMove: func() {
		if !armed.Load() {
			return
		}
		movedOnce.Do(func() { close(moved) })
		<-letGo
	}}
	f := newObjectStoreFixture(t, drv)
	resolve := func(int64) (storage.Driver, error) { return objectMoving{drv}, nil }
	f.svc.SetRestorer(handlers.NewTrash(trash.New(f.store, resolve, nil), f.store))
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	f.seedDir(t, "Baska")
	ids := f.trashAll(t, "Leon")
	ctx := context.Background()

	armed.Store(true)
	op, err := f.svc.Submit(ctx, ops.OpRestore, f.st.ID, ids, "")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("the restore never moved its bytes")
	}
	scanHalfWay(t, f.st.ID, []string{"/Leon", "/Leon/a.txt"}, "/Baska")

	finish()
	done := waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "restore op error: %s", done.Error)
	assert.True(t, rowgate.Fences(f.st.ID).Empty(), "the fence is still there after the restore")
	assert.Equal(t, "a", readAll(t, f, "Leon/a.txt"), "the folder's file did not come back")
	id, _ := strconv.ParseInt(ids[0], 10, 64)
	n, err := f.store.GetNode(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, n.DeletedAt, "the catalogue still has the folder in the trash")
}

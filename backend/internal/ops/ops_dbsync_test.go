package ops_test

// End-to-end regression tests for the async ops worker's DB-sync hook.
//
// The bug these lock down: the worker moved/deleted/copied bytes on the
// storage driver but never updated the DB node index. Directory listings read
// the DB (Store.ListNodesByParent), so a moved file kept showing in its old
// folder, a deleted file kept showing entirely, and a copy never appeared.
// The fix wires the manager handler in as the worker's DBSync, and routes
// delete through the trash (soft-delete). These tests assert BOTH the
// byte-level driver effect AND the DB-cache mirror for move/delete/copy.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type opsFixture struct {
	svc   *ops.Service
	store db.Store
	drv   *local.Driver
	st    *model.Storage
}

func newOpsFixture(t *testing.T) *opsFixture {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	dir := t.TempDir()

	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": dir}))

	cfg := strings.ReplaceAll(strings.ReplaceAll(dir, `\`, `\\`), `"`, `\"`)
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + cfg + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}

	svc := ops.New(sqlDB, resolver)
	require.NoError(t, svc.Migrate(ctx))
	// The real wiring (routes.go) injects the manager handler as DBSync.
	svc.SetSync(handlers.NewManager(store, resolver))

	return &opsFixture{svc: svc, store: store, drv: drv, st: st}
}

func opsPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

func (f *opsFixture) seedFile(t *testing.T, rel, body string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.drv.Write(ctx, rel, strings.NewReader(body), int64(len(body))))
	_, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: path.Base(rel), Path: rel,
		PathHash: opsPathHash(f.st.ID, rel), Type: model.NodeTypeFile, Size: int64(len(body)),
	})
	require.NoError(t, err)
}

func (f *opsFixture) seedDir(t *testing.T, rel string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.drv.Mkdir(ctx, rel))
	_, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: path.Base(rel), Path: rel,
		PathHash: opsPathHash(f.st.ID, rel), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
}

// runOp submits an op, runs the worker until it leaves the queue, and returns
// the finished op. Submit pokes the worker so it processes immediately.
func (f *opsFixture) runOp(t *testing.T, kind string, sources []string, dest string) *ops.Op {
	t.Helper()
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, kind, f.st.ID, sources, dest)
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.svc.Run(runCtx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		cur, err := f.svc.Get(ctx, op.ID)
		require.NoError(t, err)
		if cur.Status == ops.StatusOK || cur.Status == ops.StatusFailed || cur.Status == ops.StatusPartial {
			f.svc.Stop()
			return cur
		}
		if time.Now().After(deadline) {
			f.svc.Stop()
			t.Fatalf("op %d (%s) did not finish; last status=%s err=%s", op.ID, kind, cur.Status, cur.Error)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func TestOpsWorker_Move_UpdatesDriverAndDB(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedFile(t, "src.txt", "hello")

	op := f.runOp(t, ops.OpMove, []string{"src.txt"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "move op error: %s", op.Error)

	// driver: bytes moved into the folder
	_, err := f.drv.Stat(ctx, "dest/src.txt")
	require.NoError(t, err, "file should be in dest/ on disk")
	_, err = f.drv.Stat(ctx, "src.txt")
	require.ErrorIs(t, err, storage.ErrNotFound, "file should be gone from old path on disk")

	// DB: node retargeted to the new path (the bug: it stayed at the old path)
	moved, err := f.store.GetNodeByPath(ctx, f.st.ID, opsPathHash(f.st.ID, "dest/src.txt"))
	require.NoError(t, err)
	require.NotNil(t, moved, "DB node must exist at dest/src.txt after move")
	require.Contains(t, moved.Path, "dest/src.txt") // normalizeDBPath prepends "/"

	old, _ := f.store.GetNodeByPath(ctx, f.st.ID, opsPathHash(f.st.ID, "src.txt"))
	require.Nil(t, old, "DB node must NOT still list the file at the old path")
}

func TestOpsWorker_Delete_SoftDeletesToTrashAndDB(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	f.seedFile(t, "doomed.txt", "bye")

	op := f.runOp(t, ops.OpDelete, []string{"doomed.txt"}, "")
	require.Equal(t, ops.StatusOK, op.Status, "delete op error: %s", op.Error)

	// driver: original gone, a copy now lives under .filex-trash/
	_, err := f.drv.Stat(ctx, "doomed.txt")
	require.ErrorIs(t, err, storage.ErrNotFound, "original bytes must be gone from the old path")
	trashed, err := f.drv.List(ctx, ops.TrashPrefix)
	require.NoError(t, err)
	require.Len(t, trashed, 1, "exactly one file should now be in the trash dir")
	require.Contains(t, trashed[0].Name, "doomed.txt")

	// DB: node no longer listed at its active path (the bug: it stayed visible)
	active, _ := f.store.GetNodeByPath(ctx, f.st.ID, opsPathHash(f.st.ID, "doomed.txt"))
	require.Nil(t, active, "deleted file must NOT still appear in the active listing")
}

func TestOpsWorker_Copy_InsertsDBNode(t *testing.T) {
	ctx := context.Background()
	f := newOpsFixture(t)
	if _, ok := storage.Driver(f.drv).(storage.Copier); !ok {
		t.Skip("local driver does not implement Copier")
	}
	f.seedDir(t, "dest")
	f.seedFile(t, "orig.txt", "data")

	op := f.runOp(t, ops.OpCopy, []string{"orig.txt"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "copy op error: %s", op.Error)

	// driver: both copies exist
	_, err := f.drv.Stat(ctx, "orig.txt")
	require.NoError(t, err)
	_, err = f.drv.Stat(ctx, "dest/orig.txt")
	require.NoError(t, err)

	// DB: a node now exists for the copy (the bug: copy never appeared)
	cp, err := f.store.GetNodeByPath(ctx, f.st.ID, opsPathHash(f.st.ID, "dest/orig.txt"))
	require.NoError(t, err)
	require.NotNil(t, cp, "DB node must exist for the copied file")
}

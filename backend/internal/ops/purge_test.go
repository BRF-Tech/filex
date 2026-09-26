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
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// A permanent delete of one trash entry as a job of the queue.
//
// ⚠ It ran inside DELETE /api/admin/trash/{id}. A folder is purged one object
// and one row at a time, and the admin page's HTTP client gives up after 30 s:
// the page said "the server could not be reached" beside a raw
// "AxiosError: timeout of 30000ms exceeded" while the purge went on.

// withPurger wires the trash handler as the queue's Purger, the way routes.go
// does.
func (f *opsFixture) withPurger() {
	resolve := func(int64) (storage.Driver, error) { return f.drv, nil }
	f.svc.SetPurger(handlers.NewTrash(trash.New(f.store, resolve, nil), f.store))
}

func TestOpsWorker_Purge_TakesAFolderOutOfTheTrashForGood(t *testing.T) {
	f := newOpsFixture(t)
	f.withPurger()
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	ids := f.trashAll(t, "Leon")

	op := f.runOp(t, ops.OpPurge, ids, "")
	require.Equal(t, ops.StatusOK, op.Status, "purge op error: %s", op.Error)

	id, _ := strconv.ParseInt(ids[0], 10, 64)
	n, err := f.store.GetNode(context.Background(), id)
	assert.True(t, err != nil || n == nil, "the trash entry is still there")
	objs, _ := f.drv.List(context.Background(), trash.Prefix)
	assert.Empty(t, objs, "bytes were left in the trash")
}

func TestOpsWorker_Purge_WithNoPurgerWiredFails(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "a.txt", "a")
	ids := f.trashAll(t, "a.txt")

	op := f.runOp(t, ops.OpPurge, ids, "")
	assert.Equal(t, ops.StatusFailed, op.Status)
}

func TestSubmit_APurgeNamesTrashEntriesByID(t *testing.T) {
	f := newOpsFixture(t)
	ctx := context.Background()
	for _, bad := range [][]string{{"a.txt"}, {"0"}, {"12", "x"}} {
		_, err := f.svc.Submit(ctx, ops.OpPurge, f.st.ID, bad, "")
		refused(t, err, strings.Join(bad, ","))
	}
}

// slowDeletes is the local driver with every Delete held for a moment, so a
// purge can be caught while it runs.
type slowDeletes struct {
	*local.Driver
	hold time.Duration
}

func (d *slowDeletes) Delete(ctx context.Context, p string) error {
	time.Sleep(d.hold)
	return d.Driver.Delete(ctx, p)
}

// A running purge is not cancelled half-way either: a folder stopped between
// two objects is half gone, and its row still in the trash.
func TestOpsWorker_ARunningPurgeIsNotStoppedHalfWay(t *testing.T) {
	f := newOpsFixture(t)
	slow := &slowDeletes{Driver: f.drv, hold: 300 * time.Millisecond}
	resolve := func(int64) (storage.Driver, error) { return slow, nil }
	f.svc.SetPurger(handlers.NewTrash(trash.New(f.store, resolve, nil), f.store))
	f.seedFile(t, "a.txt", "a")
	ids := f.trashAll(t, "a.txt")
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpPurge, f.st.ID, ids, "")
	require.NoError(t, err)
	assert.True(t, op.Cancellable, "a purge waiting in the queue can still be cancelled")

	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	deadline := time.Now().Add(3 * time.Second)
	for {
		cur, err := f.svc.Get(ctx, op.ID)
		require.NoError(t, err)
		if cur.Status == ops.StatusRunning {
			assert.False(t, cur.Cancellable, "a running purge is advertised as cancellable")
			break
		}
		require.True(t, time.Now().Before(deadline), "the purge never started (status %s)", cur.Status)
		time.Sleep(5 * time.Millisecond)
	}
	ok, err := f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	assert.False(t, ok, "a running purge was cancelled")

	done := waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "purge op error: %s", done.Error)
}

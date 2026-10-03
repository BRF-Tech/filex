package ops_test

// "Repair thumbnails" as an ops job (ops/thumb_repair.go), with the walk
// replaced by a fake: what the queue owes it — a row of its own, progress,
// the result, one run per tenant, cancel, its tenant's alone, the reach it
// was asked with, and a restart that carries it on.

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type fakeRepairer struct {
	mu      sync.Mutex
	jobs    []ops.ThumbRepairJob
	total   int
	result  ops.ThumbRepairCounts
	err     error
	hold    chan struct{} // closed to let a run finish; nil: finish at once
	started chan struct{}
	once    sync.Once
}

func (f *fakeRepairer) CountRepair(_ context.Context, job ops.ThumbRepairJob) (int, error) {
	return f.total, nil
}

func (f *fakeRepairer) RunRepair(ctx context.Context, job ops.ThumbRepairJob, progress func(ops.ThumbRepairCounts)) (ops.ThumbRepairCounts, error) {
	f.mu.Lock()
	f.jobs = append(f.jobs, job)
	f.mu.Unlock()
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	progress(ops.ThumbRepairCounts{Processed: 1, OK: 1})
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return ops.ThumbRepairCounts{Processed: 1, OK: 1}, ctx.Err()
		}
	}
	return f.result, f.err
}

func newRepairSvc(t *testing.T, r *fakeRepairer) (*ops.Service, *sql.DB) {
	t.Helper()
	conn, _ := testutil.NewTestDB(t)
	svc := ops.New(conn, nil)
	require.NoError(t, svc.Migrate(context.Background()))
	svc.SetThumbRepairer(r)
	t.Cleanup(svc.Stop)
	return svc, conn
}

func TestThumbRepair_RunsAndReportsItsResult(t *testing.T) {
	r := &fakeRepairer{total: 10, result: ops.ThumbRepairCounts{Processed: 10, OK: 7, Failed: 1, Skipped: 2}}
	svc, _ := newRepairSvc(t, r)
	op, err := svc.SubmitThumbRepair(context.Background(), ops.ThumbRepairRequest{
		Job: ops.ThumbRepairJob{StorageID: 3, Path: "/Tatil", Mode: ops.ThumbRepairFix},
	})
	require.NoError(t, err)
	assert.Equal(t, ops.OpThumbRepair, op.Kind)
	end := waitDone(t, svc, op.ID)
	assert.Equal(t, ops.StatusPartial, end.Status, "one failure among successes is partial")
	assert.Equal(t, 10, end.Total)
	assert.Equal(t, 10, end.Done)
	assert.Equal(t, 1, end.Failed)
	assert.Equal(t, 2, end.Skipped)
	job, refused, _, ok := end.ThumbRepairOf()
	require.True(t, ok)
	assert.Equal(t, int64(3), job.StorageID)
	assert.Equal(t, "/Tatil", job.Path)
	assert.Equal(t, ops.ThumbRepairFix, job.Mode)
	assert.Empty(t, refused)
	assert.Empty(t, end.Sources, "the row's request is not shown as sources")
}

// A storage whose catalogue cannot hold its files is refused and the refusal
// is part of the result.
func TestThumbRepair_RecordsRefusals(t *testing.T) {
	r := &fakeRepairer{result: ops.ThumbRepairCounts{Refused: []ops.ThumbRefusal{{StorageID: 4, Code: "never_synced"}}}}
	svc, _ := newRepairSvc(t, r)
	op, err := svc.SubmitThumbRepair(context.Background(), ops.ThumbRepairRequest{Job: ops.ThumbRepairJob{Mode: ops.ThumbRepairFix}})
	require.NoError(t, err)
	end := waitDone(t, svc, op.ID)
	assert.Equal(t, ops.StatusFailed, end.Status, "nothing repaired, one storage refused")
	_, refused, _, _ := end.ThumbRepairOf()
	assert.Equal(t, []ops.ThumbRefusal{{StorageID: 4, Code: "never_synced"}}, refused)
}

func TestThumbRepair_OnePerTenant(t *testing.T) {
	r := &fakeRepairer{hold: make(chan struct{}), started: make(chan struct{})}
	svc, _ := newRepairSvc(t, r)
	defer close(r.hold)
	a := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 7, StorageIDs: []int64{1}})
	b := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 8, StorageIDs: []int64{2}})
	first, err := svc.SubmitThumbRepair(a, ops.ThumbRepairRequest{Job: ops.ThumbRepairJob{Mode: ops.ThumbRepairFix, Reach: []int64{1}}, Tenant: ops.TenantKey(a)})
	require.NoError(t, err)
	waitClosed(t, r.started, "the first run")

	again, err := svc.SubmitThumbRepair(a, ops.ThumbRepairRequest{Job: ops.ThumbRepairJob{Mode: ops.ThumbRepairRebuild}, Tenant: ops.TenantKey(a)})
	require.ErrorIs(t, err, ops.ErrThumbRepairBusy)
	assert.Equal(t, first.ID, again.ID, "the busy answer is the run already going")

	other, err := svc.SubmitThumbRepair(b, ops.ThumbRepairRequest{Job: ops.ThumbRepairJob{Mode: ops.ThumbRepairFix, Reach: []int64{2}}, Tenant: ops.TenantKey(b)})
	require.NoError(t, err, "another tenant is not refused")
	assert.NotEqual(t, first.ID, other.ID)

	assert.True(t, ops.ViewerOf(a).Sees(first), "its tenant sees it")
	assert.False(t, ops.ViewerOf(b).Sees(first), "another tenant does not")
	latest, err := svc.LatestThumbRepair(context.Background(), ops.TenantKey(b))
	require.NoError(t, err)
	assert.Equal(t, other.ID, latest.ID)
	job, _, _, _ := first.ThumbRepairOf()
	assert.Equal(t, []int64{1}, job.Reach, "the reach it was asked with travels with the row")
}

func TestThumbRepair_Cancel(t *testing.T) {
	r := &fakeRepairer{hold: make(chan struct{}), started: make(chan struct{})}
	svc, _ := newRepairSvc(t, r)
	defer close(r.hold)
	op, err := svc.SubmitThumbRepair(context.Background(), ops.ThumbRepairRequest{Job: ops.ThumbRepairJob{Mode: ops.ThumbRepairFix}})
	require.NoError(t, err)
	waitClosed(t, r.started, "the run")
	cur, err := svc.Get(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, cur.Cancellable)
	ok, err := svc.Cancel(context.Background(), op.ID)
	require.NoError(t, err)
	require.True(t, ok)
	end := waitDone(t, svc, op.ID)
	assert.Equal(t, ops.StatusCancelled, end.Status)
}

// The queue's own worker never takes a repair: it would hold every copy and
// move for as long as the repair runs.
func TestThumbRepair_NotTakenByTheWorker(t *testing.T) {
	r := &fakeRepairer{}
	conn, _ := testutil.NewTestDB(t)
	svc := ops.New(conn, nil)
	require.NoError(t, svc.Migrate(context.Background()))
	t.Cleanup(svc.Stop)
	res, err := conn.Exec(`INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status)
		VALUES ('thumb-repair', 0, 0, '["mode=fix","path="]', '', 0, 'pending')`)
	require.NoError(t, err)
	id, _ := res.LastInsertId()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx) // no repairer wired: nothing may run it
	time.Sleep(300 * time.Millisecond)
	op, err := svc.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusPending, op.Status, "the worker claimed a thumbnail repair")
	_ = r
}

// A run the server stopped under carries on at the next boot.
func TestThumbRepair_ResumesAfterARestart(t *testing.T) {
	r := &fakeRepairer{hold: make(chan struct{}), started: make(chan struct{})}
	svc, conn := newRepairSvc(t, r)
	op, err := svc.SubmitThumbRepair(context.Background(), ops.ThumbRepairRequest{Job: ops.ThumbRepairJob{StorageID: 2, Mode: ops.ThumbRepairRebuild}})
	require.NoError(t, err)
	waitClosed(t, r.started, "the run")
	svc.Stop()
	close(r.hold)

	next := ops.New(conn, nil)
	require.NoError(t, next.Migrate(context.Background()))
	again := &fakeRepairer{result: ops.ThumbRepairCounts{Processed: 3, OK: 3}}
	next.SetThumbRepairer(again)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(next.Stop)
	t.Cleanup(cancel)
	go next.Run(ctx)
	end := waitDone(t, next, op.ID)
	assert.Equal(t, ops.StatusOK, end.Status, end.Error)
	require.Len(t, again.jobs, 1)
	assert.Equal(t, ops.ThumbRepairRebuild, again.jobs[0].Mode, "the resumed run is the run that was asked for")
	assert.Equal(t, int64(2), again.jobs[0].StorageID)
}

// A row whose request cannot be read reaches nothing.
func TestThumbRepair_AnUnreadableRowReachesNothing(t *testing.T) {
	r := &fakeRepairer{}
	svc, conn := newRepairSvc(t, r)
	res, err := conn.Exec(`INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status)
		VALUES ('thumb-repair', 0, 0, '["mode=everything"]', '', 0, 'pending')`)
	require.NoError(t, err)
	id, _ := res.LastInsertId()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)
	end := waitDone(t, svc, id)
	require.Len(t, r.jobs, 1)
	assert.Equal(t, []int64{}, r.jobs[0].Reach, "an unreadable ask reaches no storage")
	_ = end
}

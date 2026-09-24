package ops_test

// "Empty the trash now" as an ops job (ops/trash_empty.go): a row of the
// queue like every long operation — seen by its tenant only, cancellable,
// never holding the worker, resumed after a restart — around the trash
// service's purge.

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// heldStore holds every row purge after the first `open` until release.
type heldStore struct {
	db.Store
	open    int
	mu      sync.Mutex
	passed  int
	once    sync.Once
	reached chan struct{}
	release chan struct{}
	free    sync.Once
}

func (h *heldStore) HardDeleteNode(ctx context.Context, id int64) error {
	h.mu.Lock()
	h.passed++
	n := h.passed
	h.mu.Unlock()
	if n > h.open {
		h.once.Do(func() { close(h.reached) })
		<-h.release
	}
	return h.Store.HardDeleteNode(ctx, id)
}

func (h *heldStore) let() { h.free.Do(func() { close(h.release) }) }

type trashFix struct {
	conn  *sql.DB
	store db.Store
	held  *heldStore
	svc   *ops.Service
	sid   int64
}

func newTrashFix(t *testing.T, open int) *trashFix {
	t.Helper()
	conn, store := testutil.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	held := &heldStore{Store: store, open: open, reached: make(chan struct{}), release: make(chan struct{})}
	svc := ops.New(conn, nil)
	require.NoError(t, svc.Migrate(context.Background()))
	svc.SetTrashEmptier(trash.New(held, nil, nil))
	t.Cleanup(svc.Stop)
	t.Cleanup(held.let) // runs first: a held purge must not keep Stop waiting
	return &trashFix{conn: conn, store: store, held: held, svc: svc, sid: st.ID}
}

func (f *trashFix) trash(t *testing.T, sid int64, name string) int64 {
	t.Helper()
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: sid, Name: name, Path: "/" + name,
		PathHash: pathkey.Hash(sid, "/"+name), Type: model.NodeTypeFile, Size: 5,
	})
	require.NoError(t, err)
	require.NoError(t, f.store.SoftDeleteNode(context.Background(), n.ID))
	return n.ID
}

func (f *trashFix) trashed(t *testing.T, sid int64) int {
	t.Helper()
	var n int
	require.NoError(t, f.conn.QueryRow(
		`SELECT COUNT(*) FROM nodes WHERE storage_id = ? AND deleted_at IS NOT NULL`, sid).Scan(&n))
	return n
}

func (f *trashFix) otherStorage(t *testing.T) int64 {
	t.Helper()
	st, err := f.store.CreateStorage(context.Background(), &model.Storage{
		Name: "other", Driver: "local", MountPath: "other", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	return st.ID
}

func waitDone(t *testing.T, svc *ops.Service, id int64) *ops.Op {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := svc.Get(context.Background(), id)
		require.NoError(t, err)
		if op.Status != ops.StatusPending && op.Status != ops.StatusRunning {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("op %d never finished", id)
	return nil
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// The ordinary case, end to end: the row counts what it will purge, runs
// without the worker (Run is never called here), and ends ok.
func TestTrashEmpty_RunsAsAnOpOfItsOwn(t *testing.T) {
	f := newTrashFix(t, 1000)
	for i := 0; i < 3; i++ {
		f.trash(t, f.sid, fmt.Sprintf("a-%d.txt", i))
	}
	op, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{})
	require.NoError(t, err)
	assert.Equal(t, ops.OpTrashEmpty, op.Kind)
	assert.Equal(t, 3, op.Total, "counted when it was asked for")
	assert.Empty(t, op.Sources, "its request is not shown as paths")
	assert.Empty(t, op.Dest)

	end := waitDone(t, f.svc, op.ID)
	assert.Equal(t, ops.StatusOK, end.Status)
	assert.Equal(t, 3, end.Done)
	assert.Zero(t, end.Failed)
	assert.Zero(t, f.trashed(t, f.sid))
	assert.Equal(t, int64(15), end.BytesDone, "bytes freed stay readable once it has ended")
}

// ⚠⚠ The queue's single worker runs copies, moves, deletes and upload
// commits one after another. A purge that took it for an hour would hold all
// of them for an hour; a trash empty never goes through it.
func TestTrashEmpty_DoesNotHoldTheWorker(t *testing.T) {
	f := newTrashFix(t, 0)
	f.trash(t, f.sid, "slow.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.svc.Run(ctx)

	op, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{})
	require.NoError(t, err)
	waitClosed(t, f.held.reached, "the purge to be under way")

	// A delete queued behind it still runs (it fails — no driver here — but
	// it RUNS, which is the point).
	del, err := f.svc.Submit(context.Background(), ops.OpDelete, f.sid, []string{"x.txt"}, "")
	require.NoError(t, err)
	got := waitDone(t, f.svc, del.ID)
	assert.NotEqual(t, ops.StatusPending, got.Status, "the worker was free")
	cur, err := f.svc.Get(context.Background(), op.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusRunning, cur.Status, "while the purge is still going")
}

// A tenant's second press is told about its first; another tenant is not
// refused — it is queued behind it, and runs when its turn comes.
func TestTrashEmpty_OnePerTenantAndTheRestWaitTheirTurn(t *testing.T) {
	f := newTrashFix(t, 0)
	other := f.otherStorage(t)
	f.trash(t, f.sid, "ours.txt")
	f.trash(t, other, "theirs.txt")
	ours := ops.TrashEmptyRequest{Reach: []int64{f.sid}, Tenant: "tenant:7"}
	theirs := ops.TrashEmptyRequest{Reach: []int64{other}, Tenant: "tenant:8"}

	first, err := f.svc.SubmitTrashEmpty(context.Background(), ours)
	require.NoError(t, err)
	waitClosed(t, f.held.reached, "the first purge")

	again, err := f.svc.SubmitTrashEmpty(context.Background(), ours)
	require.ErrorIs(t, err, ops.ErrTrashBusy)
	assert.Equal(t, first.ID, again.ID, "the busy answer is the run already going")

	queued, err := f.svc.SubmitTrashEmpty(context.Background(), theirs)
	require.NoError(t, err, "another tenant is not refused")
	time.Sleep(100 * time.Millisecond)
	cur, err := f.svc.Get(context.Background(), queued.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusPending, cur.Status, "it waits its turn")

	f.held.let()
	assert.Equal(t, ops.StatusOK, waitDone(t, f.svc, first.ID).Status)
	assert.Equal(t, ops.StatusOK, waitDone(t, f.svc, queued.ID).Status)
	assert.Zero(t, f.trashed(t, f.sid))
	assert.Zero(t, f.trashed(t, other))
}

// Cancel is the queue's cancel: a running purge stops at the next row and
// what it did not reach stays in the trash; a queued one never starts.
func TestTrashEmpty_Cancel(t *testing.T) {
	f := newTrashFix(t, 1)
	other := f.otherStorage(t)
	for i := 0; i < 4; i++ {
		f.trash(t, f.sid, fmt.Sprintf("r-%d.txt", i))
	}
	f.trash(t, other, "q.txt")
	running, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{Tenant: "tenant:1", Reach: []int64{f.sid}})
	require.NoError(t, err)
	waitClosed(t, f.held.reached, "the second purge")
	queued, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{Tenant: "tenant:2", Reach: []int64{other}})
	require.NoError(t, err)

	ok, err := f.svc.Cancel(context.Background(), queued.ID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.svc.Cancel(context.Background(), running.ID)
	require.NoError(t, err)
	require.True(t, ok)
	// The row in hand when the cancel landed is finished, not abandoned
	// half-way (its bytes gone, its row still offered for restore).
	f.held.let()

	r := waitDone(t, f.svc, running.ID)
	assert.Equal(t, ops.StatusCancelled, r.Status)
	assert.Equal(t, 2, r.Done, "the rows it had started before it was stopped")
	assert.Zero(t, r.Failed, "the row in hand was finished, not failed")
	assert.Equal(t, 2, f.trashed(t, f.sid), "the rest is still in the trash")
	q := waitDone(t, f.svc, queued.ID)
	assert.Equal(t, ops.StatusCancelled, q.Status)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, f.trashed(t, other), "a run cancelled while it waited never purged")
}

// A restart does not forget a run: the row is requeued at boot and the purge
// carries on with what is left — still bounded by the moment it was asked.
func TestTrashEmpty_ResumesAfterARestart(t *testing.T) {
	f := newTrashFix(t, 1)
	for i := 0; i < 3; i++ {
		f.trash(t, f.sid, fmt.Sprintf("r-%d.txt", i))
	}
	op, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{})
	require.NoError(t, err)
	waitClosed(t, f.held.reached, "the second purge")
	go func() { time.Sleep(100 * time.Millisecond); f.held.let() }()
	f.svc.Stop() // the process goes away mid-run; the row in hand finishes
	cur, err := f.svc.Get(context.Background(), op.ID)
	require.NoError(t, err)
	require.Equal(t, ops.StatusRunning, cur.Status, "a stopping server leaves the row for the next boot")

	// Deleted after the ask: the resumed run must leave it alone. (SQLite's
	// CURRENT_TIMESTAMP has one-second resolution: step past the ask's second.)
	time.Sleep(1100 * time.Millisecond)
	late := f.trash(t, f.sid, "late.txt")

	next := ops.New(f.conn, nil)
	require.NoError(t, next.Migrate(context.Background()))
	next.SetTrashEmptier(trash.New(f.store, nil, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(next.Stop)
	t.Cleanup(cancel)
	go next.Run(ctx)

	end := waitDone(t, next, op.ID)
	assert.Equal(t, ops.StatusOK, end.Status, end.Error)
	assert.Equal(t, 3, end.Done, "the count carries on from before the restart")
	n, err := f.store.GetNode(context.Background(), late)
	require.NoError(t, err, "a file deleted after the ask was purged by the resumed run")
	assert.NotNil(t, n.DeletedAt)
	assert.Equal(t, 1, f.trashed(t, f.sid))
}

// deletedAt moves a trashed row's deleted_at, in the column's own UTC text.
func (f *trashFix) deletedAt(t *testing.T, id int64, at time.Time) {
	t.Helper()
	_, err := f.conn.Exec(`UPDATE nodes SET deleted_at = ? WHERE id = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), id)
	require.NoError(t, err)
}

// A file deleted WHILE a run is under way stays in the trash, even when the
// sweep has yet to read the batch it lands in.
//
// ⚠⚠ Berk Başarır's field report (PR #47, 979309b): on a live instance a
// 1 h 48 min empty reported 61,845 purged of a total of 61,844 — the extra row
// a file a member deleted six minutes before the end, purged at once instead
// of waiting its thirty days. The sweep walks up by id and reads its next
// batch as it goes, so a cutoff that is not the moment of asking meets a
// newly deleted row in a later batch. The trash here is one row more than a
// batch for that reason, and the run is held after its first purge while the
// file is deleted.
func TestTrashEmpty_LeavesWhatIsTrashedWhileItRuns(t *testing.T) {
	f := newTrashFix(t, 1)
	const batchPlusOne = 501
	for i := 0; i < batchPlusOne; i++ {
		id := f.trash(t, f.sid, fmt.Sprintf("old-%d.txt", i))
		f.deletedAt(t, id, time.Now().Add(-time.Hour))
	}
	op, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, batchPlusOne, op.Total)
	waitClosed(t, f.held.reached, "the run to be under way")

	// A member deletes a file while the empty is still going.
	late := f.trash(t, f.sid, "deleted-meanwhile.txt")
	f.deletedAt(t, late, time.Now().Add(2*time.Second))
	f.held.let()

	end := waitDone(t, f.svc, op.ID)
	assert.Equal(t, ops.StatusOK, end.Status, end.Error)
	assert.Equal(t, batchPlusOne, end.Done, "the rows that were in the trash when it was asked for")
	n, err := f.store.GetNode(context.Background(), late)
	require.NoError(t, err, "the file deleted during the run was purged with it")
	assert.NotNil(t, n.DeletedAt, "it waits in the trash like any other")
	assert.Equal(t, 1, f.trashed(t, f.sid))
}

// A run that waits its turn behind another sweep is bounded by the moment it
// was ASKED for, not the moment it gets to run: the confirmation counted the
// trash as it was when the admin pressed the button, and whatever is deleted
// while the run waits is not in that count.
func TestTrashEmpty_AQueuedRunIsBoundedByItsAsk(t *testing.T) {
	f := newTrashFix(t, 0)
	other := f.otherStorage(t)
	f.trash(t, f.sid, "ours.txt")
	f.trash(t, other, "theirs.txt")

	first, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{Reach: []int64{f.sid}, Tenant: "tenant:7"})
	require.NoError(t, err)
	waitClosed(t, f.held.reached, "the first purge")
	queued, err := f.svc.SubmitTrashEmpty(context.Background(), ops.TrashEmptyRequest{Reach: []int64{other}, Tenant: "tenant:8"})
	require.NoError(t, err)
	require.Equal(t, 1, queued.Total)

	// While it waits, tenant 8 deletes another file. The column has
	// one-second resolution on SQLite: step past the ask's second, and past
	// the deletion's before the run gets its turn.
	time.Sleep(1100 * time.Millisecond)
	late := f.trash(t, other, "deleted-while-it-waited.txt")
	time.Sleep(1100 * time.Millisecond)
	cur, err := f.svc.Get(context.Background(), queued.ID)
	require.NoError(t, err)
	require.Equal(t, ops.StatusPending, cur.Status, "it is still waiting its turn")
	f.held.let()

	assert.Equal(t, ops.StatusOK, waitDone(t, f.svc, first.ID).Status)
	q := waitDone(t, f.svc, queued.ID)
	assert.Equal(t, ops.StatusOK, q.Status, q.Error)
	assert.Equal(t, 1, q.Done, "what was in the trash when it was asked for")
	n, err := f.store.GetNode(context.Background(), late)
	require.NoError(t, err, "a file deleted while the run waited was purged by it")
	assert.NotNil(t, n.DeletedAt)
}

// A trash empty is its tenant's: on the list, on its own and for a cancel.
// Another tenant does not see it, even when both name no storage in common.
func TestTrashEmpty_IsItsTenantsOwn(t *testing.T) {
	f := newTrashFix(t, 0)
	f.trash(t, f.sid, "x.txt")
	a := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 7, StorageIDs: []int64{f.sid}})
	b := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 8, StorageIDs: []int64{f.sid + 50}})
	op, err := f.svc.SubmitTrashEmpty(a, ops.TrashEmptyRequest{Reach: trash.Reach(a), Tenant: ops.TenantKey(a)})
	require.NoError(t, err)
	waitClosed(t, f.held.reached, "the purge")

	listed := func(ctx context.Context) bool {
		rows, err := f.svc.ListFor(ctx, "", ops.ViewerOf(ctx))
		require.NoError(t, err)
		for _, r := range rows {
			if r.ID == op.ID {
				return true
			}
		}
		return false
	}
	assert.True(t, listed(a), "its tenant sees it")
	assert.False(t, listed(b), "another tenant does not")
	assert.True(t, listed(context.Background()), "the platform sees every run")
	assert.True(t, ops.ViewerOf(a).Sees(op))
	assert.False(t, ops.ViewerOf(b).Sees(op))

	latest, err := f.svc.LatestTrashEmpty(context.Background(), ops.TenantKey(b))
	require.NoError(t, err)
	assert.Nil(t, latest, "and has no run of its own to follow")
}

// A row whose request cannot be read reaches nothing — the failure mode of a
// purge is too narrow, never too wide.
func TestTrashEmpty_AnUnreadableRowReachesNothing(t *testing.T) {
	f := newTrashFix(t, 1000)
	f.trash(t, f.sid, "kept.txt")
	res, err := f.conn.Exec(`INSERT INTO pending_ops (kind, storage_id, dest_storage_id, sources_json, dest, total, status)
		VALUES ('trash-empty', 0, 0, '["days=zero"]', '', 0, 'pending')`)
	require.NoError(t, err)
	id, _ := res.LastInsertId()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.svc.Run(ctx)
	end := waitDone(t, f.svc, id)
	assert.Equal(t, ops.StatusOK, end.Status)
	assert.Zero(t, end.Done)
	assert.Equal(t, 1, f.trashed(t, f.sid))
}

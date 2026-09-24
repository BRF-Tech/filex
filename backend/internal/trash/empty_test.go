package trash_test

// "Empty the trash now" is a job: what it purges is fixed when it is asked
// for, it takes its turn with every other sweep, and it says how far it got.
//
// ⚠⚠ It ran inside the request. Every row costs a storage delete and a few
// database writes, so a trash of 61,844 files — one sync incident's worth —
// needed the better part of an hour, and nginx gave the request sixty
// seconds: 504, the request's context cancelled, the purge abandoned
// mid-batch. The admin page, whose own HTTP client had given up at thirty,
// showed nothing at all, and the admin pressed the button again — three
// purges racing over the same rows. The run now belongs to the ops queue
// (ops.SubmitTrashEmpty); these tests hold the job underneath it to what the
// button promises: it says how far it has got, there is only ever one sweep
// at a time, and it purges nothing that was deleted after it was asked for.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// gate lets `open` purges through, then holds every further one until
// release is closed. reached is closed when the first purge is held.
type gate struct {
	db.Store
	open    int
	mu      sync.Mutex
	passed  int
	once    sync.Once
	reached chan struct{}
	release chan struct{}
}

func newGate(store db.Store, open int) *gate {
	return &gate{Store: store, open: open, reached: make(chan struct{}), release: make(chan struct{})}
}

func (g *gate) HardDeleteNode(ctx context.Context, id int64) error {
	g.mu.Lock()
	g.passed++
	n := g.passed
	g.mu.Unlock()
	if n > g.open {
		g.once.Do(func() { close(g.reached) })
		<-g.release
	}
	return g.Store.HardDeleteNode(ctx, id)
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func trashFiles(t *testing.T, store db.Store, sid int64, n int, prefix string) {
	t.Helper()
	for i := 0; i < n; i++ {
		trashed(t, store, sid, fmt.Sprintf("%s-%d.txt", prefix, i))
	}
}

// soon is a cutoff after everything a test trashed.
func soon() time.Time { return time.Now().UTC().Add(time.Minute) }

// While it runs it says how far it has got — the ops row draws this instead
// of nothing for an hour.
func TestRunEmpty_ReportsProgressWhileItRuns(t *testing.T) {
	_, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 5, "slow")
	g := newGate(store, 2)
	svc := trash.New(g, nil, nil)

	var mu sync.Mutex
	var last trash.PurgeResult
	done := make(chan trash.PurgeResult, 1)
	go func() {
		res, _ := svc.RunEmpty(context.Background(), trash.EmptyJob{Before: soon()}, nil, func(r trash.PurgeResult) {
			mu.Lock()
			last = r
			mu.Unlock()
		})
		done <- res
	}()
	waitFor(t, g.reached, "the third purge")
	mu.Lock()
	assert.Equal(t, 2, last.Deleted, "two rows reported while the third is held")
	assert.Equal(t, int64(2*7), last.Bytes)
	mu.Unlock()

	close(g.release)
	select {
	case res := <-done:
		assert.Equal(t, 5, res.Deleted)
	case <-time.After(10 * time.Second):
		t.Fatal("the run never finished")
	}
}

// One at a time. The admin who saw nothing pressed the button three times,
// and the three purges ran over the same rows at once — each read the row,
// each deleted it, each released its bytes from the owner's quota. A second
// run waits its turn, and a run withdrawn while it waits stops waiting.
func TestRunEmpty_OneAtATime(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 3, "once")
	g := newGate(store, 0)
	svc := trash.New(g, nil, nil)
	job := trash.EmptyJob{Before: soon()}

	first := make(chan error, 1)
	go func() {
		_, err := svc.RunEmpty(context.Background(), job, nil, nil)
		first <- err
	}()
	waitFor(t, g.reached, "the first purge")

	// A second one asked for meanwhile does not start beside it.
	startedSecond := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		_, err := svc.RunEmpty(context.Background(), job, func() bool { close(startedSecond); return true }, nil)
		second <- err
	}()
	select {
	case <-startedSecond:
		t.Fatal("a second empty started beside the first")
	case <-time.After(200 * time.Millisecond):
	}

	// A third, withdrawn while it waits, stops waiting and purges nothing.
	wctx, withdraw := context.WithCancel(context.Background())
	third := make(chan error, 1)
	go func() {
		_, err := svc.RunEmpty(wctx, job, nil, nil)
		third <- err
	}()
	withdraw()
	select {
	case err := <-third:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("a withdrawn run kept waiting")
	}

	close(g.release)
	require.NoError(t, <-first)
	require.NoError(t, <-second, "once the first has finished, the next one runs")
	assert.Zero(t, trashedCount(t, conn, sid))
}

// A run whose turn comes after it was withdrawn (started declines) purges
// nothing.
func TestRunEmpty_WithdrawnBeforeItsTurnPurgesNothing(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 2, "kept")
	res, err := trash.New(store, nil, nil).RunEmpty(context.Background(), trash.EmptyJob{Before: soon()},
		func() bool { return false }, nil)
	require.ErrorIs(t, err, trash.ErrNotStarted)
	assert.Zero(t, res.Scanned)
	assert.Equal(t, 2, trashedCount(t, conn, sid))
}

// The nightly retention sweep takes its turn too, rather than racing a
// running empty over the rows both of them match.
func TestPurgeExpired_WaitsForARunningEmpty(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	for i := 0; i < 3; i++ {
		trashedAgo(t, conn, store, sid, fmt.Sprintf("old-%d.txt", i), 40*24*time.Hour)
	}
	g := newGate(store, 0)
	svc := trash.New(g, nil, nil)

	emptied := make(chan trash.PurgeResult, 1)
	go func() {
		res, _ := svc.RunEmpty(context.Background(), trash.EmptyJob{Before: soon()}, nil, nil)
		emptied <- res
	}()
	waitFor(t, g.reached, "the empty's first purge")

	nightly := make(chan trash.PurgeResult, 1)
	go func() {
		res, _ := svc.PurgeExpired(context.Background())
		nightly <- res
	}()
	select {
	case <-nightly:
		t.Fatal("the nightly sweep ran while an empty held the trash")
	case <-time.After(200 * time.Millisecond):
	}

	close(g.release)
	assert.Equal(t, 3, (<-emptied).Deleted)
	select {
	case res := <-nightly:
		assert.Zero(t, res.Deleted, "the empty had already purged them")
	case <-time.After(10 * time.Second):
		t.Fatal("the nightly sweep never ran")
	}
}

// The total is the job's own scope, not the whole instance's trash: the same
// rule decides what is counted and what is purged.
func TestTally_IsTheJobsOwnScope(t *testing.T) {
	_, store, sid := sweepFixture(t)
	other := secondStorage(t, store)
	trashFiles(t, store, sid, 3, "named")
	trashFiles(t, store, other, 2, "elsewhere")
	svc := trash.New(store, nil, nil)

	n, b, err := svc.Tally(context.Background(), trash.EmptyJob{Before: soon(), StorageID: sid})
	require.NoError(t, err)
	assert.Equal(t, 3, n, "narrowed to one storage")
	assert.Equal(t, int64(3*7), b)

	n, _, err = svc.Tally(context.Background(), trash.EmptyJob{Before: soon(), Reach: []int64{other}})
	require.NoError(t, err)
	assert.Equal(t, 2, n, "a tenant's total is its own storages")

	n, _, err = svc.Tally(context.Background(), trash.EmptyJob{Before: soon(), StorageID: sid, Reach: []int64{other}})
	require.NoError(t, err)
	assert.Zero(t, n, "a storage outside the reach is not counted even when named")

	n, _, err = svc.Tally(context.Background(), trash.EmptyJob{Before: soon(), Reach: []int64{}})
	require.NoError(t, err)
	assert.Zero(t, n, "a reach of no storage is nothing, never everything")

	res, err := svc.RunEmpty(context.Background(), trash.EmptyJob{Before: soon(), StorageID: sid, Reach: []int64{other}}, nil, nil)
	require.NoError(t, err)
	assert.Zero(t, res.Scanned, "and not purged either")
}

// What was deleted AFTER the empty was asked for stays in the trash.
//
// ⚠⚠ The cutoff used to be "now + 24 hours", computed when the purge began,
// which was harmless while the purge lived inside a request of a few seconds.
// A background run takes as long as the trash does, and that cutoff purged —
// for good — a file somebody deleted by mistake while it ran: never counted by
// the confirmation, still believed to be in the trash.
func TestRunEmpty_LeavesWhatWasDeletedAfterItWasAskedFor(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	before := trashedAgo(t, conn, store, sid, "before.txt", time.Hour)
	asked := time.Now()
	after := trashedAgo(t, conn, store, sid, "after.txt", -time.Hour) // deleted later than the ask

	res, err := trash.New(store, nil, nil).RunEmpty(context.Background(),
		trash.EmptyJob{Before: trash.EmptyCutoff(asked, 0)}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted)
	_, err = store.GetNode(context.Background(), before)
	assert.Error(t, err, "what was in the trash when it was asked for is purged")
	n, err := store.GetNode(context.Background(), after)
	require.NoError(t, err, "what was deleted after the ask survives")
	assert.NotNil(t, n.DeletedAt)
}

// A file deleted WHILE a run is under way stays in the trash, even when the
// sweep has yet to read the batch it lands in.
//
// ⚠⚠ Berk Başarır's field report (PR #47, 979309b): on a live instance a
// 1 h 48 min empty reported 61,845 purged of a total of 61,844 — the extra row
// a file a member deleted six minutes before the end, purged at once instead
// of waiting its thirty days. The sweep walks up by id and reads its next
// batch as it goes, so a moving cutoff meets a newly deleted row in a later
// batch. The trash here is more than one batch for that reason, and the run is
// held after its first purge while the file is deleted.
func TestRunEmpty_LeavesWhatIsTrashedWhileItRuns(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	for i := 0; i < fullBatch; i++ {
		trashedAgo(t, conn, store, sid, fmt.Sprintf("old-%d.txt", i), time.Hour)
	}
	g := newGate(store, 1)
	asked := time.Now()

	var res trash.PurgeResult
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, runErr = trash.New(g, nil, nil).RunEmpty(context.Background(),
			trash.EmptyJob{Before: trash.EmptyCutoff(asked, 0)}, nil, nil)
	}()
	waitFor(t, g.reached, "the run to be under way")

	// A member deletes a file while the empty is still going.
	late := trashed(t, store, sid, "deleted-meanwhile.txt")
	_, err := conn.Exec(`UPDATE nodes SET deleted_at = ? WHERE id = ?`,
		time.Now().UTC().Add(2*time.Second).Format("2006-01-02 15:04:05"), late)
	require.NoError(t, err)

	close(g.release)
	waitFor(t, done, "the run")
	require.NoError(t, runErr)
	assert.Equal(t, fullBatch, res.Deleted, "the rows that were in the trash when it was asked for")
	n, err := store.GetNode(context.Background(), late)
	require.NoError(t, err, "the file deleted during the run was purged with it")
	assert.NotNil(t, n.DeletedAt, "it waits in the trash like any other")
}

// Reach is the request's tenant, or nil for everybody who may reach every
// storage — and never nil for a tenant with no storage.
func TestReach(t *testing.T) {
	assert.Nil(t, trash.Reach(context.Background()), "single-tenant: every storage")
	assert.Nil(t, trash.Reach(tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 1, IsSupertenant: true})))
	assert.Equal(t, []int64{4, 9}, trash.Reach(tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 7, StorageIDs: []int64{4, 9}})))
	none := trash.Reach(tenant.WithScope(context.Background(), tenant.DenyAll))
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

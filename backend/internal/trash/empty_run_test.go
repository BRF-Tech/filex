package trash_test

// "Empty the trash now" runs in the background.
//
// ⚠⚠ It ran inside the request. Every row costs a storage delete and a few
// database writes, so a trash of 61,844 files — one sync incident's worth —
// needed the better part of an hour, and nginx gave the request sixty
// seconds: 504, the request's context cancelled, the purge abandoned
// mid-batch. The admin page, whose own HTTP client had given up at thirty,
// showed nothing at all, and the admin pressed the button again — three
// purges racing over the same rows. These tests hold the run to what the
// button promises: it finishes whatever happens to the request, it says how
// far it has got, and there is only ever one.

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

// The reported bug, as a test: the request goes away — a proxy timeout, a
// closed tab — the moment the purge has started, and the purge finishes.
func TestStartEmpty_OutlivesTheRequest(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 5, "gone")
	g := newGate(store, 1)

	req, cancel := context.WithCancel(context.Background())
	run, err := trash.New(g, nil, nil).StartEmpty(req, 0, 0)
	require.NoError(t, err)
	waitFor(t, g.reached, "the run to be under way")
	cancel()
	close(g.release)
	waitFor(t, run.Done(), "the run to finish")

	st := run.Status()
	assert.False(t, st.Running)
	assert.Empty(t, st.Error, "a cancelled request does not cancel the run")
	assert.Equal(t, 5, st.Purged)
	assert.Equal(t, 5, st.Total)
	assert.NotNil(t, st.FinishedAt)
	assert.Zero(t, trashedCount(t, conn, sid))
}

// While it runs it says how far it has got, against a total counted when it
// started — the page has something to draw instead of nothing for an hour.
func TestStartEmpty_ReportsProgressWhileItRuns(t *testing.T) {
	_, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 5, "slow")
	g := newGate(store, 2)

	run, err := trash.New(g, nil, nil).StartEmpty(context.Background(), 0, 0)
	require.NoError(t, err)
	defer func() {
		close(g.release)
		waitFor(t, run.Done(), "the run")
	}()
	waitFor(t, g.reached, "the third purge")

	st := run.Status()
	assert.True(t, st.Running)
	assert.Equal(t, 5, st.Total)
	assert.Equal(t, int64(5*7), st.TotalBytes)
	assert.Equal(t, 2, st.Purged)
	assert.False(t, st.StartedAt.IsZero())
	assert.Nil(t, st.FinishedAt)
}

// One at a time. The admin who saw nothing pressed the button three times,
// and the three purges ran over the same rows at once — each read the row,
// each deleted it, each released its bytes from the owner's quota.
func TestStartEmpty_OneAtATime(t *testing.T) {
	_, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 3, "once")
	g := newGate(store, 0)
	svc := trash.New(g, nil, nil)

	first, err := svc.StartEmpty(context.Background(), 0, 0)
	require.NoError(t, err)
	waitFor(t, g.reached, "the first purge")

	_, err = svc.StartEmpty(context.Background(), 0, 0)
	require.ErrorIs(t, err, trash.ErrBusy, "a second empty must not start beside the first")

	close(g.release)
	waitFor(t, first.Done(), "the first run")
	next, err := svc.StartEmpty(context.Background(), 0, 0)
	require.NoError(t, err, "once the first has finished, the next one may start")
	waitFor(t, next.Done(), "the next run")
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

	run, err := svc.StartEmpty(context.Background(), 0, 0)
	require.NoError(t, err)
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
	waitFor(t, run.Done(), "the empty")
	select {
	case res := <-nightly:
		assert.Zero(t, res.Deleted, "the empty had already purged them")
	case <-time.After(10 * time.Second):
		t.Fatal("the nightly sweep never ran")
	}
	assert.Equal(t, 3, run.Status().Purged)
}

// A run belongs to the tenant that started it: another tenant's admin sees
// neither its progress nor its counts.
func TestLastEmpty_BelongsToTheTenantThatStartedIt(t *testing.T) {
	_, store, sid := sweepFixture(t)
	trashFiles(t, store, sid, 2, "ours")
	svc := trash.New(store, nil, nil)
	ours := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 7, StorageIDs: []int64{sid}})
	theirs := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 8, StorageIDs: []int64{sid + 100}})

	_, ok := svc.LastEmpty(ours)
	assert.False(t, ok, "no run yet")

	run, err := svc.StartEmpty(ours, 0, 0)
	require.NoError(t, err)
	waitFor(t, run.Done(), "the run")

	got, ok := svc.LastEmpty(ours)
	require.True(t, ok)
	assert.Equal(t, 2, got.Status().Purged, "the finished run stays readable to its tenant")

	_, ok = svc.LastEmpty(theirs)
	assert.False(t, ok, "another tenant's admin must not see this run")
	_, ok = svc.LastEmpty(context.Background())
	assert.False(t, ok, "nor does the unscoped view")
}

// The total is the run's own scope, not the whole instance's trash: the same
// filters decide what is counted and what is purged.
func TestStartEmpty_TotalIsTheRunsOwnScope(t *testing.T) {
	_, store, sid := sweepFixture(t)
	other := secondStorage(t, store)
	trashFiles(t, store, sid, 3, "named")
	trashFiles(t, store, other, 2, "elsewhere")
	svc := trash.New(store, nil, nil)

	narrowed, err := svc.StartEmpty(context.Background(), 0, sid)
	require.NoError(t, err)
	waitFor(t, narrowed.Done(), "the narrowed run")
	assert.Equal(t, 3, narrowed.Status().Total, "narrowed to one storage")
	assert.Equal(t, 3, narrowed.Status().Purged)

	scoped := tenant.WithScope(context.Background(), &tenant.Scope{ProviderID: 9, StorageIDs: []int64{other}})
	tenants, err := svc.StartEmpty(scoped, 0, 0)
	require.NoError(t, err)
	waitFor(t, tenants.Done(), "the tenant's run")
	assert.Equal(t, 2, tenants.Status().Total, "a tenant's total is its own storages")
	assert.Equal(t, 2, tenants.Status().Purged)
}

package trash_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Emptying ONE storage's trash spun forever.
//
// ⚠⚠ The purge reads the oldest expired rows 500 at a time and skipped, in Go,
// the rows of every other storage (and, for a tenant, of every storage outside
// its scope). A skipped row is never removed, so when the 500 oldest rows all
// belonged to somebody else the next read returned the same 500, and the next,
// forever. Measured before the fix: 500 older rows on another storage and one
// on the named one — the request ran until its context was cancelled, purged
// nothing, and the named storage's row was still there.

// olderElsewhere puts n trashed rows on another storage, each deleted before
// anything the test trashes afterwards.
func olderElsewhere(t *testing.T, store db.Store, n int) int64 {
	t.Helper()
	other := secondStorage(t, store)
	for i := 0; i < n; i++ {
		trashed(t, store, other, fmt.Sprintf("older-%d.txt", i))
	}
	// deleted_at has second resolution; make the rows below strictly newer.
	time.Sleep(1100 * time.Millisecond)
	return other
}

func TestEmptyOlderThan_OneStorage_BehindAFullBatchOfOthers(t *testing.T) {
	store, svc, _, sid := reclaimFixture(t)
	other := olderElsewhere(t, store, 500)
	mine := trashed(t, store, sid, "mine.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := svc.EmptyOlderThan(ctx, 0, sid)
	require.NoError(t, err, "the purge never got past the other storage's rows")
	assert.Equal(t, 1, res.Deleted)

	_, err = store.GetNode(context.Background(), mine)
	assert.Error(t, err, "the named storage's row is purged")
	left, total, err := store.ListTrashed(context.Background(), &other, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 500, total, "the other storage's trash is untouched")
	assert.Len(t, left, 10)
}

// The same shape reached through a tenant: "empty my trash" from a tenant
// admin, while another tenant has 500 older trashed rows.
func TestEmptyOlderThan_TenantScope_BehindAFullBatchOfOthers(t *testing.T) {
	store, svc, _, sid := reclaimFixture(t)
	olderElsewhere(t, store, 500)
	mine := trashed(t, store, sid, "mine.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = tenant.WithScope(ctx, &tenant.Scope{ProviderID: 7, StorageIDs: []int64{sid}})
	res, err := svc.EmptyOlderThan(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted)
	_, err = store.GetNode(context.Background(), mine)
	assert.Error(t, err)
}

// failingPurge refuses every row deletion, so no purge can make progress.
type failingPurge struct{ db.Store }

func (failingPurge) HardDeleteNode(context.Context, int64) error {
	return errors.New("database is read-only")
}

// A batch in which not one row could be purged ends the run instead of reading
// the same batch again.
func TestPurge_ABatchThatMakesNoProgressStops(t *testing.T) {
	store, _, _, sid := reclaimFixture(t)
	for i := 0; i < 500; i++ {
		trashed(t, store, sid, fmt.Sprintf("stuck-%d.txt", i))
	}
	svc := trash.New(failingPurge{store}, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := svc.EmptyOlderThan(ctx, 0, sid)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Deleted)
	assert.Equal(t, 500, res.Failed, "each row is tried once, not over and over")
}

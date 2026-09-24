package trash_test

// The sweep behind "empty the trash" and the nightly retention purge.
//
// ⚠⚠ It asked for "the oldest 500 expired rows" on every pass, and the only
// rows that ever left that window were the ones it purged. A row it passed
// over — another storage's while the caller had narrowed to one, another
// tenant's, or one whose purge failed — was handed back on the next pass, and
// the pass after that. Once 500 of them sat at the head of the trash, every
// pass was the same 500 rows, nothing was purged, and the loop never ended:
// inside a request until the proxy gave up, inside the nightly worker forever.
//
// Each test below puts one more than a full batch of such rows in front of the
// rows that should go, and runs under a deadline so the old loop fails instead
// of hanging the suite.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// fullBatch is one row more than the sweep reads per pass.
const fullBatch = 501

// sweepFixture is reclaimFixture that keeps the connection, so a test can say
// WHEN a row was deleted — the order the sweep meets rows in is the point.
func sweepFixture(t *testing.T) (*sql.DB, db.Store, int64) {
	t.Helper()
	conn, store := dbtest.NewTestDB(t)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "s", Driver: "local", MountPath: "s", Enabled: true, ConfigJSON: []byte(`{}`),
	})
	require.NoError(t, err)
	return conn, store, st.ID
}

// trashedAgo trashes a new file and backdates its deletion by `ago`, written
// in CURRENT_TIMESTAMP's own format so it compares the way real rows do.
func trashedAgo(t *testing.T, conn *sql.DB, store db.Store, sid int64, name string, ago time.Duration) int64 {
	t.Helper()
	id := trashed(t, store, sid, name)
	_, err := conn.Exec(`UPDATE nodes SET deleted_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-ago).Format("2006-01-02 15:04:05"), id)
	require.NoError(t, err)
	return id
}

func trashedCount(t *testing.T, conn *sql.DB, sid int64) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRow(
		`SELECT COUNT(*) FROM nodes WHERE storage_id = ? AND deleted_at IS NOT NULL`, sid).Scan(&n))
	return n
}

func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// An admin narrows "empty the trash" to one storage while another storage's
// trash is older and larger than a batch.
func TestEmptyOlderThan_NarrowedPastAFullBatchOfAnotherStorage(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	other := secondStorage(t, store)
	for i := 0; i < fullBatch; i++ {
		trashedAgo(t, conn, store, other, fmt.Sprintf("older-%d.pdf", i), 2*time.Hour)
	}
	for i := 0; i < 3; i++ {
		trashedAgo(t, conn, store, sid, fmt.Sprintf("mine-%d.txt", i), time.Hour)
	}

	res, err := trash.New(store, nil, nil).EmptyOlderThan(deadline(t), 0, sid)
	require.NoError(t, err, "the sweep never got past the other storage's rows")

	assert.Equal(t, 3, res.Deleted)
	assert.Equal(t, 3, res.Scanned, "only the named storage's rows are counted")
	assert.Zero(t, trashedCount(t, conn, sid), "the named storage's trash is empty")
	assert.Equal(t, fullBatch, trashedCount(t, conn, other), "the other storage's trash is untouched")
}

// A tenant admin empties their trash on a shared instance where another
// tenant's trash is older and larger than a batch. The shape of the reported
// bug's instance: eleven tenants, one filex.
func TestEmptyOlderThan_TenantPastAFullBatchOfAnotherTenant(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	other := secondStorage(t, store)
	for i := 0; i < fullBatch; i++ {
		trashedAgo(t, conn, store, other, fmt.Sprintf("theirs-%d.pdf", i), 2*time.Hour)
	}
	for i := 0; i < 3; i++ {
		trashedAgo(t, conn, store, sid, fmt.Sprintf("ours-%d.txt", i), time.Hour)
	}
	ctx := tenant.WithScope(deadline(t), &tenant.Scope{ProviderID: 7, StorageIDs: []int64{sid}})

	res, err := trash.New(store, nil, nil).EmptyOlderThan(ctx, 0, 0)
	require.NoError(t, err, "the sweep never got past the other tenant's rows")

	assert.Equal(t, 3, res.Deleted)
	assert.Zero(t, trashedCount(t, conn, sid))
	assert.Equal(t, fullBatch, trashedCount(t, conn, other), "another tenant's trash must survive")
}

// failingRows refuses to hard-delete the rows it was told about, the way a
// row whose delete keeps failing behaves.
type failingRows struct {
	db.Store
	fail map[int64]bool
}

func (f *failingRows) HardDeleteNode(ctx context.Context, id int64) error {
	if f.fail[id] {
		return errors.New("injected: row will not delete")
	}
	return f.Store.HardDeleteNode(ctx, id)
}

// The nightly retention sweep has no request deadline at all: a full batch of
// rows that will not purge used to keep it on the same batch for the rest of
// the process's life. Each row is tried once per run, and counted once.
func TestPurgeExpired_TriesARowThatFailsOnce(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	stuck := &failingRows{Store: store, fail: map[int64]bool{}}
	for i := 0; i < fullBatch; i++ {
		stuck.fail[trashedAgo(t, conn, store, sid, fmt.Sprintf("stuck-%d.bin", i), 40*24*time.Hour)] = true
	}
	for i := 0; i < 3; i++ {
		trashedAgo(t, conn, store, sid, fmt.Sprintf("expired-%d.bin", i), 35*24*time.Hour)
	}

	res, err := trash.New(stuck, nil, nil).PurgeExpired(deadline(t))
	require.NoError(t, err, "the sweep never got past the rows that will not purge")

	assert.Equal(t, 3, res.Deleted)
	assert.Equal(t, fullBatch, res.Failed, "each failing row counted once, not once per pass")
	assert.Equal(t, fullBatch+3, res.Scanned)
	assert.Equal(t, fullBatch, trashedCount(t, conn, sid), "the failing rows stay in the trash")
}

// cancelAfter ends the run's context once `left` rows have been purged —
// after the row's delete, so the row in hand is not the one that dies.
type cancelAfter struct {
	db.Store
	left   int
	cancel context.CancelFunc
}

func (c *cancelAfter) HardDeleteNode(ctx context.Context, id int64) error {
	err := c.Store.HardDeleteNode(ctx, id)
	if c.left--; c.left == 0 {
		c.cancel()
	}
	return err
}

// A run whose context ends stops where it is. It used to carry on through the
// rest of its batch, where every row failed on the dead context and was logged
// and counted as a failed purge — 1,091 such lines for three cut-off requests
// on the instance that reported this — and then reported success.
func TestEmptyOlderThan_StopsWhenItsContextEnds(t *testing.T) {
	conn, store, sid := sweepFixture(t)
	for i := 0; i < 10; i++ {
		trashedAgo(t, conn, store, sid, fmt.Sprintf("f-%d.txt", i), time.Hour)
	}
	ctx, cancel := context.WithCancel(deadline(t))
	cut := &cancelAfter{Store: store, left: 2, cancel: cancel}

	res, err := trash.New(cut, nil, nil).EmptyOlderThan(ctx, 0, 0)
	require.ErrorIs(t, err, context.Canceled, "a run that was cut short does not report success")

	assert.Zero(t, res.Failed, "rows the run never reached are not failures")
	assert.Equal(t, 2, res.Deleted)
	assert.Equal(t, 8, trashedCount(t, conn, sid), "the rest waits for the next run")
}

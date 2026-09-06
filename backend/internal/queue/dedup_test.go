package queue_test

// The coalescing key contract, driver-side. These run against the SQLite
// driver (always present); the Postgres and Redis drivers implement the same
// contract and their integration tests run when FILEX_TEST_PG_DSN /
// FILEX_TEST_REDIS_URL are set.
//
// The property that matters is asymmetric, and the tests are written to say
// so: two concurrent enqueues of the same key must produce exactly ONE op.
// Producing two would waste a scan. Producing none is the bug this whole
// feature exists to prevent.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"
)

func TestDedup_SecondPendingEnqueueIsRefused(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	id, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	assert.ErrorIs(t, err, queue.ErrDuplicate)

	_, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "the second request must not create a row")

	// A different key is unaffected.
	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k2"})
	require.NoError(t, err)
	_, total, err = drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
}

// An empty key opts out, which is what keeps every pre-existing caller
// byte-for-byte unchanged.
func TestDedup_EmptyKeyOptsOut(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	for i := 0; i < 3; i++ {
		_, err := drv.Enqueue(ctx, queue.Op{Type: "demo"})
		require.NoError(t, err)
	}
	_, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
}

// The key is scoped to `pending`, so it is released the moment a worker takes
// the op. A request arriving after that queues a fresh scan rather than being
// dropped — the running scan may already have read the old bytes.
func TestDedup_KeyReleasedOnDequeue(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	_, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	require.NoError(t, err)

	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	require.ErrorIs(t, err, queue.ErrDuplicate)

	op, err := drv.Dequeue(ctx, []string{"demo"})
	require.NoError(t, err)
	require.Equal(t, queue.StatusRunning, op.Status)

	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	assert.NoError(t, err, "key must be free once the op is no longer pending")
}

// ⚠ A retried op must not collide with a key that has since been taken.
// Fail(retry) puts the row back to pending, and if it still carried its key
// while a newer op held it, that UPDATE would violate the partial unique index
// and strand the op in running for ever. The key is released at DEQUEUE, which
// is what makes this safe — and what makes all three drivers agree on when a
// key becomes free.
func TestDedup_RetryAfterANewOpTookTheKey(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	first, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	require.NoError(t, err)

	claimed, err := drv.Dequeue(ctx, []string{"demo"})
	require.NoError(t, err)
	require.Equal(t, first, claimed.ID)

	// A save lands while the first scan is running: a new op takes the key.
	second, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	require.NoError(t, err)

	// The running op now fails and goes back for another attempt.
	require.NoError(t, drv.Fail(ctx, first, "clamd down", true))

	got, err := drv.Get(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusPending, got.Status, "the retry must not be stranded in running")

	_, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)

	// And the newer op still holds the key.
	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	assert.ErrorIs(t, err, queue.ErrDuplicate)
	assert.NotEqual(t, first, second)
}

// A cancelled op releases its key too, for the same reason.
func TestDedup_KeyReleasedOnCancel(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	id, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	require.NoError(t, err)
	require.NoError(t, drv.Cancel(ctx, id))

	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
	assert.NoError(t, err)
}

// ⚠ The race that must not happen is "both callers skip". Exactly one insert
// survives, and it is never zero.
func TestDedup_ConcurrentEnqueuesProduceExactlyOne(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	const n = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		okN   int
		dupN  int
		other []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "race"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				okN++
			case err == queue.ErrDuplicate:
				dupN++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Empty(t, other, "no unexpected errors")
	assert.Equal(t, 1, okN, "exactly one enqueue may win")
	assert.Equal(t, n-1, dupN)

	_, total, err := drv.List(ctx, queue.StatusPending, 100, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

// Delayed + deduped together: the op is scheduled, invisible to Dequeue until
// not_before passes, and still holding its key in the meantime.
func TestDedup_HoldsKeyWhileScheduled(t *testing.T) {
	ctx := context.Background()
	drv := setupSQLite(t)

	at := time.Now().Add(30 * time.Minute)
	_, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1", NotBefore: &at})
	require.NoError(t, err)

	_, err = drv.Dequeue(ctx, []string{"demo"})
	assert.ErrorIs(t, err, queue.ErrEmpty, "not runnable yet")

	_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1", NotBefore: &at})
	assert.ErrorIs(t, err, queue.ErrDuplicate, "still pending, still coalescing")
}

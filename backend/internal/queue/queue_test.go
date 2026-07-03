package queue_test

// This is the cross-driver contract test. It exercises the SQLite driver
// which is always present (registered via blank-import in setupSQLite),
// and spins up the worker pool to verify retries, ack, fail and orphan
// recovery. PostgreSQL and Redis drivers reuse the same Driver contract;
// integration tests for those run only when FILEX_TEST_PG_DSN /
// FILEX_TEST_REDIS_URL are set in CI.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"

	// Register the sqlite queue driver and the underlying SQL driver.
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/sqlite"
	_ "modernc.org/sqlite"
)

// setupSQLite returns a Driver wired against a fresh on-disk SQLite DB
// in t.TempDir(). The schema (ops_queue) is created inline because we
// don't want to drag in the full migration runner here.
func setupSQLite(t *testing.T) queue.Driver {
	t.Helper()

	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "queue.db"))
	conn, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	conn.SetMaxOpenConns(1)

	_, err = conn.Exec(`
CREATE TABLE ops_queue (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at    DATETIME,
    finished_at   DATETIME,
    not_before    DATETIME
);`)
	require.NoError(t, err)

	drv, err := queue.Get("sqlite")
	require.NoError(t, err)
	require.NoError(t, drv.Init(context.Background(), map[string]any{"db": conn}))
	return drv
}

// TestEnqueueDequeueAck — the happiest path. Enqueue 1, dequeue it,
// ack it, observe done in the next stats snapshot.
func TestEnqueueDequeueAck(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	id, err := drv.Enqueue(ctx, queue.Op{
		Type:    queue.TypeThumb,
		Payload: map[string]any{"node_id": int64(123)},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	op, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, id, op.ID)
	assert.Equal(t, queue.StatusRunning, op.Status)
	assert.Equal(t, 1, op.Attempts)
	assert.Equal(t, queue.DefaultMaxAttempts, op.MaxAttempts)
	assert.Equal(t, float64(123), op.Payload["node_id"]) // JSON numbers round-trip as float64

	// Second dequeue should be empty (we only enqueued one op).
	_, err = drv.Dequeue(ctx, nil)
	assert.True(t, errors.Is(err, queue.ErrEmpty), "expected ErrEmpty, got %v", err)

	require.NoError(t, drv.Ack(ctx, id))

	got, err := drv.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusDone, got.Status)
	require.NotNil(t, got.FinishedAt)
}

// TestDequeue_TypeFilter — workers should only pick up types they're
// registered for. Enqueue two ops of different types, request only one
// type, and verify the other stays pending.
func TestDequeue_TypeFilter(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	thumbID, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
	require.NoError(t, err)
	reportID, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeReplicaReport})
	require.NoError(t, err)

	// Ask for thumb only.
	op, err := drv.Dequeue(ctx, []string{queue.TypeThumb})
	require.NoError(t, err)
	assert.Equal(t, thumbID, op.ID)

	// Second call with same filter — empty (the report shouldn't match).
	_, err = drv.Dequeue(ctx, []string{queue.TypeThumb})
	assert.True(t, errors.Is(err, queue.ErrEmpty))

	// And with the other filter we still get the report.
	op, err = drv.Dequeue(ctx, []string{queue.TypeReplicaReport})
	require.NoError(t, err)
	assert.Equal(t, reportID, op.ID)
}

// TestDequeue_PriorityFIFO — higher priority wins; same-priority ties
// break by enqueue time.
func TestDequeue_PriorityFIFO(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	low1, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb, Priority: 0})
	require.NoError(t, err)
	low2, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb, Priority: 0})
	require.NoError(t, err)
	high, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb, Priority: 100})
	require.NoError(t, err)

	first, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, high, first.ID, "highest priority must dequeue first")

	second, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, low1, second.ID, "older same-priority op should beat younger")
	_ = low2
}

// TestFail_RetryRequeues — retry=true should put the op back into
// pending and bump attempts.
func TestFail_RetryRequeues(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	id, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb, MaxAttempts: 5})
	require.NoError(t, err)

	op, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, op.Attempts)

	require.NoError(t, drv.Fail(ctx, id, "transient: connection reset", true))

	// Wait past the 30s NotBefore by overriding it.
	// (We don't want to actually sleep 30s in a test.)
	// Cheat: set not_before to NULL via Retry — but Retry only works on
	// failed status. Simpler: just assert the row is back in pending and
	// not_before is set.
	got, err := drv.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusPending, got.Status)
	assert.Equal(t, "transient: connection reset", got.LastError)
	require.NotNil(t, got.NotBefore, "retry should schedule via not_before")
}

// TestFail_NoRetryTerminates — retry=false freezes the op as failed.
func TestFail_NoRetryTerminates(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	id, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
	require.NoError(t, err)
	_, err = drv.Dequeue(ctx, nil)
	require.NoError(t, err)

	require.NoError(t, drv.Fail(ctx, id, "permanent: bad payload", false))

	got, err := drv.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusFailed, got.Status)
	require.NotNil(t, got.FinishedAt)

	// Pool should not pick it up again.
	_, err = drv.Dequeue(ctx, nil)
	assert.True(t, errors.Is(err, queue.ErrEmpty))
}

// TestRetry_FailedToPending — admin "retry" button.
func TestRetry_FailedToPending(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	id, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
	require.NoError(t, err)
	_, err = drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, drv.Fail(ctx, id, "boom", false))

	require.NoError(t, drv.Retry(ctx, id))

	op, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, id, op.ID)

	// Calling Retry on a non-failed op surfaces ErrNotFound.
	require.NoError(t, drv.Ack(ctx, id))
	err = drv.Retry(ctx, id)
	assert.True(t, errors.Is(err, queue.ErrNotFound), "Retry on done op should be a no-op match (ErrNotFound)")
}

// TestCancel_Pending — cancel a pending op before any worker grabs it.
func TestCancel_Pending(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	id, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
	require.NoError(t, err)

	require.NoError(t, drv.Cancel(ctx, id))

	op, err := drv.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusCancelled, op.Status)

	// Cancelling again returns an explicit error (op already cancelled).
	err = drv.Cancel(ctx, id)
	require.Error(t, err)

	// Cancelling unknown id → ErrNotFound.
	err = drv.Cancel(ctx, "deadbeef")
	assert.True(t, errors.Is(err, queue.ErrNotFound))
}

// TestStats — counts roll up correctly.
func TestStats(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
		require.NoError(t, err)
	}

	// Run one through to done.
	op1, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, drv.Ack(ctx, op1.ID))

	// Fail one permanently.
	op2, err := drv.Dequeue(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, drv.Fail(ctx, op2.ID, "no", false))

	stats, err := drv.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Pending, "1 still in pending")
	assert.Equal(t, int64(1), stats.Failed)
	assert.Equal(t, int64(1), stats.Done24h, "the acked op should be in done24h")
}

// TestList — pagination + status filter.
func TestList(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
		require.NoError(t, err)
	}

	all, total, err := drv.List(ctx, "", 100, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, all, 5)

	pending, total, err := drv.List(ctx, queue.StatusPending, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, pending, 5)

	page1, _, err := drv.List(ctx, "", 2, 0)
	require.NoError(t, err)
	page2, _, err := drv.List(ctx, "", 2, 2)
	require.NoError(t, err)
	page3, _, err := drv.List(ctx, "", 2, 4)
	require.NoError(t, err)
	assert.Len(t, page1, 2)
	assert.Len(t, page2, 2)
	assert.Len(t, page3, 1)
}

// TestRecoverOrphans — running rows older than `olderThan` are flipped
// back to pending.
func TestRecoverOrphans(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	id, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
	require.NoError(t, err)

	_, err = drv.Dequeue(ctx, nil)
	require.NoError(t, err)

	// olderThan=0 ⇒ ALL running rows immediately re-queued.
	n, err := drv.RecoverOrphans(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	op, err := drv.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusPending, op.Status)
}

// TestPool_RoundTrip — end-to-end, the whole worker pool. 5 ops in,
// each handler bumps a counter, all ack.
func TestPool_RoundTrip(t *testing.T) {
	drv := setupSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const N = 5
	for i := 0; i < N; i++ {
		_, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb, Payload: map[string]any{"i": i}})
		require.NoError(t, err)
	}

	var processed atomic.Int64
	pool := queue.NewPool(drv, 3, queue.WithPollInterval(50*time.Millisecond))
	pool.Register(queue.TypeThumb, func(_ context.Context, _ queue.Op) error {
		processed.Add(1)
		return nil
	})
	pool.Start(ctx)
	defer pool.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && processed.Load() < N {
		time.Sleep(20 * time.Millisecond)
	}
	assert.Equal(t, int64(N), processed.Load(), "pool should have drained all ops")

	// All ops should be in done.
	stats, err := drv.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(N), stats.Done24h)
	assert.Equal(t, int64(0), stats.Pending)
}

// TestPool_RetriesUntilSuccess — handler fails 2 times then succeeds on
// the 3rd attempt; verify the op finishes done with attempts==3.
func TestPool_RetriesUntilSuccess(t *testing.T) {
	drv := setupSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeReplicaRetry, MaxAttempts: 5})
	require.NoError(t, err)

	var attemptN atomic.Int32
	pool := queue.NewPool(drv, 1, queue.WithPollInterval(20*time.Millisecond))
	pool.Register(queue.TypeReplicaRetry, func(_ context.Context, _ queue.Op) error {
		n := attemptN.Add(1)
		if n < 3 {
			return fmt.Errorf("attempt %d intentionally failed", n)
		}
		return nil
	})
	pool.Start(ctx)
	defer pool.Stop()

	// We rely on the requeue-with-not_before; in production retries
	// wait 30s. To keep the test under a few seconds we manually clear
	// not_before whenever the row goes back to pending. (The worker
	// pool itself is the unit under test, not the not_before timer.)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		op, err := drv.Get(ctx, id)
		if err == nil && op.Status == queue.StatusDone {
			break
		}
		// If the row is pending with not_before set, bump it so the
		// next dequeue picks it up immediately.
		if err == nil && op.Status == queue.StatusPending && op.NotBefore != nil {
			// We need a way to clear not_before from the test — the
			// driver doesn't expose one directly. Use Retry as a
			// fallback: it transitions failed→pending AND clears
			// not_before. Pretend the row failed for the moment.
			// (In practice the not_before timer expires on its own.)
			_ = clearNotBefore(t, drv, id)
		}
		time.Sleep(20 * time.Millisecond)
	}

	op, err := drv.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, queue.StatusDone, op.Status, "op should eventually succeed; attempts=%d", attemptN.Load())
	assert.GreaterOrEqual(t, attemptN.Load(), int32(3))
}

// TestPool_StopsCleanly — once Stop is invoked, the pool must drain
// in-flight handlers and return.
func TestPool_StopsCleanly(t *testing.T) {
	drv := setupSQLite(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := drv.Enqueue(ctx, queue.Op{Type: queue.TypeThumb})
		require.NoError(t, err)
	}

	var (
		started sync.WaitGroup
		release = make(chan struct{})
	)
	pool := queue.NewPool(drv, 2, queue.WithPollInterval(20*time.Millisecond))
	started.Add(2)
	pool.Register(queue.TypeThumb, func(hctx context.Context, _ queue.Op) error {
		started.Done()
		select {
		case <-release:
		case <-hctx.Done():
		}
		return nil
	})
	pool.Start(ctx)
	started.Wait()
	close(release)

	done := make(chan struct{})
	go func() {
		pool.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pool.Stop did not return within 2s")
	}
}

// clearNotBefore is a test helper that uses the queue driver's only
// public lever to nudge a delayed row forward — we bounce it through
// failed → pending via Fail+Retry. This is a test-only crutch.
func clearNotBefore(t *testing.T, drv queue.Driver, id string) error {
	t.Helper()
	op, err := drv.Get(context.Background(), id)
	if err != nil {
		return err
	}
	if op.Status != queue.StatusPending {
		return nil
	}
	// Fail it (no retry) → failed; then Retry → pending with not_before
	// cleared by the driver.
	if err := drv.Fail(context.Background(), id, "test: bump", false); err != nil {
		return err
	}
	return drv.Retry(context.Background(), id)
}

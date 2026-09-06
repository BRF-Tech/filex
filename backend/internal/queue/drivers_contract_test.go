package queue_test

// The coalescing-key contract, run against EVERY queue driver that is
// configured, not just the one that is always compiled in.
//
// ⚠ This file exists because the promise "the Postgres and Redis drivers
// implement the same Driver contract; integration tests for those run when
// FILEX_TEST_PG_DSN / FILEX_TEST_REDIS_URL are set" was, until now, only a
// comment: no test read either variable. The three drivers implement
// coalescing in three genuinely different ways — a guarded INSERT plus a
// partial unique index on SQLite, the same plus SQLSTATE 23505 on Postgres,
// and SET NX on Redis — so "same contract" is exactly the kind of claim that
// has to be executed rather than asserted.
//
// Run them:
//
//	FILEX_TEST_REDIS_URL=redis://127.0.0.1:6399/0 go test ./internal/queue/
//	FILEX_TEST_PG_DSN='postgres://filex:filex@127.0.0.1:55432/filex?sslmode=disable' \
//	    go test ./internal/queue/
//
// Unset, the driver is skipped and only SQLite runs — the same behaviour as
// before, so no developer is forced to run a database to work on the queue.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/postgres"
	_ "github.com/brf-tech/filex/backend/internal/queue/drivers/redis"
)

// pgSchema mirrors migrations 00006_queue.sql + 00031_ops_queue_dedup.sql. The
// test owns its own table so it never depends on a migrated database.
const pgSchema = `
DROP TABLE IF EXISTS ops_queue;
CREATE TABLE ops_queue (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    not_before    TIMESTAMPTZ,
    dedup_key     TEXT
);
CREATE INDEX idx_ops_queue_status_pri_at ON ops_queue (status, priority DESC, enqueued_at);
CREATE UNIQUE INDEX idx_ops_queue_dedup_pending
    ON ops_queue (dedup_key) WHERE dedup_key IS NOT NULL AND status = 'pending';
`

// configuredDrivers returns one factory per driver the environment provides.
// SQLite is always there; the other two appear only when their DSN is set.
func configuredDrivers(t *testing.T) map[string]func(*testing.T) queue.Driver {
	t.Helper()
	out := map[string]func(*testing.T) queue.Driver{
		"sqlite": func(t *testing.T) queue.Driver { return setupSQLite(t) },
	}

	if dsn := os.Getenv("FILEX_TEST_PG_DSN"); dsn != "" {
		out["postgres"] = func(t *testing.T) queue.Driver {
			t.Helper()
			conn, err := sql.Open("pgx", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			_, err = conn.Exec(pgSchema)
			require.NoError(t, err, "create ops_queue")
			drv, err := queue.Get("postgres")
			require.NoError(t, err)
			require.NoError(t, drv.Init(context.Background(), map[string]any{"db": conn, "dsn": dsn}))
			return drv
		}
	}

	if url := os.Getenv("FILEX_TEST_REDIS_URL"); url != "" {
		out["redis"] = func(t *testing.T) queue.Driver {
			t.Helper()
			drv, err := queue.Get("redis")
			require.NoError(t, err)
			// A per-test key prefix keeps parallel runs and leftover state
			// from previous runs out of each other's way.
			prefix := fmt.Sprintf("filextest:%d:%s", time.Now().UnixNano(), t.Name())
			require.NoError(t, drv.Init(context.Background(), map[string]any{
				"url": url, "key_prefix": prefix,
			}))
			t.Cleanup(func() { _ = drv.Close() })
			return drv
		}
	}
	return out
}

// The basic lifecycle, on every configured driver. It looks too simple to be
// worth writing until you notice it is the test that found a postgres-backed
// queue could never claim an op: Pool always passes a type filter, and that
// path had never been executed against a real Postgres.
func TestDriverContract_Lifecycle(t *testing.T) {
	for name, mk := range configuredDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			drv := mk(t)

			id, err := drv.Enqueue(ctx, queue.Op{
				Type:    "demo",
				Payload: map[string]any{"node_id": int64(7)},
			})
			require.NoError(t, err)

			// ⚠ With a type filter, the way queue.Pool always calls it.
			op, err := drv.Dequeue(ctx, []string{"demo", "other"})
			require.NoError(t, err, "a type-filtered dequeue must work")
			assert.Equal(t, id, op.ID)
			assert.Equal(t, queue.StatusRunning, op.Status)
			assert.Equal(t, 1, op.Attempts)
			assert.EqualValues(t, 7, op.Payload["node_id"])

			require.NoError(t, drv.Ack(ctx, id))
			done, err := drv.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, queue.StatusDone, done.Status)

			// A type filter that matches nothing yields ErrEmpty, not an error.
			_, err = drv.Enqueue(ctx, queue.Op{Type: "demo"})
			require.NoError(t, err)
			_, err = drv.Dequeue(ctx, []string{"nothing-registered"})
			assert.ErrorIs(t, err, queue.ErrEmpty)

			// Failure with retry puts it back; without, it terminates.
			id2, err := drv.Enqueue(ctx, queue.Op{Type: "demo2"})
			require.NoError(t, err)
			_, err = drv.Dequeue(ctx, []string{"demo2"})
			require.NoError(t, err)
			require.NoError(t, drv.Fail(ctx, id2, "boom", false))
			failed, err := drv.Get(ctx, id2)
			require.NoError(t, err)
			assert.Equal(t, queue.StatusFailed, failed.Status)
			assert.Equal(t, "boom", failed.LastError)
		})
	}
}

func TestDriverContract_Coalescing(t *testing.T) {
	for name, mk := range configuredDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			t.Run("second pending enqueue is refused", func(t *testing.T) {
				drv := mk(t)
				_, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
				require.NoError(t, err)
				_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
				assert.ErrorIs(t, err, queue.ErrDuplicate)

				_, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
				require.NoError(t, err)
				assert.EqualValues(t, 1, total)
			})

			t.Run("empty key opts out", func(t *testing.T) {
				drv := mk(t)
				for i := 0; i < 3; i++ {
					_, err := drv.Enqueue(ctx, queue.Op{Type: "demo"})
					require.NoError(t, err)
				}
				_, total, err := drv.List(ctx, queue.StatusPending, 10, 0)
				require.NoError(t, err)
				assert.EqualValues(t, 3, total)
			})

			t.Run("key released on dequeue", func(t *testing.T) {
				drv := mk(t)
				_, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
				require.NoError(t, err)
				_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
				require.ErrorIs(t, err, queue.ErrDuplicate)

				op, err := drv.Dequeue(ctx, []string{"demo"})
				require.NoError(t, err)
				require.Equal(t, queue.StatusRunning, op.Status)

				_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1"})
				assert.NoError(t, err, "a request arriving while the op runs must queue a new one")
			})

			// The asymmetry that matters: concurrent enqueues may produce one
			// op, never zero.
			t.Run("concurrent enqueues produce exactly one", func(t *testing.T) {
				drv := mk(t)
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

				assert.Empty(t, other)
				assert.Equal(t, 1, okN, "exactly one enqueue may win")
				assert.Equal(t, n-1, dupN)
			})

			// A scheduled op still holds its key while it waits.
			t.Run("holds the key while scheduled", func(t *testing.T) {
				drv := mk(t)
				at := time.Now().Add(30 * time.Minute)
				_, err := drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1", NotBefore: &at})
				require.NoError(t, err)

				_, err = drv.Dequeue(ctx, []string{"demo"})
				assert.ErrorIs(t, err, queue.ErrEmpty, "not runnable yet")

				_, err = drv.Enqueue(ctx, queue.Op{Type: "demo", DedupKey: "k1", NotBefore: &at})
				assert.ErrorIs(t, err, queue.ErrDuplicate)
			})
		})
	}
}

// Delayed dispatch, on every configured driver. The SQLite implementation was
// wrong outside UTC until it was fixed; the others store an absolute instant.
func TestDriverContract_NotBeforeIsZoneIndependent(t *testing.T) {
	for name, mk := range configuredDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			drv := mk(t)
			base := time.Now().Add(2 * time.Hour)
			for _, loc := range []*time.Location{
				time.UTC,
				time.FixedZone("UTC+3", 3*60*60),
				time.FixedZone("UTC-5", -5*60*60),
			} {
				at := base.In(loc)
				id, err := drv.Enqueue(ctx, queue.Op{Type: "zone", NotBefore: &at})
				require.NoError(t, err)
				got, err := drv.Get(ctx, id)
				require.NoError(t, err)
				require.NotNil(t, got.NotBefore)
				assert.WithinDuration(t, base, *got.NotBefore, time.Second)
			}
			_, err := drv.Dequeue(ctx, []string{"zone"})
			assert.ErrorIs(t, err, queue.ErrEmpty, "nothing scheduled two hours out is runnable")
		})
	}
}

// Priority, on every configured driver.
//
// The SQL drivers claim with `ORDER BY priority DESC, enqueued_at ASC`. The
// Redis driver kept its pending set in a LIST consumed with BLMOVE, and a LIST
// is positional: whatever priority an op carried was written to the data hash,
// read back by Get, shown in the admin UI — and ignored by the one operation
// it exists for. So a scan the storage sync discovered (Priority -1, so that a
// person's upload is not stuck behind a 20 000-file first import) still went
// out ahead of the upload on Redis, and the 41 seconds the SQLite fix removed
// were still being paid.
//
// This is the red proof: it passes on sqlite and postgres, and fails on redis
// until the pending set becomes an ordered one.
func TestDriverContract_PriorityBeatsPosition(t *testing.T) {
	for name, mk := range configuredDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			drv := mk(t)

			// A backlog of sweep-priority ops, the shape of a first import.
			const backlog = 40
			for i := 0; i < backlog; i++ {
				_, err := drv.Enqueue(ctx, queue.Op{
					Type:     "scan",
					Priority: -1,
					Payload:  map[string]any{"n": i},
				})
				require.NoError(t, err)
			}

			// Someone uploads a file. Enqueued LAST, into the middle of the
			// backlog as far as arrival order goes.
			urgent, err := drv.Enqueue(ctx, queue.Op{
				Type:    "scan",
				Payload: map[string]any{"n": "interactive"},
			})
			require.NoError(t, err)

			op, err := drv.Dequeue(ctx, []string{"scan"})
			require.NoError(t, err)
			assert.Equal(t, urgent, op.ID,
				"a person's op waited behind the whole backlog: priority is not honoured")
			assert.Equal(t, 0, op.Priority)

			// And the rest still drain oldest-first within their own band, so
			// the fix is an ordering, not a stack.
			for i := 0; i < backlog; i++ {
				got, err := drv.Dequeue(ctx, []string{"scan"})
				require.NoError(t, err)
				assert.EqualValues(t, i, toInt(got.Payload["n"]),
					"equal-priority ops must still come out oldest-first")
			}
		})
	}
}

// toInt normalises the numeric payload a driver round-trips (JSON gives
// float64 on one, json.Number on another).
func toInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return -1
}

// No op may be handed to two workers, and none may be lost.
//
// On the Redis driver this used to be two steps: BLMOVE moved the id from
// pending to running, and a separate transaction flipped the hash to
// `running`. A process that died in the gap left an id in the running list
// whose hash still said `pending`, which is exactly what RecoverOrphans reads
// as a stale list entry and DROPS — the op then existed in no list at all,
// pending forever in its own hash, and nothing would ever run it. The claim is
// one atomic step now; this is the test that says so out loud.
func TestDriverContract_EachOpIsClaimedExactlyOnce(t *testing.T) {
	for name, mk := range configuredDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			drv := mk(t)

			const ops, workers = 60, 8
			want := map[string]bool{}
			for i := 0; i < ops; i++ {
				id, err := drv.Enqueue(ctx, queue.Op{Type: "race", Payload: map[string]any{"n": i}})
				require.NoError(t, err)
				want[id] = true
			}

			var (
				mu   sync.Mutex
				seen = map[string]int{}
				wg   sync.WaitGroup
			)
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						op, err := drv.Dequeue(ctx, []string{"race"})
						if err != nil {
							return // ErrEmpty: the queue is drained
						}
						mu.Lock()
						seen[op.ID]++
						done := len(seen)
						mu.Unlock()
						_ = drv.Ack(ctx, op.ID)
						if done == ops {
							return
						}
					}
				}()
			}
			wg.Wait()

			assert.Len(t, seen, ops, "an op was lost")
			for id := range want {
				assert.Equal(t, 1, seen[id], "op %s was claimed %d times", id, seen[id])
			}
		})
	}
}

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sqlitedrv "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
)

// A request that is cancelled while its query is starting must not take the
// database down with it.
//
// ⚠⚠ Written after it did, three times, each time looking like something
// else: a dev container whose logins all answered 401, a CI run with 61
// failures (2026-09-12), and a local end-to-end run whose last 148 tests never
// signed in (2026-09-14). The server log carried the real cause every time —
// `interrupted (9)` on every statement, the queue's included, for as long as
// the process lived.
//
// The mechanism is in modernc.org/sqlite up to v1.30.x. `stmt.query` arms a
// goroutine that calls sqlite3_interrupt when the context is done. If the
// cancellation lands after the first step produced a row but before the
// query returns, the driver throws the rows object away — WITHOUT finalizing
// its statement — and returns ctx.Err(). database/sql then hands the
// connection back as healthy. That statement stays active forever, and
// SQLite only clears the interrupt flag when no statement on the connection
// is active, so the flag is never cleared again: every later statement on the
// connection fails with SQLITE_INTERRUPT. filex runs SQLite on ONE connection
// (SetMaxOpenConns(1)), so that is every statement in the process.
//
// Fixed upstream (the rows are closed before being dropped); this test keeps
// a downgrade — or a wrapper that reintroduces the leak — from shipping.
func TestSQLite_CancelledQueryDoesNotPoisonTheConnection(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "cancel.sqlite")
	conn, err := sqlitedrv.Driver{}.Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx := context.Background()
	_, err = conn.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`)
	require.NoError(t, err)
	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	for i := 0; i < 2000; i++ {
		_, err = tx.ExecContext(ctx, `INSERT INTO t (v) VALUES (?)`, "row")
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	// How long a query takes to reach its first row, so the cancellations
	// below land across that window rather than all before or all after it.
	start := time.Now()
	rows, err := conn.QueryContext(ctx, `SELECT id, v FROM t ORDER BY v, id`)
	require.NoError(t, err)
	firstRow := time.Since(start)
	require.NoError(t, rows.Close())
	if firstRow < 50*time.Microsecond {
		firstRow = 50 * time.Microsecond
	}

	const attempts = 3000
	for i := 0; i < attempts; i++ {
		// Sweep the cancellation point from well before to well after the
		// first row, so the narrow window is crossed many times.
		delay := time.Duration(i%60) * firstRow / 30
		qctx, cancel := context.WithCancel(ctx)
		timer := time.AfterFunc(delay, cancel)
		rows, qerr := conn.QueryContext(qctx, `SELECT id, v FROM t ORDER BY v, id`)
		if qerr == nil {
			_ = rows.Close()
		}
		timer.Stop()
		cancel()

		var n int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Fatalf("after %d cancelled queries, an uncancelled one fails: %v — "+
				"a cancelled query left its statement active and the connection "+
				"is interrupted for good", i+1, err)
		}
	}
}

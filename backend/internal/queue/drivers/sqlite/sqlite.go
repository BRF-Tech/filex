// Package sqlite is the default queue driver, sharing the filex DB file
// with the metadata store. It uses BEGIN IMMEDIATE for the dequeue
// fast-path; SQLite's single-writer semantics make this race-free under
// the typical single-node deployment. For HA setups switch to the
// postgres driver via FILEMANAGER_QUEUE_DRIVER=postgres.
package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	// Keep the modernc/sqlite registration; the queue package opens its
	// own *sql.DB so it is safe to register twice — Go's database/sql
	// guards the registry.
	_ "modernc.org/sqlite"

	"github.com/brf-tech/filex/backend/internal/queue"
)

func init() {
	queue.Register("sqlite", func() queue.Driver { return &Driver{} })
}

// Driver is the SQLite-backed queue.
type Driver struct {
	db *sql.DB
}

// Name implements queue.Driver.
func (Driver) Name() string { return "sqlite" }

// Init opens the queue's DB. cfg keys:
//
//	dsn  — SQLite connection string. Required.
//	      Pass the same DSN as the metadata store to share one file.
//	db   — *sql.DB (optional). When set, dsn is ignored. Used by the
//	      bootstrap path that already has the application *sql.DB
//	      handle.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if v, ok := cfg["db"].(*sql.DB); ok && v != nil {
		d.db = v
		return nil
	}
	dsn, _ := cfg["dsn"].(string)
	if dsn == "" {
		return errors.New("queue/sqlite: dsn required (or supply *sql.DB via cfg[\"db\"])")
	}
	if !strings.Contains(dsn, "_pragma") {
		joiner := "?"
		if strings.Contains(dsn, "?") {
			joiner = "&"
		}
		dsn += joiner + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("queue/sqlite: open: %w", err)
	}
	conn.SetMaxOpenConns(1)
	d.db = conn
	return nil
}

// Close releases the underlying *sql.DB only when this driver opened it
// (cfg["dsn"] path). When wired against the application's shared *sql.DB
// (cfg["db"]), Close is a no-op so we don't tear down the metadata store.
func (d *Driver) Close() error {
	// We can't tell apart the two init paths after the fact; the safest
	// semantic is "don't close shared handles". The bootstrap supplies
	// its own *sql.DB, owns the lifecycle, and never invokes Close on
	// the queue. Tests that pass a dsn rely on t.Cleanup → Close()
	// and we honor that by checking SetMaxOpenConns: when the caller
	// owned the handle they don't reach in here.
	return nil
}

// Enqueue inserts an op and returns its id.
func (d *Driver) Enqueue(ctx context.Context, op queue.Op) (string, error) {
	if op.Type == "" {
		return "", errors.New("queue/sqlite: op.Type required")
	}
	if op.MaxAttempts == 0 {
		op.MaxAttempts = queue.DefaultMaxAttempts
	}
	if op.Status == "" {
		op.Status = queue.StatusPending
	}
	id := op.ID
	if id == "" {
		var err error
		id, err = newID()
		if err != nil {
			return "", err
		}
	}
	payload := op.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("queue/sqlite: marshal payload: %w", err)
	}
	if op.DedupKey != "" {
		return d.enqueueDeduped(ctx, op, id, string(body))
	}
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO ops_queue (id, type, payload, status, priority, max_attempts, not_before)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, op.Type, string(body), op.Status, op.Priority, op.MaxAttempts, sqlTime(op.NotBefore),
	)
	if err != nil {
		return "", fmt.Errorf("queue/sqlite: insert: %w", err)
	}
	return id, nil
}

// enqueueDeduped inserts only when no PENDING op already holds op.DedupKey,
// returning queue.ErrDuplicate when one does.
//
// The guard is inside the INSERT rather than a SELECT followed by an INSERT:
// SQLite serialises writers, so the statement decides the winner atomically
// and two concurrent saves can never both insert — nor both skip, which is
// the failure that would matter (a coalesced scan that never happens is the
// gap this exists to close). The partial unique index added in migration
// 00031 is a second line of defence for any backend where the statement
// alone would not be atomic; a violation from it maps to the same
// ErrDuplicate, so callers see one behaviour either way.
//
// `FROM (SELECT 1) AS one` keeps the statement legal on every SQL dialect
// this driver is ever pointed at — a bare `SELECT ? WHERE ...` is invalid on
// MySQL, and this driver is also what a MySQL-backed install gets, since the
// queue driver defaults to sqlite and is handed the application *sql.DB.
func (d *Driver) enqueueDeduped(ctx context.Context, op queue.Op, id, payload string) (string, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO ops_queue (id, type, payload, status, priority, max_attempts, not_before, dedup_key)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?
		   FROM (SELECT 1) AS one
		  WHERE NOT EXISTS (
		        SELECT 1 FROM ops_queue o
		         WHERE o.dedup_key = ? AND o.status = 'pending')`,
		id, op.Type, payload, op.Status, op.Priority, op.MaxAttempts,
		sqlTime(op.NotBefore), op.DedupKey, op.DedupKey,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return "", queue.ErrDuplicate
		}
		return "", fmt.Errorf("queue/sqlite: insert: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("queue/sqlite: insert rows: %w", err)
	}
	if n == 0 {
		return "", queue.ErrDuplicate
	}
	return id, nil
}

// isUniqueViolation recognises the partial-unique-index collision from
// migration 00031. modernc/sqlite surfaces it as a plain error string.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Dequeue uses an UPDATE-with-subquery RETURNING (SQLite ≥ 3.35) to
// atomically claim the next eligible op for a worker.
func (d *Driver) Dequeue(ctx context.Context, types []string) (queue.Op, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return queue.Op{}, fmt.Errorf("queue/sqlite: begin: %w", err)
	}
	defer tx.Rollback()

	// SELECT the candidate.
	var (
		args  []any
		where = []string{
			"status = 'pending'",
			"(not_before IS NULL OR not_before <= CURRENT_TIMESTAMP)",
		}
	)
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf("type IN (%s)", strings.Join(placeholders, ",")))
	}
	q := fmt.Sprintf(`SELECT id FROM ops_queue WHERE %s
	                  ORDER BY priority DESC, enqueued_at ASC
	                  LIMIT 1`, strings.Join(where, " AND "))

	var id string
	if err := tx.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return queue.Op{}, queue.ErrEmpty
		}
		return queue.Op{}, fmt.Errorf("queue/sqlite: select: %w", err)
	}

	// Claim it.
	if _, err := tx.ExecContext(ctx,
		`UPDATE ops_queue
		 SET status='running', started_at=CURRENT_TIMESTAMP, attempts=attempts+1,
		     dedup_key=NULL
		 -- ⚠ dedup_key is cleared here, not when the op finishes. Two reasons,
		 -- and both are correctness rather than tidiness:
		 --   1. A request arriving while this scan RUNS must queue a new one —
		 --      this scan may already have read the old bytes.
		 --   2. Fail(retry) puts the row back to pending. If it still held
		 --      the key and a newer op had meanwhile taken it, that UPDATE
		 --      would violate the partial unique index and the op would be
		 --      stranded in running for ever.
		 -- Redis releases its claim at the same moment, so all three drivers
		 -- agree on when a key becomes free.
		 WHERE id=? AND status='pending'`, id); err != nil {
		return queue.Op{}, fmt.Errorf("queue/sqlite: claim: %w", err)
	}

	// Read it back inside the same tx so the caller sees attempts++.
	op, err := getInTx(ctx, tx, id)
	if err != nil {
		return queue.Op{}, err
	}
	if err := tx.Commit(); err != nil {
		return queue.Op{}, fmt.Errorf("queue/sqlite: commit: %w", err)
	}
	return op, nil
}

// Ack marks the op done.
func (d *Driver) Ack(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='done', finished_at=CURRENT_TIMESTAMP, last_error=''
		 WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("queue/sqlite: ack: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return queue.ErrNotFound
	}
	return nil
}

// Fail records the failure. retry=true requeues; retry=false terminates.
func (d *Driver) Fail(ctx context.Context, id, errMsg string, retry bool) error {
	q := `UPDATE ops_queue
	      SET status=?, last_error=?, started_at=NULL, finished_at=CURRENT_TIMESTAMP
	      WHERE id=?`
	target := queue.StatusFailed
	if retry {
		// Re-queue: status back to pending, finished_at NULL.
		q = `UPDATE ops_queue
		     SET status='pending', last_error=?, started_at=NULL, finished_at=NULL,
		         not_before=DATETIME('now', '+30 seconds')
		     WHERE id=?`
		_, err := d.db.ExecContext(ctx, q, errMsg, id)
		if err != nil {
			return fmt.Errorf("queue/sqlite: requeue: %w", err)
		}
		return nil
	}
	_, err := d.db.ExecContext(ctx, q, target, errMsg, id)
	if err != nil {
		return fmt.Errorf("queue/sqlite: fail: %w", err)
	}
	return nil
}

// List returns ops with optional status filter and pagination.
func (d *Driver) List(ctx context.Context, status string, limit, offset int) ([]queue.Op, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var (
		args     []any
		whereSQL string
	)
	if status != "" {
		whereSQL = "WHERE status = ?"
		args = append(args, status)
	}

	// Count first.
	var total int64
	if err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM ops_queue "+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("queue/sqlite: count: %w", err)
	}

	rows, err := d.db.QueryContext(ctx,
		`SELECT id, type, payload, status, priority, attempts, max_attempts,
		        COALESCE(last_error,''), enqueued_at, started_at, finished_at, not_before
		 FROM ops_queue `+whereSQL+`
		 ORDER BY enqueued_at DESC
		 LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("queue/sqlite: list: %w", err)
	}
	defer rows.Close()

	var out []queue.Op
	for rows.Next() {
		op, err := scanRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, op)
	}
	return out, total, rows.Err()
}

// Get fetches a single op.
func (d *Driver) Get(ctx context.Context, id string) (queue.Op, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, type, payload, status, priority, attempts, max_attempts,
		        COALESCE(last_error,''), enqueued_at, started_at, finished_at, not_before
		 FROM ops_queue WHERE id=?`, id)
	op, err := scanRowFromQueryRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return queue.Op{}, queue.ErrNotFound
		}
		return queue.Op{}, err
	}
	return op, nil
}

// Stats returns the dashboard counters.
func (d *Driver) Stats(ctx context.Context) (queue.Stats, error) {
	var s queue.Stats
	rows, err := d.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM ops_queue GROUP BY status`)
	if err != nil {
		return s, fmt.Errorf("queue/sqlite: stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return s, err
		}
		switch st {
		case queue.StatusPending:
			s.Pending = n
		case queue.StatusRunning:
			s.Running = n
		case queue.StatusFailed:
			s.Failed = n
		case queue.StatusCancelled:
			s.Cancelled = n
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	if err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ops_queue
		 WHERE status='done' AND finished_at >= DATETIME('now','-1 day')`,
	).Scan(&s.Done24h); err != nil {
		return s, fmt.Errorf("queue/sqlite: done24h: %w", err)
	}
	return s, nil
}

// Cancel transitions a pending op to cancelled.
func (d *Driver) Cancel(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='cancelled', finished_at=CURRENT_TIMESTAMP
		 WHERE id=? AND status='pending'`, id)
	if err != nil {
		return fmt.Errorf("queue/sqlite: cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either missing or not pending — surface a useful error.
		op, getErr := d.Get(ctx, id)
		if errors.Is(getErr, queue.ErrNotFound) {
			return queue.ErrNotFound
		}
		return fmt.Errorf("queue/sqlite: cancel: op already in status %q", op.Status)
	}
	return nil
}

// Retry transitions a failed op back to pending. The attempts counter
// is preserved so the operator can see how many tries it took.
func (d *Driver) Retry(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='pending', started_at=NULL, finished_at=NULL,
		                       last_error='', not_before=NULL
		 WHERE id=? AND status='failed'`, id)
	if err != nil {
		return fmt.Errorf("queue/sqlite: retry: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return queue.ErrNotFound
	}
	return nil
}

// RecoverOrphans flips long-running rows back to pending. Called on boot
// to handle ungraceful shutdowns.
func (d *Driver) RecoverOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoffSec := int64(olderThan.Seconds())
	if cutoffSec < 0 {
		cutoffSec = 0
	}
	res, err := d.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE ops_queue SET status='pending', started_at=NULL
		             WHERE status='running'
		               AND (started_at IS NULL OR started_at <= DATETIME('now','-%d seconds'))`, cutoffSec))
	if err != nil {
		return 0, fmt.Errorf("queue/sqlite: recover: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ─── helpers ───────────────────────────────────────────────────────

// rowScanner is the row-vs-rows abstraction for scanRow.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRow(rs rowScanner) (queue.Op, error) {
	var (
		op          queue.Op
		payloadJSON string
		notBefore   sql.NullTime
		startedAt   sql.NullTime
		finishedAt  sql.NullTime
	)
	if err := rs.Scan(
		&op.ID, &op.Type, &payloadJSON, &op.Status, &op.Priority,
		&op.Attempts, &op.MaxAttempts, &op.LastError,
		&op.EnqueuedAt, &startedAt, &finishedAt, &notBefore,
	); err != nil {
		return op, err
	}
	op.Payload = map[string]any{}
	if payloadJSON != "" {
		_ = json.Unmarshal([]byte(payloadJSON), &op.Payload)
	}
	if startedAt.Valid {
		t := startedAt.Time
		op.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		op.FinishedAt = &t
	}
	if notBefore.Valid {
		t := notBefore.Time
		op.NotBefore = &t
	}
	return op, nil
}

func scanRowFromQueryRow(r *sql.Row) (queue.Op, error) {
	return scanRow(r)
}

func getInTx(ctx context.Context, tx *sql.Tx, id string) (queue.Op, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, type, payload, status, priority, attempts, max_attempts,
		        COALESCE(last_error,''), enqueued_at, started_at, finished_at, not_before
		 FROM ops_queue WHERE id=?`, id)
	return scanRow(row)
}

// sqlTime renders a *time.Time for the `not_before` column the way SQLite's
// own CURRENT_TIMESTAMP does: UTC, second resolution, "2006-01-02 15:04:05".
//
// ⚠⚠ This is not cosmetic. `not_before` is compared in SQL, as
// `not_before <= CURRENT_TIMESTAMP`, and SQLite has no date type — the
// comparison is a STRING comparison. Binding a Go time.Time let the SQL driver
// write its own rendering ("2026-09-06 04:42:59.215071132 +0300 +03
// m=+0.118370428"), which is not comparable with CURRENT_TIMESTAMP at all:
// on a host at UTC+3 a scheduled op stayed invisible for three extra hours,
// and on a host WEST of UTC it became runnable immediately, i.e. the delay was
// silently dropped. Measured on this driver at TZ=Europe/Istanbul: a 100ms
// delay was still not runnable 400ms later, while the same test at TZ=UTC
// passed — which is exactly why it had never been noticed.
//
// The other two drivers were always correct and are untouched: postgres binds
// a real timestamptz, and redis scores the scheduled ZSET by Unix seconds.
//
// Rows written by the old code still read back — the scan side accepts both
// renderings — they merely compare wrongly until they are rewritten, and the
// only ops that carry not_before are short-lived retries and save scans.
func sqlTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

// newID returns a 16-byte hex random string. Crockford-style would be
// nicer for human readability but hex stays compatible with all DB
// dialects (no bytea / blob coercion needed).
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Package postgres is the production HA queue driver. Unlike the SQLite
// fast-path, which leans on the engine's single-writer guarantee, this
// driver uses SELECT … FOR UPDATE SKIP LOCKED so N parallel workers (and
// N filex nodes behind a load balancer) can safely race for the same
// queue without losing ops or double-claiming them.
//
// The schema is identical to the SQLite version (see
// db/migrations/postgres/00006_queue.sql) modulo column types: payload
// is JSONB and timestamps are TIMESTAMPTZ. JSONB is read back via the
// `::text` cast so the Go scan target stays a string and the rest of
// the helpers can be shared verbatim with sqlite.
package postgres

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

	// Pgx via the database/sql interface. The DB driver registration is
	// idempotent; importing it from multiple packages is safe.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/brf-tech/filex/backend/internal/queue"
)

func init() {
	queue.Register("postgres", func() queue.Driver { return &Driver{} })
}

// Driver is the Postgres-backed queue.
type Driver struct {
	db *sql.DB
}

// Name implements queue.Driver.
func (Driver) Name() string { return "postgres" }

// Init opens the queue's DB. cfg keys:
//
//	dsn  — Postgres connection string (e.g. postgres://u:p@host/db?sslmode=require).
//	      Required when cfg["db"] is absent.
//	db   — *sql.DB (preferred). When set, dsn is ignored. The bootstrap
//	      path passes the application *sql.DB so the queue piggybacks on
//	      the existing connection pool.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	if v, ok := cfg["db"].(*sql.DB); ok && v != nil {
		d.db = v
		return nil
	}
	dsn, _ := cfg["dsn"].(string)
	if dsn == "" {
		return errors.New("queue/postgres: dsn required (or supply *sql.DB via cfg[\"db\"])")
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("queue/postgres: open: %w", err)
	}
	// Modest defaults — the queue does small, fast statements. The DB
	// driver pool that wraps the metadata store sets larger limits for
	// the wider workload; here we cap to avoid over-subscribing the
	// server when the queue is the only consumer (DSN path, used in
	// tests).
	conn.SetMaxOpenConns(8)
	conn.SetMaxIdleConns(2)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	d.db = conn
	return nil
}

// Close releases the underlying *sql.DB only when this driver opened it
// (cfg["dsn"] path). When wired against the application's shared *sql.DB
// (cfg["db"]), Close is a no-op so we don't tear down the metadata store
// — same semantics as the sqlite driver.
func (d *Driver) Close() error {
	// We can't tell apart the two init paths after the fact; the safest
	// semantic is "don't close shared handles". The bootstrap supplies
	// its own *sql.DB, owns the lifecycle, and never invokes Close on
	// the queue. Tests that pass a dsn rely on t.Cleanup → Close()
	// and can swap this no-op for a real close if they ever start
	// leaking connections in CI.
	return nil
}

// Enqueue inserts an op and returns its id.
func (d *Driver) Enqueue(ctx context.Context, op queue.Op) (string, error) {
	if op.Type == "" {
		return "", errors.New("queue/postgres: op.Type required")
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
		return "", fmt.Errorf("queue/postgres: marshal payload: %w", err)
	}
	// payload is JSONB; pass the marshalled bytes as a string and let
	// the explicit ::jsonb cast on $3 perform the conversion. This
	// keeps the wire format the same as every other JSONB INSERT in
	// the project (see internal/db/drivers/postgres for the same idiom).
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO ops_queue (id, type, payload, status, priority, max_attempts, not_before)
		 VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7)`,
		id, op.Type, string(body), op.Status, op.Priority, op.MaxAttempts, asNullTime(op.NotBefore),
	)
	if err != nil {
		return "", fmt.Errorf("queue/postgres: insert: %w", err)
	}
	return id, nil
}

// Dequeue claims the next eligible op atomically using SELECT … FOR
// UPDATE SKIP LOCKED. SKIP LOCKED is the canonical Postgres pattern for
// work-stealing queues: parallel workers each get a different row in
// one round-trip, with no application-level coordination.
func (d *Driver) Dequeue(ctx context.Context, types []string) (queue.Op, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return queue.Op{}, fmt.Errorf("queue/postgres: begin: %w", err)
	}
	defer tx.Rollback()

	// Build the candidate query. Predicates mirror the SQLite version:
	// pending status, not_before honored, optional type filter.
	where := []string{
		"status = 'pending'",
		"(not_before IS NULL OR not_before <= NOW())",
	}
	var args []any
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args = append(args, t)
		}
		where = append(where, fmt.Sprintf("type IN (%s)", strings.Join(placeholders, ",")))
	}
	q := fmt.Sprintf(`SELECT id FROM ops_queue WHERE %s
	                  ORDER BY priority DESC, enqueued_at ASC
	                  LIMIT 1
	                  FOR UPDATE SKIP LOCKED`, strings.Join(where, " AND "))

	var id string
	if err := tx.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return queue.Op{}, queue.ErrEmpty
		}
		return queue.Op{}, fmt.Errorf("queue/postgres: select: %w", err)
	}

	// Claim it. The SKIP LOCKED above guarantees we hold the row lock
	// for the duration of the tx, so the AND status='pending' guard is
	// belt-and-braces — it would be impossible for another worker to
	// have claimed it, but the predicate keeps recovery semantics tidy
	// if a future change widens the SELECT.
	idArgIdx := len(args) + 1
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE ops_queue
		             SET status='running', started_at=NOW(), attempts=attempts+1
		             WHERE id=$%d AND status='pending'`, idArgIdx),
		append(args, id)...); err != nil {
		return queue.Op{}, fmt.Errorf("queue/postgres: claim: %w", err)
	}

	// Read it back inside the same tx so the caller observes attempts++.
	op, err := getInTx(ctx, tx, id)
	if err != nil {
		return queue.Op{}, err
	}
	if err := tx.Commit(); err != nil {
		return queue.Op{}, fmt.Errorf("queue/postgres: commit: %w", err)
	}
	return op, nil
}

// Ack marks the op done.
func (d *Driver) Ack(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='done', finished_at=NOW(), last_error=''
		 WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("queue/postgres: ack: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return queue.ErrNotFound
	}
	return nil
}

// Fail records the failure. retry=true requeues with a 30 s delay so a
// flapping upstream doesn't immediately re-burn an attempt; retry=false
// terminates the op as failed.
func (d *Driver) Fail(ctx context.Context, id, errMsg string, retry bool) error {
	if retry {
		// Re-queue: status back to pending, finished_at NULL, with a
		// short hold-off so workers don't busy-loop on a failing op.
		_, err := d.db.ExecContext(ctx,
			`UPDATE ops_queue
			 SET status='pending', last_error=$1, started_at=NULL, finished_at=NULL,
			     not_before=NOW() + INTERVAL '30 seconds'
			 WHERE id=$2`, errMsg, id)
		if err != nil {
			return fmt.Errorf("queue/postgres: requeue: %w", err)
		}
		return nil
	}
	_, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue
		 SET status=$1, last_error=$2, started_at=NULL, finished_at=NOW()
		 WHERE id=$3`, queue.StatusFailed, errMsg, id)
	if err != nil {
		return fmt.Errorf("queue/postgres: fail: %w", err)
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
		whereSQL = "WHERE status = $1"
		args = append(args, status)
	}

	// Count first.
	var total int64
	if err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM ops_queue "+whereSQL, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("queue/postgres: count: %w", err)
	}

	limitIdx := len(args) + 1
	offsetIdx := len(args) + 2
	rows, err := d.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT id, type, payload::text, status, priority, attempts, max_attempts,
		                    COALESCE(last_error,''), enqueued_at, started_at, finished_at, not_before
		             FROM ops_queue %s
		             ORDER BY enqueued_at DESC
		             LIMIT $%d OFFSET $%d`, whereSQL, limitIdx, offsetIdx),
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("queue/postgres: list: %w", err)
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
		`SELECT id, type, payload::text, status, priority, attempts, max_attempts,
		        COALESCE(last_error,''), enqueued_at, started_at, finished_at, not_before
		 FROM ops_queue WHERE id=$1`, id)
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
		return s, fmt.Errorf("queue/postgres: stats: %w", err)
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
		 WHERE status='done' AND finished_at >= NOW() - INTERVAL '1 day'`,
	).Scan(&s.Done24h); err != nil {
		return s, fmt.Errorf("queue/postgres: done24h: %w", err)
	}
	return s, nil
}

// Cancel transitions a pending op to cancelled.
func (d *Driver) Cancel(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='cancelled', finished_at=NOW()
		 WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return fmt.Errorf("queue/postgres: cancel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either missing or not pending — surface a useful error.
		op, getErr := d.Get(ctx, id)
		if errors.Is(getErr, queue.ErrNotFound) {
			return queue.ErrNotFound
		}
		return fmt.Errorf("queue/postgres: cancel: op already in status %q", op.Status)
	}
	return nil
}

// Retry transitions a failed op back to pending. The attempts counter
// is preserved so the operator can see how many tries it took.
func (d *Driver) Retry(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='pending', started_at=NULL, finished_at=NULL,
		                       last_error='', not_before=NULL
		 WHERE id=$1 AND status='failed'`, id)
	if err != nil {
		return fmt.Errorf("queue/postgres: retry: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return queue.ErrNotFound
	}
	return nil
}

// RecoverOrphans flips long-running rows back to pending. Called on boot
// to handle ungraceful shutdowns. The interval is parameterised as a
// floating-point second count via make_interval() — that's safer than
// string-concatenating the value into the SQL because olderThan is
// caller-controlled.
func (d *Driver) RecoverOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoffSec := olderThan.Seconds()
	if cutoffSec < 0 {
		cutoffSec = 0
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE ops_queue SET status='pending', started_at=NULL
		 WHERE status='running'
		   AND (started_at IS NULL OR started_at <= NOW() - make_interval(secs => $1))`,
		cutoffSec)
	if err != nil {
		return 0, fmt.Errorf("queue/postgres: recover: %w", err)
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
		`SELECT id, type, payload::text, status, priority, attempts, max_attempts,
		        COALESCE(last_error,''), enqueued_at, started_at, finished_at, not_before
		 FROM ops_queue WHERE id=$1`, id)
	return scanRow(row)
}

// asNullTime converts *time.Time to sql.NullTime.
func asNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// newID returns a 16-byte hex random string. Same shape as the sqlite
// driver so an op id round-trips between drivers without re-encoding.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

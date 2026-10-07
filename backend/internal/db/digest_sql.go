package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// DigestSQL implements the notification digest's half of Store (migration
// 00087, internal/notify digest.go), written ONCE for every engine and embedded
// in each driver's Store — the arrangement of DraftSQL, for the same reason
// (the duplication gate in web/tests/quality).
//
// It only reads and writes rows: which notifications are held, when a window
// ends and what a digest says are internal/notify's.
//
// ⚠ No upsert: the three engines spell it three ways. A digest point is made
// by an INSERT that may lose a race (the row is then read back), a policy is
// replaced by a DELETE and an INSERT in one transaction.
type DigestSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp: CatalogueTime for SQLite and MySQL (they compare
	// stored timestamps as text), PlainTime for PostgreSQL. Nil = CatalogueTime.
	Time func(t *time.Time) any
}

func (d *DigestSQL) q(query string) string { return rebind(d.Placeholders, query) }

func (d *DigestSQL) t(v time.Time) any {
	if d.Time != nil {
		return d.Time(&v)
	}
	return CatalogueTime(&v)
}

// digestBatch bounds one statement's placeholders well under every engine's
// limit.
const digestBatch = 400

func (d *DigestSQL) DigestState(ctx context.Context, userID int64) (int64, *time.Time, bool, error) {
	var (
		through int64
		due     sql.NullTime
	)
	err := Conn(ctx, d.Pool).QueryRowContext(ctx, d.q(
		`SELECT through_id, due_at FROM notify_digest_state WHERE user_id=?`), userID).Scan(&through, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, false, fmt.Errorf("digest state: %w", err)
	}
	if !due.Valid {
		return through, nil, true, nil
	}
	t := due.Time.UTC()
	return through, &t, true, nil
}

func (d *DigestSQL) EnsureDigestState(ctx context.Context, userID int64) (int64, error) {
	through, _, found, err := d.DigestState(ctx, userID)
	if err != nil || found {
		return through, err
	}
	newest, err := d.NewestNotificationID(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	if _, insErr := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
		`INSERT INTO notify_digest_state (user_id, through_id, updated_at) VALUES (?,?,?)`),
		userID, newest, d.t(now)); insErr != nil {
		// Another request made it first (the primary key refused this one),
		// or the account is gone (the foreign key did). Read what is there.
		through, _, found, err = d.DigestState(ctx, userID)
		if err != nil {
			return 0, err
		}
		if !found {
			return 0, fmt.Errorf("digest state: %w", insErr)
		}
		return through, nil
	}
	return newest, nil
}

func (d *DigestSQL) SetDigestDue(ctx context.Context, userID int64, due time.Time, exact bool) error {
	q := `UPDATE notify_digest_state SET due_at=? WHERE user_id=? AND (due_at IS NULL OR due_at > ?)`
	args := []any{d.t(due), userID, d.t(due)}
	if exact {
		q = `UPDATE notify_digest_state SET due_at=? WHERE user_id=?`
		args = []any{d.t(due), userID}
	}
	if _, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(q), args...); err != nil {
		return fmt.Errorf("digest due: %w", err)
	}
	return nil
}

func (d *DigestSQL) ClearDigestDue(ctx context.Context, userID int64, now time.Time) error {
	if _, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
		`UPDATE notify_digest_state SET due_at=NULL WHERE user_id=? AND due_at IS NOT NULL AND due_at <= ?`),
		userID, d.t(now)); err != nil {
		return fmt.Errorf("digest clear due: %w", err)
	}
	return nil
}

func (d *DigestSQL) DueDigests(ctx context.Context, now time.Time, limit int) ([]int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := Conn(ctx, d.Pool).QueryContext(ctx, d.q(
		`SELECT user_id FROM notify_digest_state
		  WHERE due_at IS NOT NULL AND due_at <= ?
		  ORDER BY due_at, user_id LIMIT ?`), d.t(now), limit)
	if err != nil {
		return nil, fmt.Errorf("due digests: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AdvanceDigest is a compare-and-set on through_id. ⚠ updated_at is written
// too: MySQL counts a row whose values did not change as not affected, and a
// set that does nothing else must still answer "it was mine".
func (d *DigestSQL) AdvanceDigest(ctx context.Context, userID, from, to int64, now time.Time) (bool, error) {
	res, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
		`UPDATE notify_digest_state SET through_id=?, due_at=NULL, updated_at=?
		  WHERE user_id=? AND through_id=?`), to, d.t(now), userID, from)
	if err != nil {
		return false, fmt.Errorf("digest advance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d *DigestSQL) NewestNotificationID(ctx context.Context) (int64, error) {
	var id int64
	if err := Conn(ctx, d.Pool).QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM notifications`).Scan(&id); err != nil {
		return 0, fmt.Errorf("newest notification: %w", err)
	}
	return id, nil
}

func (d *DigestSQL) OldestQuietOwn(ctx context.Context, userID, after int64, events []string) (*time.Time, error) {
	if len(events) == 0 {
		return nil, nil
	}
	args := []any{userID, after}
	for _, e := range events {
		args = append(args, e)
	}
	// ⚠ The column itself, the lowest id first — not MIN(created_at): SQLite
	// hands an aggregate back as text, which no driver turns into a time.
	var at sql.NullTime
	err := Conn(ctx, d.Pool).QueryRowContext(ctx, d.q(
		`SELECT created_at FROM notifications
		  WHERE user_id=? AND id > ? AND event IN (`+placeholders(len(events))+`)
		  ORDER BY id LIMIT 1`), args...).Scan(&at)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("oldest quiet: %w", err)
	}
	if !at.Valid {
		return nil, nil
	}
	t := at.Time.UTC()
	return &t, nil
}

func (d *DigestSQL) MarkOwnNotificationsRead(ctx context.Context, userID int64, ids []int64) error {
	for start := 0; start < len(ids); start += digestBatch {
		batch := ids[start:min(start+digestBatch, len(ids))]
		args := make([]any, 0, len(batch)+1)
		args = append(args, userID)
		for _, id := range batch {
			args = append(args, id)
		}
		if _, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
			`UPDATE notifications SET read_at = CURRENT_TIMESTAMP
			  WHERE user_id=? AND read_at IS NULL AND id IN (`+placeholders(len(batch))+`)`), args...); err != nil {
			return fmt.Errorf("mark own read: %w", err)
		}
	}
	return nil
}

func (d *DigestSQL) GetDigestPolicy(ctx context.Context, scope int64) (*model.DigestPolicy, error) {
	var (
		minutes int
		urgent  sql.NullString
		updated time.Time
	)
	err := Conn(ctx, d.Pool).QueryRowContext(ctx, d.q(
		`SELECT window_minutes, urgent_events, updated_at FROM notify_digest_policy WHERE scope_id=?`), scope).
		Scan(&minutes, &urgent, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("digest policy: %w", err)
	}
	p := &model.DigestPolicy{Scope: scope, WindowMinutes: minutes, UpdatedAt: updated.UTC()}
	if urgent.Valid && strings.TrimSpace(urgent.String) != "" {
		var list []string
		// A list nobody can read is the built-in one (nil), never "nothing is
		// urgent": the failure must not hold a security alert back.
		if json.Unmarshal([]byte(urgent.String), &list) == nil {
			p.Urgent = list
			if p.Urgent == nil {
				p.Urgent = []string{}
			}
		}
	}
	return p, nil
}

func (d *DigestSQL) SaveDigestPolicy(ctx context.Context, p *model.DigestPolicy) error {
	if p == nil {
		return errors.New("digest policy: nil")
	}
	var urgent any
	if p.Urgent != nil {
		b, err := json.Marshal(p.Urgent)
		if err != nil {
			return err
		}
		urgent = string(b)
	}
	now := time.Now().UTC()
	return RunInTx(ctx, d.Pool, func(ctx context.Context) error {
		if _, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
			`DELETE FROM notify_digest_policy WHERE scope_id=?`), p.Scope); err != nil {
			return fmt.Errorf("digest policy: %w", err)
		}
		if _, err := Conn(ctx, d.Pool).ExecContext(ctx, d.q(
			`INSERT INTO notify_digest_policy (scope_id, window_minutes, urgent_events, updated_at) VALUES (?,?,?,?)`),
			p.Scope, p.WindowMinutes, urgent, d.t(now)); err != nil {
			return fmt.Errorf("digest policy: %w", err)
		}
		return nil
	})
}

// placeholders is n comma-separated `?` (rebound per engine by q).
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// LoginThrottleSQL implements the login_throttle half of Store (migration
// 00072, internal/loginguard), written ONCE for every engine and embedded in
// each driver's Store — the arrangement of PluginRequestSQL and DraftSQL.
//
// It only reads and writes counter rows; what a count means (the limits, the
// windows, the lock arithmetic) is internal/loginguard's.
type LoginThrottleSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp: CatalogueTime for SQLite and MySQL (they compare
	// stored timestamps as text), PlainTime for PostgreSQL. Nil = CatalogueTime.
	Time func(t *time.Time) any
}

func (p *LoginThrottleSQL) q(query string) string { return rebind(p.Placeholders, query) }

func (p *LoginThrottleSQL) t(v *time.Time) any {
	if p.Time != nil {
		return p.Time(v)
	}
	return CatalogueTime(v)
}

const loginThrottleCols = `id, scope, subject, fails, lock_level, window_start, locked_until, last_fail_at, last_ip, last_protocol, updated_at`

func scanLoginThrottle(r interface{ Scan(dst ...any) error }) (*model.LoginThrottle, error) {
	var (
		out    model.LoginThrottle
		locked sql.NullTime
	)
	if err := r.Scan(&out.ID, &out.Scope, &out.Subject, &out.Fails, &out.LockLevel, &out.WindowStart,
		&locked, &out.LastFailAt, &out.LastIP, &out.LastProtocol, &out.UpdatedAt); err != nil {
		return nil, err
	}
	out.LockedUntil = nullTimePtr(locked)
	out.WindowStart = out.WindowStart.UTC()
	out.LastFailAt = out.LastFailAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return &out, nil
}

// GetLoginThrottle returns one counter, or (nil, nil) when nothing has been
// counted for that subject.
func (p *LoginThrottleSQL) GetLoginThrottle(ctx context.Context, scope, subject string) (*model.LoginThrottle, error) {
	t, err := scanLoginThrottle(Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT `+loginThrottleCols+` FROM login_throttle WHERE scope=? AND subject=?`), scope, subject))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// SaveLoginThrottle writes a counter, creating the row on first sight. The
// (scope, subject) pair is the key; ID is ignored.
//
// ⚠ UPDATE-then-INSERT and not an upsert, because the two upsert spellings
// differ per engine and this file is written once. Two writers racing on a
// brand-new subject can both reach the INSERT; the loser's unique-key error is
// answered by looking the row up — it exists, the other writer's count stands.
func (p *LoginThrottleSQL) SaveLoginThrottle(ctx context.Context, t *model.LoginThrottle) error {
	if t == nil || t.Scope == "" || t.Subject == "" {
		return errors.New("login throttle: missing scope or subject")
	}
	now := time.Now().UTC()
	window := t.WindowStart
	if window.IsZero() {
		window = now
	}
	lastFail := t.LastFailAt
	if lastFail.IsZero() {
		lastFail = now
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE login_throttle SET fails=?, lock_level=?, window_start=?, locked_until=?, last_fail_at=?, last_ip=?, last_protocol=?, updated_at=?
		  WHERE scope=? AND subject=?`),
		t.Fails, t.LockLevel, p.t(&window), p.t(t.LockedUntil), p.t(&lastFail), t.LastIP, t.LastProtocol, p.t(&now),
		t.Scope, t.Subject)
	if err != nil {
		return fmt.Errorf("update login throttle: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO login_throttle (scope, subject, fails, lock_level, window_start, locked_until, last_fail_at, last_ip, last_protocol, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`),
		t.Scope, t.Subject, t.Fails, t.LockLevel, p.t(&window), p.t(t.LockedUntil), p.t(&lastFail), t.LastIP, t.LastProtocol, p.t(&now)); err != nil {
		// A row that is already there is the race above, or an UPDATE that
		// changed nothing (MySQL counts changed rows, not matched ones).
		if existing, gerr := p.GetLoginThrottle(ctx, t.Scope, t.Subject); gerr == nil && existing != nil {
			return nil
		}
		return fmt.Errorf("insert login throttle: %w", err)
	}
	return nil
}

// DeleteLoginThrottle removes one counter and reports whether there was one.
func (p *LoginThrottleSQL) DeleteLoginThrottle(ctx context.Context, scope, subject string) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM login_throttle WHERE scope=? AND subject=?`), scope, subject)
	if err != nil {
		return false, fmt.Errorf("delete login throttle: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListLoginThrottles returns counters, the most recently active first, at most
// limit of them (<= 0: 200; never more than 1000). scope "" = every scope.
// With lockedAt non-nil only the rows locked at that instant.
func (p *LoginThrottleSQL) ListLoginThrottles(ctx context.Context, scope string, lockedAt *time.Time, limit int) ([]*model.LoginThrottle, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `SELECT ` + loginThrottleCols + ` FROM login_throttle WHERE 1=1`
	args := []any{}
	if scope != "" {
		query += ` AND scope=?`
		args = append(args, scope)
	}
	if lockedAt != nil {
		query += ` AND locked_until > ?`
		args = append(args, p.t(lockedAt))
	}
	query += ` ORDER BY last_fail_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return p.queryLoginThrottles(ctx, query, args)
}

// queryLoginThrottles runs a SELECT of loginThrottleCols and reads every row.
func (p *LoginThrottleSQL) queryLoginThrottles(ctx context.Context, query string, args []any) ([]*model.LoginThrottle, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.LoginThrottle{}
	for rows.Next() {
		t, err := scanLoginThrottle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LoadLoginThrottles returns the counters that still have something to say at
// since — a failure at or after it, or a lock that runs past it (exactly the
// rows PruneLoginThrottles(since) keeps) — the most recently active first, at
// most limit of them (<= 0: all). Unlike ListLoginThrottles it has no ceiling
// of its own: it is read once, at start, to fill the limiter's memory, and
// the limiter passes the size of that memory.
func (p *LoginThrottleSQL) LoadLoginThrottles(ctx context.Context, since time.Time, limit int) ([]*model.LoginThrottle, error) {
	query := `SELECT ` + loginThrottleCols + ` FROM login_throttle
	 WHERE last_fail_at >= ? OR (locked_until IS NOT NULL AND locked_until >= ?)
	 ORDER BY last_fail_at DESC, id DESC`
	args := []any{p.t(&since), p.t(&since)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return p.queryLoginThrottles(ctx, query, args)
}

// PruneLoginThrottles deletes counters that have had nothing to say since
// before: no failure after it and no lock still running. It answers how many.
func (p *LoginThrottleSQL) PruneLoginThrottles(ctx context.Context, before time.Time) (int64, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM login_throttle WHERE last_fail_at < ? AND (locked_until IS NULL OR locked_until < ?)`),
		p.t(&before), p.t(&before))
	if err != nil {
		return 0, fmt.Errorf("prune login throttle: %w", err)
	}
	return res.RowsAffected()
}

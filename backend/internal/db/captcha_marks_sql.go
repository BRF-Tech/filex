package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// CaptchaMarkSQL implements the captcha_marks half of Store (migration 00134,
// internal/captcha), written ONCE for every engine and embedded in each
// driver's Store - the arrangement of AppStoreSQL and LoginThrottleSQL.
//
// It only reads and writes opaque rows (a key and the unix second it ends);
// what a mark means - the outage flag, a spent token, solution or pass - is
// internal/captcha's. Times are integers, so no engine's timestamp spelling
// is involved.
type CaptchaMarkSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *CaptchaMarkSQL) q(query string) string { return rebind(p.Placeholders, query) }

// CaptchaMarkUntil answers when key's mark ends; ok is false when there is
// none (an ended one still counts as there until it is pruned: the caller
// compares the end with its clock).
func (p *CaptchaMarkSQL) CaptchaMarkUntil(ctx context.Context, key string) (int64, bool, error) {
	var until int64
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT expires_at FROM captcha_marks WHERE mark_key=?`), key).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get captcha mark: %w", err)
	}
	return until, true, nil
}

// AddCaptchaMark writes key until expires and answers true - or false, with
// nothing written, when a mark with that key is still live at now. Two
// writers racing on one key: one INSERT wins, the other is told false.
//
// ⚠ DELETE-then-INSERT and not an upsert, because the upsert spellings differ
// per engine and this file is written once. The DELETE takes only an ENDED
// row; a live one makes the INSERT fail on the key, and the loser looks the
// row up rather than guessing from an engine's error text.
func (p *CaptchaMarkSQL) AddCaptchaMark(ctx context.Context, key string, expires, now int64) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, errors.New("captcha mark: empty key")
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM captcha_marks WHERE mark_key=? AND expires_at<=?`), key, now); err != nil {
		return false, fmt.Errorf("clear ended captcha mark: %w", err)
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO captcha_marks (mark_key, expires_at) VALUES (?,?)`), key, expires); err != nil {
		until, ok, gerr := p.CaptchaMarkUntil(ctx, key)
		if gerr == nil && ok && until > now {
			return false, nil
		}
		return false, fmt.Errorf("add captcha mark: %w", err)
	}
	return true, nil
}

// PutCaptchaMark writes key until expires, over any mark it had.
func (p *CaptchaMarkSQL) PutCaptchaMark(ctx context.Context, key string, expires int64) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("captcha mark: empty key")
	}
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE captcha_marks SET expires_at=? WHERE mark_key=?`), expires, key)
	if err != nil {
		return fmt.Errorf("update captcha mark: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO captcha_marks (mark_key, expires_at) VALUES (?,?)`), key, expires); err != nil {
		// MySQL counts CHANGED rows: an UPDATE to the same value reports 0
		// and the row is there. Or another writer got there first.
		if _, ok, gerr := p.CaptchaMarkUntil(ctx, key); gerr == nil && ok {
			_, uerr := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
				`UPDATE captcha_marks SET expires_at=? WHERE mark_key=?`), expires, key)
			return uerr
		}
		return fmt.Errorf("insert captcha mark: %w", err)
	}
	return nil
}

// PruneCaptchaMarks deletes the marks that ended at or before now.
func (p *CaptchaMarkSQL) PruneCaptchaMarks(ctx context.Context, now int64) (int64, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM captcha_marks WHERE expires_at<=?`), now)
	if err != nil {
		return 0, fmt.Errorf("prune captcha marks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

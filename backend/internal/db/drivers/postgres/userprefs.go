package postgres

import (
	"context"
	"database/sql"
	"errors"
)

// ─────────────────── Per-user surface preferences (00047) ───────────────────
//
// The PostgreSQL half of GetUserPrefs / SetUserPrefs. See
// drivers/sqlite/userprefs.go for the contract and
// db/migrations/sqlite/00047_user_prefs.sql for the model.
//
// ⚠ Separate because this driver speaks `$1` and that one speaks `?`, and
// because pgx has no LastInsertId — the same split every other statement pair
// in this package lives with.

// GetUserPrefs returns the raw JSON document for one person on one surface, or
// "" when there is none. No row and an empty column are one answer; neither is
// an error.
func (s *Store) GetUserPrefs(ctx context.Context, userID int64, surface string) (string, error) {
	var v sql.NullString
	err := s.conn(ctx).QueryRowContext(ctx,
		`SELECT doc FROM user_prefs WHERE user_id=$1 AND surface=$2`, userID, surface).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// SetUserPrefs replaces the whole document for one person on one surface.
func (s *Store) SetUserPrefs(ctx context.Context, userID int64, surface, doc string) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		`INSERT INTO user_prefs (user_id, surface, doc, updated_at)
		 VALUES ($1,$2,$3,NOW())
		 ON CONFLICT(user_id, surface) DO UPDATE SET doc=EXCLUDED.doc, updated_at=NOW()`,
		userID, surface, doc)
	return err
}

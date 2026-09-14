package postgres

import (
	"context"
	"database/sql"
	"errors"
)

// ─────────────────── Per-user view preferences (00039) ───────────────────
//
// The PostgreSQL half of GetUserViewPrefs / SetUserViewPrefs. See
// drivers/sqlite/viewprefs.go for the contract and
// db/migrations/sqlite/00039_user_view_prefs.sql for the model.
//
// ⚠ Separate because this driver speaks `$1` and that one speaks `?`, and
// because pgx has no LastInsertId — the same split every other statement pair
// in this package lives with.

// GetUserViewPrefs returns the raw JSON document for a user, or "" when there
// is none. No row and an empty column are one answer; neither is an error.
func (s *Store) GetUserViewPrefs(ctx context.Context, userID int64) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT prefs_json FROM user_view_prefs WHERE user_id=$1`, userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// SetUserViewPrefs replaces the whole document for a user.
func (s *Store) SetUserViewPrefs(ctx context.Context, userID int64, doc string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_view_prefs (user_id, prefs_json, updated_at)
		 VALUES ($1,$2,NOW())
		 ON CONFLICT(user_id) DO UPDATE SET prefs_json=EXCLUDED.prefs_json, updated_at=NOW()`,
		userID, doc)
	return err
}

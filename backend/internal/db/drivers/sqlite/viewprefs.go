package sqlite

import (
	"context"
	"database/sql"
)

// ─────────────────── Per-user view preferences (00039) ───────────────────
//
// How each person left each folder: one JSON document per user, read whole and
// written whole. See db/migrations/sqlite/00039_user_view_prefs.sql for why it
// is one document rather than a row per folder, and why it is in the database
// rather than in the browser.
//
// ⚠ This file serves BOTH SQLite and MySQL — the driver carries an `s.mysql`
// flag and rewrites the upsert through `s.upsert()`. That is why the statement
// below is written in SQLite's `ON CONFLICT … DO UPDATE SET x=excluded.x`
// shape and nowhere spells `ON DUPLICATE KEY UPDATE`: `upsert()` translates
// it, and a second hand-written MySQL statement beside this one is the pair
// that drifts (issue #19 is a catalogue of exactly that).

// GetUserViewPrefs returns the raw JSON document for a user, or "" when the
// user has never stored one.
//
// ⚠ "" and "no row" are the same answer on purpose, and neither is an error:
// "this person has not arranged any folders yet" is the ordinary case for
// every account on its first day, and a caller forced to distinguish
// sql.ErrNoRows from an empty column would get it wrong once and log a scary
// line every time somebody signed in.
func (s *Store) GetUserViewPrefs(ctx context.Context, userID int64) (string, error) {
	var v sql.NullString
	err := s.conn(ctx).QueryRowContext(ctx,
		`SELECT prefs_json FROM user_view_prefs WHERE user_id=?`, userID).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// SetUserViewPrefs replaces the whole document for a user.
func (s *Store) SetUserViewPrefs(ctx context.Context, userID int64, doc string) error {
	_, err := s.conn(ctx).ExecContext(ctx,
		s.upsert(`INSERT INTO user_view_prefs (user_id, prefs_json, updated_at)
		 VALUES (?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET prefs_json=excluded.prefs_json, updated_at=CURRENT_TIMESTAMP`),
		userID, doc)
	return err
}

package sqlite

import (
	"context"
	"database/sql"
)

// ─────────────────── Per-user surface preferences (00047) ───────────────────
//
// What a person chose about the interface itself — theme, palette, density,
// language. One JSON document per person PER SURFACE ("web" / "desktop"), read
// whole and written whole. See db/migrations/sqlite/00047_user_prefs.sql for
// why this is in the database rather than in the browser (the owner picked a
// theme in one browser and the other browser did not have it) and why the two
// surfaces are separate rows.
//
// ⚠ This file serves BOTH SQLite and MySQL — the driver carries an `s.mysql`
// flag and rewrites the upsert through `s.upsert()`. That is why the statement
// below is written in SQLite's `ON CONFLICT … DO UPDATE SET x=excluded.x`
// shape and nowhere spells `ON DUPLICATE KEY UPDATE`: a second hand-written
// MySQL statement beside this one is the pair that drifts (issue #19 is a
// catalogue of exactly that).

// GetUserPrefs returns the raw JSON document for one person on one surface, or
// "" when they have never stored one.
//
// ⚠ "" and "no row" are the same answer and neither is an error: "this person
// has not chosen anything yet" is the ordinary case for every account on its
// first day, on every surface they have not opened.
func (s *Store) GetUserPrefs(ctx context.Context, userID int64, surface string) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT doc FROM user_prefs WHERE user_id=? AND surface=?`, userID, surface).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// SetUserPrefs replaces the whole document for one person on one surface.
func (s *Store) SetUserPrefs(ctx context.Context, userID int64, surface, doc string) error {
	_, err := s.db.ExecContext(ctx,
		s.upsert(`INSERT INTO user_prefs (user_id, surface, doc, updated_at)
		 VALUES (?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id, surface) DO UPDATE SET doc=excluded.doc, updated_at=CURRENT_TIMESTAMP`),
		userID, surface, doc)
	return err
}

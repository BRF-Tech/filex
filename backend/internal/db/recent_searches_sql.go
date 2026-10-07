package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/brf-tech/filex/backend/internal/model"
)

// RecentSearchSQL implements the recent-searches half of Store (migration
// 00090, task #168), written ONCE for every engine and embedded in each
// driver's Store - the arrangement of DraftSQL and LoginThrottleSQL.
//
// The order of a person's list is the row id: searching the same words again
// deletes the old row and writes a new one, so "newest first" is `id DESC` on
// every engine, with no timestamp resolution to tie on (SQLite's
// CURRENT_TIMESTAMP is whole seconds, and two searches a second apart are an
// ordinary thing to type).
type RecentSearchSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *RecentSearchSQL) q(query string) string { return rebind(p.Placeholders, query) }

const recentSearchCols = `id, user_id, surface, query_text, searched_at`

func scanRecentSearch(r interface{ Scan(dst ...any) error }) (*model.RecentSearch, error) {
	out := &model.RecentSearch{}
	if err := r.Scan(&out.ID, &out.UserID, &out.Surface, &out.Query, &out.SearchedAt); err != nil {
		return nil, err
	}
	out.SearchedAt = out.SearchedAt.UTC()
	return out, nil
}

// AddRecentSearch records that userID searched for query on surface: the row
// for the same words goes, a new one is written at the top, and the list is
// cut to the newest `keep`. It answers the new row.
//
// ⚠ Delete-then-insert rather than an upsert, because the two upsert
// spellings differ per engine and this file is written once, and because the
// new id IS the new position. Two writers racing on the same words can leave
// two rows for a moment; the next write of those words removes both.
func (p *RecentSearchSQL) AddRecentSearch(ctx context.Context, userID int64, surface, query string, keep int) (*model.RecentSearch, error) {
	if userID == 0 || surface == "" || query == "" {
		return nil, errors.New("recent search: missing user, surface or query")
	}
	conn := Conn(ctx, p.Pool)
	if _, err := conn.ExecContext(ctx, p.q(
		`DELETE FROM recent_searches WHERE user_id=? AND surface=? AND query_text=?`), userID, surface, query); err != nil {
		return nil, fmt.Errorf("drop earlier recent search: %w", err)
	}
	if _, err := conn.ExecContext(ctx, p.q(
		`INSERT INTO recent_searches (user_id, surface, query_text) VALUES (?,?,?)`), userID, surface, query); err != nil {
		return nil, fmt.Errorf("insert recent search: %w", err)
	}
	// Read back by the newest row of these words: PostgreSQL has no
	// LastInsertId, MySQL no RETURNING.
	row, err := scanRecentSearch(conn.QueryRowContext(ctx, p.q(
		`SELECT `+recentSearchCols+` FROM recent_searches WHERE user_id=? AND surface=? AND query_text=? ORDER BY id DESC LIMIT 1`),
		userID, surface, query))
	if err != nil {
		return nil, fmt.Errorf("read recent search back: %w", err)
	}
	if keep > 0 {
		if err := p.trimRecentSearches(ctx, userID, surface, keep); err != nil {
			return nil, err
		}
	}
	return row, nil
}

// trimRecentSearches drops everything past the newest `keep` rows of one
// person's list. The ids are read and deleted one by one: MySQL refuses both
// a LIMIT inside an IN subquery and a DELETE that reads its own table, and a
// list one longer than its limit (the usual case here) is one statement.
func (p *RecentSearchSQL) trimRecentSearches(ctx context.Context, userID int64, surface string, keep int) error {
	conn := Conn(ctx, p.Pool)
	rows, err := conn.QueryContext(ctx, p.q(
		`SELECT id FROM recent_searches WHERE user_id=? AND surface=? ORDER BY id DESC`), userID, surface)
	if err != nil {
		return fmt.Errorf("list recent searches to trim: %w", err)
	}
	var stale []int64
	n := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		n++
		if n > keep {
			stale = append(stale, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := conn.ExecContext(ctx, p.q(`DELETE FROM recent_searches WHERE id=? AND user_id=?`), id, userID); err != nil {
			return fmt.Errorf("trim recent searches: %w", err)
		}
	}
	return nil
}

// ListRecentSearches answers one person's list on one surface, newest first,
// at most limit rows (limit <= 0: all of them).
func (p *RecentSearchSQL) ListRecentSearches(ctx context.Context, userID int64, surface string, limit int) ([]*model.RecentSearch, error) {
	query := `SELECT ` + recentSearchCols + ` FROM recent_searches WHERE user_id=? AND surface=? ORDER BY id DESC`
	args := []any{userID, surface}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(query), args...)
	if err != nil {
		return nil, fmt.Errorf("list recent searches: %w", err)
	}
	defer rows.Close()
	out := []*model.RecentSearch{}
	for rows.Next() {
		r, err := scanRecentSearch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRecentSearch removes one row of userID's own list; ok is false when
// there was no such row OF THEIRS - somebody else's id is not theirs to
// remove, and is answered exactly like an id that does not exist.
func (p *RecentSearchSQL) DeleteRecentSearch(ctx context.Context, userID, id int64) (bool, error) {
	// Looked up first rather than read off RowsAffected: MySQL reports
	// CHANGED rows, and the answer must not depend on the engine.
	var found int64
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT id FROM recent_searches WHERE id=? AND user_id=?`), id, userID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find recent search: %w", err)
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM recent_searches WHERE id=? AND user_id=?`), id, userID); err != nil {
		return false, fmt.Errorf("delete recent search: %w", err)
	}
	return true, nil
}

// ClearRecentSearches empties userID's list on one surface.
func (p *RecentSearchSQL) ClearRecentSearches(ctx context.Context, userID int64, surface string) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM recent_searches WHERE user_id=? AND surface=?`), userID, surface); err != nil {
		return fmt.Errorf("clear recent searches: %w", err)
	}
	return nil
}

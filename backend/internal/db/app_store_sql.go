package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppStoreSQL implements the app_store_state half of Store (migration 00081,
// internal/appstore), written ONCE for every engine and embedded in each
// driver's Store - the arrangement of LoginThrottleSQL and PluginRequestSQL.
//
// It only reads and writes opaque rows (a key and a JSON value); what a row
// means - a trusted store, a license, a used install link - is
// internal/appstore's.
type AppStoreSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
	// Time binds a timestamp: CatalogueTime for SQLite and MySQL, PlainTime
	// for PostgreSQL. Nil = CatalogueTime.
	Time func(t *time.Time) any
}

func (p *AppStoreSQL) q(query string) string { return rebind(p.Placeholders, query) }

func (p *AppStoreSQL) t(v *time.Time) any {
	if p.Time != nil {
		return p.Time(v)
	}
	return CatalogueTime(v)
}

// GetAppStoreState returns one row's value; ok is false when there is none.
func (p *AppStoreSQL) GetAppStoreState(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT state_value FROM app_store_state WHERE state_key=?`), key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get app store state: %w", err)
	}
	return v, true, nil
}

// PutAppStoreState writes one row, creating it on first sight.
//
// ⚠ UPDATE-then-INSERT and not an upsert, because the upsert spellings differ
// per engine and this file is written once (LoginThrottleSQL). The loser of
// two writers racing on a new key is answered by writing over the winner.
func (p *AppStoreSQL) PutAppStoreState(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("app store state: empty key")
	}
	now := time.Now().UTC()
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE app_store_state SET state_value=?, updated_at=? WHERE state_key=?`), value, p.t(&now), key)
	if err != nil {
		return fmt.Errorf("update app store state: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO app_store_state (state_key, state_value, updated_at) VALUES (?,?,?)`), key, value, p.t(&now)); err != nil {
		// An UPDATE that changed nothing (MySQL counts changed rows, not
		// matched ones) or a writer that got there first: the row is there.
		if cur, ok, gerr := p.GetAppStoreState(ctx, key); gerr == nil && ok {
			if cur == value {
				return nil
			}
			_, uerr := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
				`UPDATE app_store_state SET state_value=?, updated_at=? WHERE state_key=?`), value, p.t(&now), key)
			return uerr
		}
		return fmt.Errorf("insert app store state: %w", err)
	}
	return nil
}

// DeleteAppStoreState removes one row and reports whether there was one.
func (p *AppStoreSQL) DeleteAppStoreState(ctx context.Context, key string) (bool, error) {
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM app_store_state WHERE state_key=?`), key)
	if err != nil {
		return false, fmt.Errorf("delete app store state: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListAppStoreState returns every row whose key starts with prefix, by key.
//
// The prefix is compared in Go, not with LIKE: a key holds `_` and `%`
// (an origin, an app name) that LIKE would read as wildcards, and the table
// is a handful of rows.
func (p *AppStoreSQL) ListAppStoreState(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, p.q(
		`SELECT state_key, state_value FROM app_store_state ORDER BY state_key`))
	if err != nil {
		return nil, fmt.Errorf("list app store state: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, rows.Err()
}

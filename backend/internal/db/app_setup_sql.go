package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AppSetupSQL implements the app_plugin_setup half of Store (migration
// 00138, internal/wasmplugin setup.go), written ONCE for every engine and
// embedded in each driver's Store - the arrangement of CaptchaMarkSQL.
//
// The row is the host's own record of an app's setup (the addresses of its
// routes, whether the wizard is done) as an opaque JSON document; what it
// means is wasmplugin's. The app's data is not here: it is kept outside
// filex's database (internal/appdata).
type AppSetupSQL struct {
	Pool *sql.DB
	// Placeholders turns `?` into the engine's own (PostgreSQL:
	// DollarPlaceholders). Nil for SQLite and MySQL.
	Placeholders func(q string) string
}

func (p *AppSetupSQL) q(query string) string { return rebind(p.Placeholders, query) }

// GetAppSetup answers an app's setup record ("" and false for none).
func (p *AppSetupSQL) GetAppSetup(ctx context.Context, app string) (string, bool, error) {
	var doc string
	err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT record_json FROM app_plugin_setup WHERE app=?`), app).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get app setup: %w", err)
	}
	return doc, true, nil
}

// PutAppSetup writes an app's setup record, over the one it had.
//
// ⚠ UPDATE-then-INSERT and not an upsert, because the upsert spellings differ
// per engine and this file is written once; the record has one writer at a
// time (the registry serialises an app's setup).
func (p *AppSetupSQL) PutAppSetup(ctx context.Context, app, doc string) error {
	now := time.Now().Unix()
	res, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`UPDATE app_plugin_setup SET record_json=?, updated_at=? WHERE app=?`), doc, now, app)
	if err != nil {
		return fmt.Errorf("update app setup: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`INSERT INTO app_plugin_setup (app, record_json, updated_at) VALUES (?,?,?)`), app, doc, now); err != nil {
		return fmt.Errorf("add app setup: %w", err)
	}
	return nil
}

// DeleteAppSetup forgets an app's setup (Reconfigure, removal).
func (p *AppSetupSQL) DeleteAppSetup(ctx context.Context, app string) error {
	if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(
		`DELETE FROM app_plugin_setup WHERE app=?`), app); err != nil {
		return fmt.Errorf("delete app setup: %w", err)
	}
	return nil
}

// ListAppSetups answers every app's setup record, by app.
func (p *AppSetupSQL) ListAppSetups(ctx context.Context) (map[string]string, error) {
	rows, err := Conn(ctx, p.Pool).QueryContext(ctx, `SELECT app, record_json FROM app_plugin_setup`)
	if err != nil {
		return nil, fmt.Errorf("list app setups: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var app, doc string
		if err := rows.Scan(&app, &doc); err != nil {
			return nil, err
		}
		out[app] = doc
	}
	return out, rows.Err()
}

// AppPluginKept counts what filex keeps for an app in its own tables: its
// per-file state rows and its locks (Reconfigure's warning).
func (p *AppSetupSQL) AppPluginKept(ctx context.Context, pluginID int64) (stateRows, locks int64, err error) {
	if err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT COUNT(*) FROM app_plugin_state WHERE plugin_id=?`), pluginID).Scan(&stateRows); err != nil {
		return 0, 0, fmt.Errorf("count app state: %w", err)
	}
	if err := Conn(ctx, p.Pool).QueryRowContext(ctx, p.q(
		`SELECT COUNT(*) FROM app_plugin_locks WHERE plugin_id=?`), pluginID).Scan(&locks); err != nil {
		return 0, 0, fmt.Errorf("count app locks: %w", err)
	}
	return stateRows, locks, nil
}

// EraseAppPluginKept deletes an app's per-file state, its locks and the work
// it scheduled (Reconfigure: the app stays installed, its record starts
// again). Its settings and overrides are the administrator's and stay.
func (p *AppSetupSQL) EraseAppPluginKept(ctx context.Context, pluginID int64) error {
	for _, q := range []string{
		`DELETE FROM app_plugin_state WHERE plugin_id=?`,
		`DELETE FROM app_plugin_locks WHERE plugin_id=?`,
		`DELETE FROM app_plugin_schedule WHERE plugin_id=?`,
	} {
		if _, err := Conn(ctx, p.Pool).ExecContext(ctx, p.q(q), pluginID); err != nil {
			return fmt.Errorf("erase app data: %w", err)
		}
	}
	return nil
}

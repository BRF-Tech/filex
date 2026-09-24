package sqlite

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Operator-defined themes (00051) ───────────────────
//
// A name plus two maps of `--fe-*` custom properties. See
// db/migrations/sqlite/00051_custom_themes.sql for why this is a table and not
// a settings row, and internal/db/customtheme_scan.go for the row handling —
// only the SQL text lives here, because only the SQL text has a dialect.
//
// ⚠ This file serves BOTH SQLite and MySQL: the driver carries an `s.mysql`
// flag and rewrites the upsert through `s.upsert()`. The statement below is
// therefore written in SQLite's `ON CONFLICT … DO UPDATE SET x=excluded.x`
// shape and nowhere spells `ON DUPLICATE KEY UPDATE` — `upsert()` translates
// it, and a second hand-written MySQL statement beside this one is the pair
// that drifts (issue #19 is a catalogue of exactly that).

// ListCustomThemes returns every stored theme, oldest first.
//
// ⚠ Ordered by id and not by name: this is the order the palette cards appear
// in, and re-sorting the moment somebody renames a theme moves the card a
// person was about to click.
func (s *Store) ListCustomThemes(ctx context.Context) ([]*model.CustomTheme, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+db.CustomThemeColumns+` FROM custom_themes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return db.ScanCustomThemes(rows)
}

// GetCustomTheme returns one theme by slug, or (nil, nil) when there is none.
func (s *Store) GetCustomTheme(ctx context.Context, key string) (*model.CustomTheme, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+db.CustomThemeColumns+` FROM custom_themes WHERE theme_key=?`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return db.ScanCustomTheme(rows), rows.Err()
}

// UpsertCustomTheme creates or replaces a theme, keyed on its slug.
func (s *Store) UpsertCustomTheme(ctx context.Context, t *model.CustomTheme) error {
	light, dark, err := db.CustomThemeJSON(t)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		s.upsert(`INSERT INTO custom_themes (theme_key, name, tokens_light, tokens_dark, created_at, updated_at)
		 VALUES (?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
		 ON CONFLICT(theme_key) DO UPDATE SET name=excluded.name, tokens_light=excluded.tokens_light,
		   tokens_dark=excluded.tokens_dark, updated_at=CURRENT_TIMESTAMP`),
		t.Key, t.Name, light, dark)
	return err
}

// DeleteCustomTheme removes a theme, reporting whether a row was there.
func (s *Store) DeleteCustomTheme(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM custom_themes WHERE theme_key=?`, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

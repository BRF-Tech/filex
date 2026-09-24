package postgres

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Operator-defined themes (00051) ───────────────────
//
// The PostgreSQL half. See drivers/sqlite/customthemes.go for the contract and
// internal/db/customtheme_scan.go for the row handling, which is shared: only
// the SQL text is per-dialect, and only the SQL text is here.
//
// ⚠ Separate at all because this driver speaks `$1` and that one speaks `?` —
// the same split every other statement pair in this package lives with.

// ListCustomThemes returns every stored theme, oldest first.
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
		`SELECT `+db.CustomThemeColumns+` FROM custom_themes WHERE theme_key=$1`, key)
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
		`INSERT INTO custom_themes (theme_key, name, tokens_light, tokens_dark, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,NOW(),NOW())
		 ON CONFLICT(theme_key) DO UPDATE SET name=EXCLUDED.name, tokens_light=EXCLUDED.tokens_light,
		   tokens_dark=EXCLUDED.tokens_dark, updated_at=NOW()`,
		t.Key, t.Name, light, dark)
	return err
}

// DeleteCustomTheme removes a theme, reporting whether a row was there.
func (s *Store) DeleteCustomTheme(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM custom_themes WHERE theme_key=$1`, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

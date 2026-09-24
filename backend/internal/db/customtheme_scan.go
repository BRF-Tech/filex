package db

import (
	"database/sql"
	"encoding/json"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ─────────────────── Operator-defined themes: the dialect-free half ────────
//
// The two drivers (drivers/sqlite, which MySQL shares, and drivers/postgres)
// have to spell the SQL twice — one speaks `?` and the other `$1`, and the
// repo's standing decision is that a query builder which can silently emit the
// wrong dialect is worse than two files that read alike.
//
// ⚠ NONE OF THAT APPLIES TO THE ROW ITSELF. Scanning seven columns and parsing
// two JSON documents has no dialect in it, so a copy in each driver would be
// pure duplication — the kind `web/tests/quality/duplication.test.ts` exists to
// refuse, and did refuse when this was first written that way. It lives here,
// in the package both drivers already import to implement Store.

// CustomThemeColumns is the select list both drivers use, in the order
// ScanCustomTheme reads them.
//
// ⚠ The order is a contract between this constant and the function below. They
// are next to each other so a column added to one is added to the other in the
// same edit; split across packages, a mismatch would scan the name into the
// light-token field and fail with a JSON error naming neither.
const CustomThemeColumns = `id, theme_key, name, tokens_light, tokens_dark, created_at, updated_at`

// ScanCustomTheme turns the current row into a model, or nil when the row
// cannot be read.
//
// ⚠ Unparseable is nil, NOT an error. This runs on the public appearance path
// that the login page and every share page read; a row hand-edited into the
// database, or written by a future version with a shape this one cannot read,
// must cost the operator that one theme rather than the whole instance.
func ScanCustomTheme(rows *sql.Rows) *model.CustomTheme {
	var (
		t         model.CustomTheme
		lightJSON string
		darkJSON  string
	)
	if err := rows.Scan(&t.ID, &t.Key, &t.Name, &lightJSON, &darkJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil
	}
	if err := json.Unmarshal([]byte(lightJSON), &t.TokensLight); err != nil {
		return nil
	}
	if err := json.Unmarshal([]byte(darkJSON), &t.TokensDark); err != nil {
		return nil
	}
	return &t
}

// ScanCustomThemes drains a result set, dropping rows that cannot be read.
func ScanCustomThemes(rows *sql.Rows) ([]*model.CustomTheme, error) {
	out := []*model.CustomTheme{}
	for rows.Next() {
		if t := ScanCustomTheme(rows); t != nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// CustomThemeJSON renders a theme's two token maps for storage.
func CustomThemeJSON(t *model.CustomTheme) (light string, dark string, err error) {
	l, err := json.Marshal(t.TokensLight)
	if err != nil {
		return "", "", err
	}
	d, err := json.Marshal(t.TokensDark)
	if err != nil {
		return "", "", err
	}
	return string(l), string(d), nil
}

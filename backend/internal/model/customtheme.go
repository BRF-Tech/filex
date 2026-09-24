package model

import "time"

// CustomTheme is an operator-defined palette — migration 00051.
//
// It is the same shape as a built-in palette in
// packages/core/src/lib/themes.ts (`ThemeDef`): a name and two maps of
// `--fe-*` custom properties, one per light/dark variant. The browser is
// handed built-ins and custom themes in one list and cannot tell them apart
// beyond the `custom:` prefix on the id.
//
// ⚠ Key is the operator's slug WITHOUT the prefix (what is stored in
// custom_themes.theme_key). The palette id the browser persists is
// `custom:<Key>` — see handlers.CustomThemePrefix. Keeping the prefix out of
// the column means the slug pattern is the only thing the database has to
// agree with, and a built-in id can never be typed into it by accident.
type CustomTheme struct {
	ID          int64             `json:"-"`
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	TokensLight map[string]string `json:"light"`
	TokensDark  map[string]string `json:"dark"`
	CreatedAt   time.Time         `json:"-"`
	UpdatedAt   time.Time         `json:"-"`
}

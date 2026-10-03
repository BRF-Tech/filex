package db

import (
	"context"
	"database/sql"
)

// ListUserPrefs returns every preference document one person stored (00047),
// newest first, each with when it was last written.
//
// It rides on FileAssocSQL, the shared statement set every driver embeds,
// because it serves the same 0.50 feature: the account-wide "open with"
// choices (handlers/openwith.go) are adopted once from the most recently
// written surface document, and only this listing knows which one that is.
// One statement for every engine: it only reads, and `?` is rebound for
// PostgreSQL.
func (f *FileAssocSQL) ListUserPrefs(ctx context.Context, userID int64) ([]UserPrefsDoc, error) {
	rows, err := Conn(ctx, f.Pool).QueryContext(ctx, f.q(
		`SELECT surface, doc, updated_at FROM user_prefs WHERE user_id=? ORDER BY updated_at DESC, surface`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserPrefsDoc{}
	for rows.Next() {
		var (
			d   UserPrefsDoc
			doc sql.NullString
		)
		if err := rows.Scan(&d.Surface, &doc, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Doc = doc.String
		d.UpdatedAt = d.UpdatedAt.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

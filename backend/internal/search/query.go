package search

import "strings"

// SQLLike returns a SQL LIKE pattern from a free-text query, with all
// wildcards escaped to literal characters except a single trailing %.
//
// Used as the SQL fallback path when the Bleve index is disabled or
// unavailable.
func SQLLike(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return "%"
	}
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, "%", `\%`)
	q = strings.ReplaceAll(q, "_", `\_`)
	return "%" + q + "%"
}

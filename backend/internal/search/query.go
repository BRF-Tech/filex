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

// Fallback is how a query is answered WITHOUT the Bleve index.
//
// The index-less install is a different code path, not a different
// product: an operator running with FILEX_SEARCH_ENABLED=false should
// see `invoice 2026` find `invoice_2026.pdf` exactly like everybody
// else. A LIKE cannot do that on its own — `%invoice 2026%` matches no
// filename that used a different separator, which is the whole bug from
// issue #15 — and the store's SearchNodes takes ONE pattern, so the
// separator-blind part happens in two steps:
//
//  1. Like is the single most selective word, sent to the database.
//  2. Accepts re-checks the FULL query against each returned row in
//     normalised form, so the remaining words still have to match.
//
// What the fallback deliberately does NOT get is typo tolerance: edit
// distance is not something a LIKE can express, and faking it with more
// patterns would turn one scan into many. Said out loud in
// docs/SEARCH.md rather than left for somebody to discover.
type Fallback struct {
	// Like is the pattern to hand to Store.SearchNodes.
	Like string
	// Anchor is the bare word inside Like, for callers whose store
	// wrapper builds the % itself (the AI surface does).
	Anchor string
	// Words is every normalised query word; a row must contain them all.
	Words []string
}

// PlanFallback builds the two-step plan described on Fallback.
func PlanFallback(query string) Fallback {
	words := NormWords(query)
	if len(words) == 0 {
		// Nothing alphanumeric to anchor on (`***`, `---`). Keep the
		// historical behaviour rather than inventing one.
		return Fallback{Like: SQLLike(query)}
	}
	anchor := words[0]
	for _, w := range words[1:] {
		if len(w) > len(anchor) {
			anchor = w
		}
	}
	return Fallback{Like: SQLLike(anchor), Anchor: anchor, Words: words}
}

// Accepts reports whether a row the database returned really satisfies
// the whole query. Name first, then name+path, mirroring the index side
// where a folder match is a (lower-ranked) hit too.
func (f Fallback) Accepts(name, path string) bool {
	if len(f.Words) == 0 {
		return true
	}
	if containsAll(Normalize(name), f.Words) {
		return true
	}
	return containsAll(Normalize(name)+" "+Normalize(path), f.Words)
}

// FallbackOverFetch is how many times `limit` rows a caller should ask
// the database for, since Accepts drops some of them afterwards.
const FallbackOverFetch = 4

package search

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
)

// Fallback is how a query is answered WITHOUT the Bleve index.
//
// The index-less install is a different code path, not a different
// product: an operator running with FILEX_SEARCH_ENABLED=false should
// see `invoice 2026` find `invoice_2026.pdf` exactly like everybody
// else. A LIKE cannot do that on its own — `%invoice 2026%` matches no
// filename that used a different separator, which is the whole bug from
// issue #15 — so the separator-blind part happens in two steps:
//
//  1. Candidates asks the database for the rows whose NAME holds EVERY word
//     of the query (Store.SearchNodes), compared the way internal/namefold
//     compares — composed, the four i's one letter, case folded — on the
//     stored side as on this one, and ranked before the LIMIT.
//  2. Accepts re-checks the FULL query against each returned row with the
//     same scorer the index path uses, which also ranks it.
//
// ⚠ Step 1 used to send only the longest word and leave the others to
// step 2. The database then returned the first rows by name that held that
// one word — at most a thousand of them — and a row answering the whole
// query but sorting after them was never looked at. Measured on a
// production catalogue on 2026-09-24 (GitHub PR #46): the longest word of a
// typed filename was the prefix every file in the storage carried, it was in
// 64 483 of 169 471 names, and the two files being looked for sat at rows
// 4 128 and 33 623. So the more of a name somebody typed, the less they
// found; the file's own name, pasted byte for byte, found nothing. With
// every word in the query, the LIMIT counts rows that can actually answer it.
//
// ⚠ Every word is matched against the NAME, never the path. The name is
// in an index (storage_id, parent_id, name), so SQLite rejects a row
// without reading it; a condition on the path reads every candidate row
// from the table. Measured on the same catalogue: an eleven-word query took
// 1.06 s with the words matched against the path and 0.12 s against the
// name. A word that appears only in a folder name is therefore not found
// without the index — the anchor was matched against the name before too.
//
// What the fallback deliberately does NOT get is typo tolerance: edit
// distance is not something a LIKE can express, and faking it with more
// patterns would turn one scan into many. Said out loud in
// docs/SEARCH.md rather than left for somebody to discover.
type Fallback struct {
	// Words is every normalised query word, each once; a row must contain
	// them all. Empty for a query with no letter or digit in it (`***`).
	Words []string
	// match is what the database is asked for (see PlanFallback).
	match model.NameMatch
	// query is the same PreparedQuery the index path scores with. Step 2
	// runs the SAME scorer as the index, so a row that survives here
	// would have survived there and is ranked into the same tier. A
	// fallback that ranked differently from the index would be a support
	// burden: two installs of the same version, one with the index
	// switched off, disagreeing about which file is the best match.
	query PreparedQuery
}

// maxFallbackTerms caps the conditions one query sends the database — words
// and runs together. A query is not a document, and a pasted page would
// otherwise become hundreds of conditions (and, on MySQL, two placeholders a
// word). The words past the cap are the shortest, the least likely to narrow
// anything, and dropping them only widens what the database returns: the
// scorer still requires every word.
const maxFallbackTerms = 32

// PlanFallback builds the two-step plan described on Fallback.
//
// The database is asked for three things (model.NameMatch):
//
//   - Words: every word, folded (namefold), longest first — a row is turned
//     away by the first word it lacks, and a long word is the likelier one
//     to be rare.
//   - Runs: for each word that is not all plain letters (namefold.AllPlain —
//     those the store looks for natively as they are), the longest run of
//     letters and digits that every stored spelling of it holds as it is
//     (sharedRun: `arch` for `archive`, `rel` for `gürel`). The engine checks
//     those with its own LIKE on the stored bytes before the costlier
//     comparison through the normaliser: on SQLite that comparison is a Go
//     function, a few microseconds a row, and the run turns most rows away
//     before it is called.
//   - Prefer: the longest word, to rank the rows before the LIMIT — names
//     that are the word, then names starting with it.
//
// A query with no letter or digit in it (`***`, `---`) asks for the query
// itself, trimmed, as one piece of text.
func PlanFallback(query string) Fallback {
	words := uniqueWords(NormWords(query))
	if len(words) == 0 {
		t := namefold.String(strings.TrimSpace(query))
		if t == "" {
			return Fallback{}
		}
		return Fallback{match: model.NameMatch{Words: []string{t}}}
	}
	longestFirst := func(s []string) {
		sort.SliceStable(s, func(a, b int) bool {
			return utf8.RuneCountInString(s[a]) > utf8.RuneCountInString(s[b])
		})
	}
	sent := append([]string(nil), words...)
	longestFirst(sent)
	if len(sent) > maxFallbackTerms {
		sent = sent[:maxFallbackTerms]
	}
	var runs []string
	seen := map[string]bool{}
	for _, w := range sent {
		if namefold.AllPlain(w) {
			continue // the word is its own run: the store looks for it natively
		}
		if run := sharedRun(w); len(run) >= 2 && !seen[run] {
			seen[run] = true
			runs = append(runs, run)
		}
	}
	longestFirst(runs)
	if room := maxFallbackTerms - len(sent); len(runs) > room {
		runs = runs[:room]
	}
	return Fallback{
		Words: words,
		match: model.NameMatch{Words: sent, Runs: runs, Prefer: sent[0]},
		query: PrepareQuery(query),
	}
}

// NodeSearcher is the part of the store a fallback runs against. It is an
// interface for the same reason NodeLister is: this package cannot import
// the store.
type NodeSearcher interface {
	SearchNodes(ctx context.Context, storageID int64, m model.NameMatch, limit int) ([]*model.Node, error)
}

// Candidates fetches the rows of one storage that step 2 has to look at.
// Every caller goes through here — the toolbar search, /api/files/search and
// the AI/MCP name search — so no surface can go back to asking the database
// for one word.
func (f Fallback) Candidates(ctx context.Context, store NodeSearcher, storageID int64, limit int) ([]*model.Node, error) {
	if len(f.match.Words) == 0 {
		return nil, nil
	}
	return store.SearchNodes(ctx, storageID, f.match, limit)
}

// sharedRun is the longest run of a folded word that every stored spelling
// of it holds byte for byte, as far as an engine's case-insensitive LIKE can
// tell: letters and digits namefold.Plain vouches for. For `archive` it is
// `arch` (an `i` may be stored `İ` or `ı`); for `gürel`, `rel` (a Mac stores
// `ü` as `u` + a mark); a word of Turkish letters may have none.
func sharedRun(word string) string {
	best, start := "", -1
	for i, r := range word + "\x00" {
		if r != 0 && namefold.Plain(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start > len(best) {
			best = word[start:i]
		}
		start = -1
	}
	return best
}

// uniqueWords drops repeats, keeping the first occurrence of each word.
func uniqueWords(words []string) []string {
	seen := make(map[string]bool, len(words))
	out := words[:0:0]
	for _, w := range words {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// Accepts reports whether a row the database returned really satisfies
// the whole query — the subsequence scorer's verdict, so the filename
// and the folders above it are weighed exactly as they are on the index
// path and a row every piece cannot answer is dropped.
func (f Fallback) Accepts(name, path string) bool {
	if len(f.Words) == 0 {
		return true
	}
	return f.query.ScoreName(name, path).OK
}

// Rank is the tier a fallback row belongs in, for callers that sort. It
// is RankName by another name; it exists so a caller does not have to
// re-prepare the query per row.
func (f Fallback) Rank(name, path string) Tier {
	if len(f.Words) == 0 {
		return TierName
	}
	return f.query.Rank(name, path)
}

// FallbackOverFetch is how many times `limit` rows a caller should ask
// the database for, since Accepts drops some of them afterwards.
const FallbackOverFetch = 4

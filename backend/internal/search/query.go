package search

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/model"
)

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
// issue #15 — so the separator-blind part happens in two steps:
//
//  1. Terms puts EVERY word of the query in the database query
//     (Store.SearchNodesAll), each one in the spellings a name can be
//     stored under: a row's name has to hold all of them.
//  2. Accepts re-checks the FULL query against each returned row with the
//     same scorer the index path uses, which also ranks it.
//
// ⚠ Step 1 used to send only the longest word and leave the others to
// step 2. The database then returned the first rows by name that held that
// one word — at most a thousand of them — and a row answering the whole
// query but sorting after them was never looked at. Measured on a
// production catalogue on 2026-09-24: the longest word of a typed filename
// was the prefix every file in the storage carried, it was in 64 483 of
// 169 471 names, and the two files being looked for sat at rows 4 128 and
// 33 623. So the more of a name somebody typed, the less they found; the
// file's own name, pasted byte for byte, found nothing. With every word in
// the query, the LIMIT counts rows that can actually answer it.
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
	// Like is the single pattern for a query with no word in it at all
	// (`***`), handed to Store.SearchNodes. Unset otherwise.
	Like string
	// Words is every normalised query word, each once; a row must contain
	// them all.
	Words []string
	// Terms is what the database is asked for: a row's name has to match
	// one pattern of every term. Each word is a term holding a pattern per
	// spelling (see spellings); a word with several spellings also adds a
	// one-pattern term that all of them contain (see sharedRun), which lets
	// the database turn most rows away with a single comparison. The cheap
	// terms come first — see PlanFallback.
	Terms [][]string
	// query is the same PreparedQuery the index path scores with. Step 2
	// runs the SAME scorer as the index, so a row that survives here
	// would have survived there and is ranked into the same tier. A
	// fallback that ranked differently from the index would be a support
	// burden: two installs of the same version, one with the index
	// switched off, disagreeing about which file is the best match.
	query PreparedQuery
}

// maxFallbackTerms caps what one query sends the database. A query is not a
// document: a word used to cost one placeholder at most and now costs up to
// seven, and SQLite refuses a statement with more than 32 766 of them, so a
// pasted page would have turned the search into an error. The terms past
// the cap are the costliest ones (see PlanFallback's order), and dropping
// them only widens what the database returns — the scorer still requires
// every word.
const maxFallbackTerms = 32

// PlanFallback builds the two-step plan described on Fallback.
func PlanFallback(query string) Fallback {
	words := uniqueWords(NormWords(query))
	if len(words) == 0 {
		// Nothing alphanumeric to anchor on (`***`, `---`). Keep the
		// historical behaviour rather than inventing one.
		return Fallback{Like: SQLLike(query)}
	}
	type term struct {
		patterns []string
		size     int // the text a pattern must contain, in runes
	}
	terms := make([]term, 0, 2*len(words))
	for _, w := range words {
		sp := spellings(w)
		terms = append(terms, term{sp, utf8.RuneCountInString(w)})
		if len(sp) > 1 {
			if run := sharedRun(w); len(run) >= 2 {
				terms = append(terms, term{[]string{SQLLike(run)}, len(run)})
			}
		}
	}
	// The order is the work, not the result: every term is required. A
	// row is rejected by the first term it fails, and a term costs one
	// comparison per pattern, so one-pattern terms go first and, among
	// equals, the longer text — more likely to be rare. Measured on the
	// catalogue above: 0.26 s longest-word-first, 0.12 s this way.
	sort.SliceStable(terms, func(a, b int) bool {
		if len(terms[a].patterns) != len(terms[b].patterns) {
			return len(terms[a].patterns) < len(terms[b].patterns)
		}
		return terms[a].size > terms[b].size
	})
	if len(terms) > maxFallbackTerms {
		terms = terms[:maxFallbackTerms]
	}
	out := make([][]string, 0, len(terms))
	for _, t := range terms {
		out = append(out, t.patterns)
	}
	return Fallback{Words: words, Terms: out, query: PrepareQuery(query)}
}

// NodeSearcher is the part of the store a fallback runs against. It is an
// interface for the same reason NodeLister is: this package cannot import
// the store.
type NodeSearcher interface {
	SearchNodes(ctx context.Context, storageID int64, like string, limit int) ([]*model.Node, error)
	SearchNodesAll(ctx context.Context, storageID int64, terms [][]string, limit int) ([]*model.Node, error)
}

// Candidates fetches the rows of one storage that step 2 has to look at.
// Every caller goes through here, so no surface can go back to asking the
// database for one word.
func (f Fallback) Candidates(ctx context.Context, store NodeSearcher, storageID int64, limit int) ([]*model.Node, error) {
	if len(f.Terms) == 0 {
		return store.SearchNodes(ctx, storageID, f.Like, limit)
	}
	return store.SearchNodesAll(ctx, storageID, f.Terms, limit)
}

// spellings returns a LIKE pattern for every way one query word can be
// stored.
//
// The database compares the name it holds, and it holds the name as it was
// uploaded — composed or decomposed (see canonical), in whatever case.
// SQLite's LIKE folds case for ASCII letters only, and PostgreSQL's ILIKE
// only as far as the database's ctype locale does, so on its own `günlük`
// reaches neither `GÜNLÜK` nor a decomposed `Günlük`. The word is therefore
// sent in the forms names are actually written in:
//
//   - lower case, upper case and title case, cased the Turkish way so that
//     `i` also becomes the dotted capital `İ` — the plain `I` is already
//     covered, because every engine folds ASCII;
//   - each of those composed and decomposed. A decomposed Turkish letter is
//     an ASCII letter plus a mark, so the decomposed spelling matches the
//     word in any case at all.
//
// Spellings that differ only in ASCII case are one pattern to every engine
// and are sent once: a plain ASCII word without an `i` stays one pattern.
// Everything the database returns is re-checked by the scorer, so a
// spelling can only widen what is looked at, never what is shown.
func spellings(word string) []string {
	seen := map[string]bool{}
	var out []string
	for _, cased := range []string{
		word,
		strings.ToUpperSpecial(unicode.TurkishCase, word),
		titleTurkish(word),
	} {
		for _, form := range []string{norm.NFC.String(cased), norm.NFD.String(cased)} {
			key := asciiLower(form)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SQLLike(form))
		}
	}
	return out
}

// titleTurkish upper-cases the first letter of a lower-case word, the
// Turkish way (`ipek` → `İpek`).
func titleTurkish(word string) string {
	r, size := utf8.DecodeRuneInString(word)
	return string(unicode.TurkishCase.ToUpper(r)) + word[size:]
}

// sharedRun is the longest run of the word that every spelling of it
// holds, as far as an engine's LIKE can tell: ASCII letters and digits
// other than `i`, which becomes `İ` in the Turkish upper case. For
// `archive` it is `arch`; a word of Turkish letters may have none.
func sharedRun(word string) string {
	best, start := "", -1
	for i, r := range word + "\x00" {
		if r != 0 && r < utf8.RuneSelf && r != 'i' {
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

// asciiLower folds ASCII letters only — exactly the folding every engine's
// LIKE does, which is what decides whether two spellings are one pattern.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
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

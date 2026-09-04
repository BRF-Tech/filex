// Package search — name.go
//
// Forgiving filename matching: separator-blind normalisation, multi-word
// wildcards, typo tolerance, an explicit ranking contract, and the
// `tag:` query filter.
//
// Motivated by issue #15, which reported that `main go` did not find
// `main.go`. Measuring it turned up something narrower and stranger than
// the report: single words always worked, and `main go` / `foo bar`
// already worked, while `invoice 2026` did not find `invoice_2026.pdf`.
// The reason is tokenisation, not the number of words. Bleve's standard
// analyzer splits `invoice_2026.pdf` into [invoice_2026, pdf] — an
// underscore JOINS — and `foo-bar.txt` into [foo, bar.txt] — a hyphen
// SPLITS. So which separator the file happened to use decided whether the
// search worked, a distinction no user can predict.
//
// The fix is to stop letting the analyzer decide: every name and every
// query is normalised to the same separator-blind form before it reaches
// the index, so `.`, `-`, `_` and a space are all the same character.
package search

import (
	"path/filepath"
	"strings"
	"unicode"
)

// indexSchemaVersion is the document schema this build writes. It is
// stamped into the Bleve index itself (see Index.stampSchemaVersion) so a
// server that opens an index written by an older build can SAY so:
// documents indexed before v2 carry no name_norm/path_norm, and the
// separator-blind half of a query cannot match them.
//
// v1 -> v2 (issue #15): added name_norm + path_norm.
const indexSchemaVersion = "2"

// indexVersionKey is the Bleve internal-KV key holding the above.
const indexVersionKey = "filex:index_schema"

// Normalize maps a filename — or a raw user query — onto the
// separator-blind form both sides of a search are compared in:
// lower-cased, with every run of non-alphanumeric characters collapsed to
// a single space.
//
//	invoice_2026.pdf        -> "invoice 2026 pdf"
//	foo-bar.txt             -> "foo bar txt"
//	annual report 2025.docx -> "annual report 2025 docx"
//
// This is deliberately aggressive: the whole point is that a user typing
// `invoice 2026` must not have to know which of `.`, `-`, `_` or a space
// the file actually used. The ORIGINAL name stays indexed in the `name`
// field, so a query that leans on punctuation the normaliser removes
// (`c++`) still has the legacy wildcard path to land on.
//
// Letters and digits are kept by Unicode class, not by ASCII range, so
// `rapor-şubat.txt` normalises to `rapor şubat txt` instead of losing its
// Turkish characters.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	wrote := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingSpace && wrote {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
			pendingSpace = false
			wrote = true
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

// NormWords is Normalize split into its words — the unit every
// separator-blind sub-query is built per.
func NormWords(s string) []string {
	n := Normalize(s)
	if n == "" {
		return nil
	}
	return strings.Split(n, " ")
}

// ─────────────────── tag: filter parsing ───────────────────

// Parsed is a raw query string split into free text and tag filters.
//
// Tags are a FILTER, not a search term: `main go tag:source` means "the
// name match `main go`, restricted to nodes tagged source", never "also
// look for the word source". Filters narrow, so several tags are ANDed.
type Parsed struct {
	// Text is the query with every tag token removed. It may be empty (a
	// pure `tag:x` query), in which case the caller should list the
	// tagged nodes rather than run a text search.
	Text string
	// Tags must ALL be present on a node for it to survive the filter.
	Tags []string
	// ExcludeTags (`-tag:x`) drop a node when ANY of them is present.
	ExcludeTags []string
}

// HasTagFilter reports whether any tag token was parsed out.
func (p Parsed) HasTagFilter() bool { return len(p.Tags) > 0 || len(p.ExcludeTags) > 0 }

// ParseQuery splits `foo bar tag:source -tag:archive` into its text and
// tag parts.
//
// Rules, all deliberate and mirrored in docs/SEARCH.md:
//
//   - the `tag:` prefix is case-insensitive, and so is the tag VALUE.
//     The tags endpoint lower-cases on write, so a query that did not
//     lower-case would silently match nothing;
//   - `tag:"two words"` is supported, because the tags endpoint accepts
//     any string up to 64 characters and people do use spaces;
//   - a bare `tag:` with no value is NOT a filter. It stays in the text,
//     so somebody looking for a file actually called `tag:` still can.
func ParseQuery(raw string) Parsed {
	var p Parsed
	var text []string
	for _, tok := range splitQuery(raw) {
		neg := false
		body := tok
		if strings.HasPrefix(body, "-") {
			neg, body = true, body[1:]
		}
		if len(body) < 4 || !strings.EqualFold(body[:4], "tag:") {
			text = append(text, tok)
			continue
		}
		val := strings.ToLower(strings.TrimSpace(unquote(body[4:])))
		if val == "" {
			text = append(text, tok)
			continue
		}
		if neg {
			p.ExcludeTags = append(p.ExcludeTags, val)
		} else {
			p.Tags = append(p.Tags, val)
		}
	}
	p.Text = strings.Join(text, " ")
	return p
}

// splitQuery is strings.Fields that keeps a double-quoted run together,
// so `tag:"quarterly report"` survives as a single token.
func splitQuery(raw string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range raw {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case unicode.IsSpace(r) && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// unquote strips wrapping double quotes from a tag value.
func unquote(s string) string {
	return strings.Trim(s, "\"")
}

// ─────────────────── ranking ───────────────────

// Tier is the rank bucket a hit falls in.
//
// The issue asked that exact filename matches keep ranking first. Once
// fuzziness is in the mix that stops being something merged Bleve scores
// can be trusted to deliver — a two-edit fuzzy hit on a short filename
// can out-score an exact hit on a long one — so the order is decided
// HERE, in Go, and asserted by a test, rather than left to emerge from
// boost arithmetic.
//
// Lower is better. TierContent is last, which is also how the pre-v0.2
// "name hits before content-only hits" contract survives unchanged.
type Tier int

// Ranking tiers, best first.
const (
	TierExact   Tier = iota // filename equals the query (ignoring case + separators)
	TierPrefix              // filename starts with the query
	TierName                // every query word appears in the filename
	TierPath                // every query word appears in the path (folder hit)
	TierFuzzy               // only the typo-tolerant pass produced this
	TierContent             // matched inside the file, not in its name
)

// String renders a tier for logs and tests.
func (t Tier) String() string {
	switch t {
	case TierExact:
		return "exact"
	case TierPrefix:
		return "prefix"
	case TierName:
		return "name"
	case TierPath:
		return "path"
	case TierFuzzy:
		return "fuzzy"
	default:
		return "content"
	}
}

// RankName classifies one name hit against the query. Both sides are
// compared in normalised form, so `invoice 2026` ranks as an exact match
// for a file called `invoice_2026.pdf` exactly as it would for one
// literally called `invoice 2026`.
//
// The extension is compared BOTH ways — against the whole normalised
// name and against the name with its extension removed. Typing a
// filename without its extension is the most ordinary thing a person
// does, and a rule that only compared the whole name would have demoted
// `report` → `report.txt` below `report-final.txt` purely because of
// four characters the user did not type.
func RankName(query, name, path string) Tier {
	nq := Normalize(query)
	if nq == "" {
		return TierName
	}
	nn := Normalize(name)
	stem := nn
	if ext := filepath.Ext(name); ext != "" && ext != name {
		stem = Normalize(strings.TrimSuffix(name, ext))
	}
	if nn == nq || stem == nq {
		return TierExact
	}
	// Word-boundary prefix: `report` is a prefix of `report-final.txt`,
	// but `main` must not claim one on `maintenance.md` — that falls
	// through to TierName below, which is the honest bucket for it.
	if hasWordPrefix(nn, nq) || hasWordPrefix(stem, nq) {
		return TierPrefix
	}
	words := strings.Split(nq, " ")
	if containsAll(nn, words) {
		// A mid-word prefix (`rep` → `report.txt`) is still a prefix.
		if strings.HasPrefix(nn, nq) {
			return TierPrefix
		}
		return TierName
	}
	if containsAll(Normalize(path), words) {
		return TierPath
	}
	return TierFuzzy
}

// hasWordPrefix reports whether hay starts with prefix at a word boundary.
func hasWordPrefix(hay, prefix string) bool {
	return hay == prefix || strings.HasPrefix(hay, prefix+" ")
}

// containsAll reports whether every word appears somewhere in hay. It is
// substring containment rather than token equality because the wildcard
// half of the query matches mid-word too (`squ` -> `square.jpg`), and the
// ranking has to agree with what the query can actually return.
func containsAll(hay string, words []string) bool {
	for _, w := range words {
		if w == "" {
			continue
		}
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// fuzzinessFor picks an edit distance for one query word.
//
// Measured against Bleve's automaton, which counts a transposition as ONE
// edit — that is why `mian` reaches `main` at distance 1 and the issue's
// headline typo example does not need the expensive distance-2 pass. A
// distance of 1 on a three-letter word matches a large slice of any term
// dictionary and buys nothing, so short words stay exact; two edits wait
// until a word is long enough that two edits still leave it recognisable.
func fuzzinessFor(word string) int {
	switch n := len([]rune(word)); {
	case n <= 3:
		return 0
	case n <= 7:
		return 1
	default:
		return 2
	}
}

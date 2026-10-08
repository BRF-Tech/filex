package search

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/namefold"
)

// NameMatcher is the search's own rule for "does this NAME answer what was
// typed", for a name on its own: no index, no folder above it.
//
// ⚠ Why it exists (filex 0.54, audit D5). The explorer's "Filter in this
// folder" box used to narrow the rows in hand with its own rule, a substring
// over an accent-stripped copy of the name, and its comment said that was the
// search's rule. It was not: the search keeps accents ("müşteri" is not
// "musteri", internal/namefold), treats `.`, `-`, `_` and a space as one
// separator ("invoice 2026" finds "invoice_2026.pdf") and wants every word.
// So the box and the search disagreed on the same folder. The box now asks the
// server (POST /api/files/search/match), and the server answers with this.
//
// The rule is the index-less search's (Fallback): every normalised word of the
// query inside the folded name, then the subsequence scorer's verdict over the
// name alone. A query with no letter or digit (`***`) asks for itself as text.
type NameMatcher struct {
	words []string
	text  string
	query PreparedQuery
}

// NewNameMatcher prepares q once, for many names.
func NewNameMatcher(q string) NameMatcher {
	words := uniqueWords(NormWords(q))
	if len(words) == 0 {
		return NameMatcher{text: namefold.String(strings.TrimSpace(q))}
	}
	return NameMatcher{words: words, query: PrepareQuery(q)}
}

// Empty reports whether the query narrows nothing (blank).
func (m NameMatcher) Empty() bool { return len(m.words) == 0 && m.text == "" }

// Match reports whether name answers the query.
func (m NameMatcher) Match(name string) bool {
	if m.Empty() {
		return true
	}
	folded := namefold.String(name)
	if len(m.words) == 0 {
		return strings.Contains(folded, m.text)
	}
	if !containsAll(Normalize(name), m.words) {
		return false
	}
	return m.query.ScoreName(name, "").OK
}

// MatchName is NewNameMatcher(q).Match(name), for a single name.
func MatchName(q, name string) bool { return NewNameMatcher(q).Match(name) }

// Score is the scorer's number for a fallback row (bigger is better), the
// same number the index path puts on a hit's Score.
func (f Fallback) Score(name, path string) int {
	if len(f.Words) == 0 {
		return 0
	}
	return f.query.ScoreName(name, path).Score
}

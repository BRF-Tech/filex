// Package search — scorer.go
//
// Subsequence scoring for filenames, ported from VS Code's Quick Open
// (src/vs/base/common/fuzzyScorer.ts, MIT).
//
// Why a second matching layer exists at all. v0.29.0 answered issue #15
// with separator-blind normalisation plus substring matching, and the
// reporter came back with three things it still could not do:
//
//	"Fuzzy is distance matching. You shipped string matching. [...]
//	 Code/main.go and example/main.go are the same thing to the search,
//	 and `Code main` returns nothing. [...] with real fuzzy the word
//	 order shouldn't matter either, `main code` should still find
//	 Code/main.go"
//
// All three are the same missing piece: nothing in the pipeline scored a
// candidate. Bleve decided IF a document matched, and a merged Bleve
// score decided the order — and a Bleve score knows nothing about where
// in the filename the match landed, whether the folder or the file
// answered, or whether every word the user typed was answered at all.
//
// So the shape is: Bleve stays the candidate generator (cheap recall
// from the normalised fields, the wildcards and the typo pass), and this
// file re-ranks what comes back. It is also the FILTER — a candidate
// where some query piece matches nothing is dropped, which is what turns
// `Code main` from nine results into one.
//
// ⚠ What this file can NOT do, and it is worth being plain about it: it
// only ever sees candidates Bleve produced. Subsequence SCORING is not
// subsequence RECALL. `mgo` does not find `main.go`, because no Bleve
// query retrieves it — see docs/SEARCH.md, "What fuzzy does not mean
// here", for why widening recall needs an index change rather than a
// scoring change.
package search

import (
	"strings"
	"sync"
	"unicode"
)

// Score thresholds, verbatim from VS Code. They are what keeps the
// classes apart no matter how the per-character bonuses add up: a label
// match always outranks a folder-path match, and a label PREFIX match
// always outranks a label match somewhere in the middle, because the
// baseline they start from differs by a factor of two.
//
// The issue asked for exact matches to keep ranking first; these
// thresholds plus Tier are how that stays true rather than emerging from
// bonus arithmetic.
const (
	// PathIdentityScore is awarded when the query IS the path.
	PathIdentityScore = 1 << 18
	// LabelPrefixScoreThreshold is the baseline for a piece that matches
	// the start of the filename.
	LabelPrefixScoreThreshold = 1 << 17
	// LabelScoreThreshold is the baseline for a piece that matches the
	// filename anywhere else.
	LabelScoreThreshold = 1 << 16
)

// Per-character bonuses, verbatim from VS Code's computeCharScore.
const (
	bonusCharMatch     = 1 // any character that matches at all
	bonusSameCase      = 1 // ...and matched with the same case
	bonusStartOfTarget = 8 // index 0 of the target
	bonusAfterPathSep  = 5 // straight after / or \
	bonusAfterOtherSep = 4 // straight after _ - . space : ' "
	bonusCamelHump     = 2 // an upper-case char mid-word, outside a run
)

// PreparedQuery is a user query split into the pieces every candidate
// has to answer.
//
// The split is on whitespace and it is the whole reason `main code`
// finds Code/main.go: each piece is matched independently, so the ORDER
// the user typed them in carries no meaning. That is VS Code's
// behaviour, and it is what the reporter asked for in as many words.
type PreparedQuery struct {
	// raw is the query as typed, kept for the separator-blind tier
	// classification (which is our rule, not VS Code's — see ScoreName).
	raw string
	// normalized is the whole query lower-cased, path separators unified
	// to `/`, with wildcards, quotes and whitespace removed. Used for the
	// path-identity check.
	normalized string
	pieces     []queryPiece
	// hasPathSeparator switches off the label-only pass: once somebody
	// types a `/` they are describing a location, so the folder has to be
	// part of what is scored.
	hasPathSeparator bool
}

// queryPiece is one whitespace-separated piece, pre-converted to runes.
//
// Runes, not bytes: filenames here are routinely Turkish, and a
// byte-wise camelCase or separator test would score `rapor-şubat.txt`
// off the middle of a multi-byte character.
type queryPiece struct {
	runes []rune
	lower []rune
}

// Empty reports whether the query has nothing to score with.
func (q PreparedQuery) Empty() bool { return len(q.pieces) == 0 }

// PrepareQuery splits and normalises a raw query string.
//
// ⚠ The caller must have run ParseQuery first: `tag:` is a filter, and a
// `tag:source` token reaching this function would become a piece that no
// filename answers, so every candidate would be dropped and a perfectly
// good tag search would return nothing. Asserted by
// TestPrepareQuery_RawTagTokenWouldDropEverything.
func PrepareQuery(raw string) PreparedQuery {
	q := PreparedQuery{raw: raw}
	unified := strings.Map(func(r rune) rune {
		switch r {
		case '\\':
			return '/'
		case '*', '"', '…':
			// Wildcards, quotes and ellipsis are how people ASK for
			// fuzziness; they are never part of what they are looking
			// for. VS Code drops them for the same reason.
			return -1
		}
		return r
	}, raw)
	var norm strings.Builder
	for _, field := range strings.Fields(unified) {
		norm.WriteString(strings.ToLower(field))
		runes := []rune(field)
		lower := make([]rune, len(runes))
		for i, r := range runes {
			lower[i] = unicode.ToLower(r)
		}
		q.pieces = append(q.pieces, queryPiece{runes: runes, lower: lower})
	}
	q.normalized = norm.String()
	q.hasPathSeparator = strings.ContainsRune(q.normalized, '/')
	return q
}

// NameScore is one candidate's verdict.
type NameScore struct {
	// OK is false when some query piece matched nothing. The candidate is
	// not a result — this is the filter half of the scorer.
	OK bool
	// Score orders candidates WITHIN a tier. Bigger is better.
	Score int
	// Tier is the rank bucket, and it is what orders candidates ACROSS
	// classes. See Tier.
	Tier Tier
}

// ScoreName scores one candidate's filename and path against the query.
//
// The label (the filename) and the description (the folders above it)
// are scored SEPARATELY, and the label is weighted an order of magnitude
// higher — that is VS Code's doScoreItemFuzzy, and it is the answer to
// "Code/main.go and example/main.go are the same thing to the search".
// The combined `folder/name` string is only scored for pieces the label
// alone could not answer.
//
// The tier, unlike the score, is OURS. VS Code has no notion of an exact
// match distinct from a very good fuzzy one; issue #15 explicitly asked
// that exact filenames keep ranking first, so exact and prefix are still
// decided by the separator-blind rules RankName has used since v0.29.0,
// and the subsequence score orders what is left.
func (q PreparedQuery) ScoreName(name, path string) NameScore {
	if q.Empty() {
		// Nothing to score against (a pure `tag:` query, or punctuation
		// only). Filtering on no evidence would be worse than not
		// filtering at all.
		return NameScore{OK: true, Tier: TierName}
	}
	if name == "" && path == "" {
		// An index written before these fields were stored gives us
		// nothing to score. Keep the candidate: an upgrade that has not
		// been reindexed yet must lose ranking quality, never results.
		return NameScore{OK: true, Tier: TierName}
	}

	rel := strings.TrimPrefix(path, "/")
	if rel == "" {
		rel = name
	}
	// The query IS the path. Nothing can beat that, and it is the reason
	// `Code/main.go` puts /Code/main.go first instead of tying with
	// /example/main.go.
	//
	// EqualFold rather than ToLower: this runs on every candidate of
	// every search, and ToLower would allocate a throwaway string per
	// call just to throw it away again.
	if strings.EqualFold(q.normalized, rel) || strings.EqualFold(q.normalized, path) {
		return NameScore{OK: true, Score: PathIdentityScore, Tier: TierExact}
	}

	sc := scratchPool.Get().(*scratch)
	defer scratchPool.Put(sc)
	label := sc.label.set(name)
	full := sc.full.set(rel)

	total := 0
	labelOnly := true
	// preferLabelMatches: once the query contains a `/` the user is
	// describing a location, so the label-only pass is skipped entirely
	// and everything is scored against folder+name.
	preferLabel := !q.hasPathSeparator
	for _, p := range q.pieces {
		if preferLabel {
			if s := sc.scoreFuzzy(label, p); s > 0 {
				total += labelBaseScore(p, label) + s
				continue
			}
		}
		if s := sc.scoreFuzzy(full, p); s > 0 {
			total += s
			labelOnly = false
			continue
		}
		// This piece is answered by neither the filename nor any folder
		// above it. On the demo this is what drops the `/Code` folder
		// from `Code main`: it answers "code" and nothing answers "main".
		return NameScore{}
	}

	tier := TierPath
	if labelOnly {
		tier = TierName
	}
	if t, ok := classifyExactPrefix(q.raw, name); ok {
		tier = t
	}
	return NameScore{OK: true, Score: total, Tier: tier}
}

// labelBaseScore picks the baseline a label match starts from: a piece
// that matches the START of the filename gets twice the baseline of one
// that matches in the middle, plus a boost for covering more of a short
// name — so given `window.ts` and `windowActions.ts`, a query of
// `window` prefers the shorter one. Verbatim from VS Code.
func labelBaseScore(p queryPiece, label scoreTarget) int {
	if !hasPrefixFold(label.lower, p.lower) {
		return LabelScoreThreshold
	}
	boost := 0
	if len(label.runes) > 0 {
		boost = (len(p.runes) * 100) / len(label.runes)
	}
	return LabelPrefixScoreThreshold + boost
}

// hasPrefixFold reports whether target starts with prefix (both already
// lower-cased).
func hasPrefixFold(target, prefix []rune) bool {
	if len(prefix) > len(target) {
		return false
	}
	for i, r := range prefix {
		if target[i] != r {
			return false
		}
	}
	return true
}

// scoreTarget is a candidate string pre-converted to runes and their
// lower-case forms, so a multi-piece query converts each target once
// instead of once per piece.
type scoreTarget struct {
	runes []rune
	lower []rune
}

// set refills the target from s, reusing the backing arrays. Returns a
// copy of the slice headers so the caller can pass it by value into the
// hot loop without touching the pooled struct again.
func (t *scoreTarget) set(s string) scoreTarget {
	t.runes = t.runes[:0]
	t.lower = t.lower[:0]
	for _, r := range s {
		t.runes = append(t.runes, r)
		t.lower = append(t.lower, unicode.ToLower(r))
	}
	return *t
}

// scratch holds the DP matrices and the rune buffers between calls.
//
// The scorer runs once per query piece per candidate, and a search
// re-ranks up to searchMaxFetch of them, so anything allocated per call
// is multiplied by the whole result set. Everything here is reused;
// TestScorerDoesNotAllocatePerCall asserts the steady state is zero
// allocations, so a regression that drops the pool fails loudly instead
// of quietly costing a millisecond per search.
type scratch struct {
	scores  []int
	matches []int
	label   scoreTarget
	full    scoreTarget
}

var scratchPool = sync.Pool{New: func() any { return &scratch{} }}

func (s *scratch) grow(n int) {
	if cap(s.scores) < n {
		s.scores = make([]int, n)
		s.matches = make([]int, n)
		return
	}
	s.scores = s.scores[:n]
	s.matches = s.matches[:n]
	for i := range s.scores {
		s.scores[i] = 0
		s.matches[i] = 0
	}
}

// scoreFuzzy is VS Code's doScoreFuzzy: a DP matrix over
// (query char x target char) that finds the best-scoring subsequence
// match, or 0 when the query is not a subsequence of the target at all.
//
// We do not reconstruct the match POSITIONS. VS Code needs them to
// bold the matched characters in the picker; our search API returns
// nodes, not highlight ranges, and skipping the walk-back keeps the
// per-candidate cost down.
func (s *scratch) scoreFuzzy(t scoreTarget, p queryPiece) int {
	tl, ql := len(t.runes), len(p.runes)
	if tl == 0 || ql == 0 || tl < ql {
		return 0 // the query cannot fit inside the target
	}
	s.grow(ql * tl)
	for qi := 0; qi < ql; qi++ {
		qOff := qi * tl
		qPrevOff := qOff - tl
		for ti := 0; ti < tl; ti++ {
			cur := qOff + ti
			leftScore, diagScore, seqLen := 0, 0, 0
			if ti > 0 {
				leftScore = s.scores[cur-1]
				if qi > 0 {
					diagScore = s.scores[qPrevOff+ti-1]
					seqLen = s.matches[qPrevOff+ti-1]
				}
			}
			// Past the first query character we only score where the
			// PREVIOUS character already matched to the left — that is
			// what keeps the match in sequence, and without it a target
			// of "ede" would score a query of "de" off the leading "e".
			score := 0
			if diagScore > 0 || qi == 0 {
				score = charScore(t, p, ti, qi, seqLen)
			}
			if score > 0 && diagScore+score >= leftScore {
				s.matches[cur] = seqLen + 1
				s.scores[cur] = diagScore + score
			} else {
				s.matches[cur] = 0
				s.scores[cur] = leftScore
			}
		}
	}
	return s.scores[ql*tl-1]
}

// charScore is VS Code's computeCharScore. The bonuses are cumulative
// and they are the whole reason this ranks better than a substring test:
// they know that a match at the start of a filename, or straight after a
// separator, or on a camelCase hump, means more than a match in the
// middle of a word.
func charScore(t scoreTarget, p queryPiece, ti, qi, seqLen int) int {
	if !considerEqual(p.lower[qi], t.lower[ti]) {
		return 0
	}
	score := bonusCharMatch
	if seqLen > 0 {
		// A run up to 3 characters gets the full bonus and the remainder
		// gets half, so a long run does not drown out a better-placed
		// short one.
		score += min(seqLen, 3)*6 + max(0, seqLen-3)*3
	}
	if p.runes[qi] == t.runes[ti] {
		score += bonusSameCase
	}
	if ti == 0 {
		score += bonusStartOfTarget
		return score
	}
	if sep := separatorBonus(t.runes[ti-1]); sep > 0 {
		score += sep
		return score
	}
	// camelCase, but only outside a contiguous run: NPE should be
	// boosted against NullPointerException, HTTP against HTTP should not.
	if unicode.IsUpper(t.runes[ti]) && seqLen == 0 {
		score += bonusCamelHump
	}
	return score
}

// considerEqual treats the two path separators as the same character, so
// a user who types `Code\main.go` on Windows finds `Code/main.go`.
func considerEqual(a, b rune) bool {
	if a == b {
		return true
	}
	if a == '/' || a == '\\' {
		return b == '/' || b == '\\'
	}
	return false
}

// separatorBonus scores the character BEFORE a match: a path separator
// beats the others, because a match at the start of a path segment is
// the strongest signal short of the start of the name.
func separatorBonus(r rune) int {
	switch r {
	case '/', '\\':
		return bonusAfterPathSep
	case '_', '-', '.', ' ', ':', '\'', '"':
		return bonusAfterOtherSep
	}
	return 0
}

// Package namefold is the one rule for when two spellings of a name are the
// same text: what a search compares, and the part of a tag's identity that is
// about letters rather than whitespace.
//
// Two things make one name look like two, and both are folded here, in this
// order, for both sides of every comparison:
//
//  1. How the letters are ENCODED. A keyboard types `ü` as one code point
//     (Unicode normalisation form C); macOS hands a filename over decomposed,
//     `u` followed by U+0308 COMBINING DIAERESIS (form D). filex stores the
//     name it is given — the storage needs those bytes — so a catalogue holds
//     both. Measured on a production catalogue on 2026-09-24: 71 388 of
//     169 471 names were decomposed, about nine in ten of the names with a
//     Turkish letter in them (GitHub PR #46). Canonical composes.
//
//  2. How the letters are CASED. Go's default case mapping is wrong for
//     Turkish in exactly the letters a Turkish user types most: `I` lowers to
//     `i`, not `ı`, and `ı` has no default partner at all, so "IŞIK" and
//     "ışık" — one word — are different text to strings.ToLower. Turkish
//     casing is wrong the other way: "INVOICE" lowers to "ınvoıce". So the
//     four Latin i's — I, ı, İ, i — are ONE letter here, and everything else
//     is lower-cased the default way. That is the rule internal/tagname has
//     used for tags since v0.43.0; a search that disagreed with it would call
//     a tag and the file name it was copied from two different words.
//
// The price of (2) is that two Turkish words spelt apart only by the dot —
// "ılık" and "ilik" — are one word to a search. The opposite rule fails every
// capitalised Turkish name typed on an English keyboard and every English
// name typed on a Turkish one.
//
// Accents stay significant: "müşteri" and "musteri" are different words. The
// stored name is never changed; this is how names are COMPARED.
package namefold

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// dotAbove is U+0307 COMBINING DOT ABOVE.
const dotAbove rune = 0x0307

// Canonical returns s composed (NFC), with a dot above dropped where it sits
// on a letter that already has one or is about to lose it: any of the four
// i's, or j.
//
// The dot: lower-casing `İ` the full Unicode way — JavaScript's toLowerCase(),
// Python's lower() — gives `i` + U+0307, which composition leaves as it is, so
// a client or an agent that lower-cases a query before sending it would never
// meet the `i` on the other side (found verifying PR #46 against the
// production catalogue, where 4 of 10 sampled names carried an `İ`).
//
// A string that is already composed and holds no U+0307 — ASCII among them —
// is returned as it is, without allocating: this runs on every candidate of
// every search. ⚠ QuickSpanString, not IsNormalString: the latter allocates on
// every call, fast path included (two allocations per candidate in
// BenchmarkScorerPerCandidate, which is otherwise flat zero). U+0307 can
// compose, so a string holding it never takes the fast path.
func Canonical(s string) string {
	if norm.NFC.QuickSpanString(s) == len(s) {
		return s
	}
	s = norm.NFC.String(s)
	if !strings.ContainsRune(s, dotAbove) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := rune(0)
	for _, r := range s {
		if r == dotAbove && carriesDot(prev) {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// carriesDot reports whether a dot above on r is not a letter of its own:
// the i's (one already has it, and to a search they are one letter) and j.
func carriesDot(r rune) bool {
	switch r {
	case 'i', 'I', 'ı', 'İ', 'j', 'J':
		return true
	}
	return false
}

// Rune is the case fold of one rune: the four Latin i's are one letter, `i`;
// everything else is lower-cased the Unicode default way. One rune in, one
// rune out, so a caller that compares position by position (the scorer)
// keeps its positions.
func Rune(r rune) rune {
	if IsI(r) {
		return 'i'
	}
	if r < utf8.RuneSelf {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}
	return unicode.ToLower(r)
}

// String is s as a search compares it: Canonical, then Rune on every rune.
// Allocation-free for a string that is already composed and folded.
func String(s string) string {
	return strings.Map(Rune, Canonical(s))
}

// Words is each of words through String, empty results and repeats dropped,
// in their first order — the words a store compares a folded name with.
func Words(words []string) []string {
	out := make([]string, 0, len(words))
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		if w = String(w); w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// Plain reports whether r, an ASCII letter or digit, is stored as itself in
// every spelling of a name that folds to it — so a database can look for it
// in the stored bytes as they are, with nothing but its own ASCII case
// folding, and cheaply. Every ASCII letter and digit is, except three:
//
//   - `i`: `ı`, `I` and `İ` fold to it, and a stored i may carry a dot above
//     that Canonical drops;
//   - `j`: a stored j may carry a dot above that Canonical drops;
//   - `k`: U+212A KELVIN SIGN composes to `K`.
//
// TestPlain_NothingElseFoldsIntoPlainASCII walks every code point to keep
// this list honest.
func Plain(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		switch r | 0x20 {
		case 'i', 'j', 'k':
			return false
		}
		return true
	}
	return false
}

// PlainPrefix is the longest prefix of s made of Plain letters and digits:
// all of s when AllPlain(s). A folded word that is all Plain is stored as it
// is in every spelling of a name that folds to it, so a database finds it
// with its own case-insensitive LIKE — no normaliser needed, and none of its
// cost; a word that is not can still be narrowed by its plain prefix.
func PlainPrefix(s string) string {
	for i, r := range s {
		if !Plain(r) {
			return s[:i]
		}
	}
	return s
}

// AllPlain reports whether every rune of a non-empty s is Plain.
func AllPlain(s string) bool { return s != "" && PlainPrefix(s) == s }

// IsI reports whether r is one of the four Latin i's.
func IsI(r rune) bool {
	switch r {
	case 'i', 'I', 'ı', 'İ':
		return true
	}
	return false
}

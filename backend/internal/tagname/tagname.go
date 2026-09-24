// Package tagname decides what a tag is CALLED and when two tags are the SAME.
//
// Two answers, deliberately kept apart:
//
//   - Clean — the name a person sees. Exactly as typed, apart from the
//     whitespace at the ends and runs of it in the middle. "Müşteri Teklifi"
//     stays "Müşteri Teklifi".
//   - Key — the identity. Two names with one Key are one tag: typing
//     "müşteri teklifi" on a second file adds the tag the first file already
//     carries, under the name it was first given, instead of creating a twin
//     that differs only in capitals.
//
// ⚠⚠ Until v0.43.0 there was only one answer, `strings.ToLower`, and it was
// written INTO the name: "Müşteri Teklifi" was stored and shown as "müşteri
// teklifi" (tester, 2026-09-22). Lower-casing is a matching rule; it must not
// be what the person reads back.
//
// # What "the same tag" means, and why it is not strings.EqualFold
//
// Go's case mapping (strings.ToLower, strings.EqualFold, unicode.SimpleFold) is
// the Unicode DEFAULT, which is wrong for Turkish in exactly the letters a
// Turkish user types most: the default lower case of `I` is `i`, but in Turkish
// it is `ı`; `İ` has no simple fold partner at all. So under the default rules
// "IŞIK" and "ışık" (the same word) are different tags, and under the Turkish
// rules "INVOICE" and "invoice" are different tags for everyone else. No single
// locale's casing is right for a tag vocabulary that people with different
// keyboards share.
//
// Key therefore treats the four Latin i's — I, ı, İ, i — as ONE letter, and
// applies full Unicode case folding to everything else:
//
//	"IŞIK" = "ışık" = "Işık"             (Turkish, either keyboard)
//	"İSTANBUL" = "istanbul" = "ISTANBUL" (typed with or without the dot)
//	"INVOICE" = "invoice"                (English)
//	"STRASSE" = "Straße"                 (German: full folding maps ß to ss)
//	"ΟΔΟΣ" = "οδος"                    (Greek: the final ς folds to σ)
//
// The i rule and the composition below are internal/namefold's — the rule a
// search compares file names by — so a tag and a file name agree on what one
// word is. Key adds FULL case folding on top (ß = ss), which a search cannot:
// it compares a typed word against a name position by position.
//
// The price is that two different Turkish words spelt apart only by the dot,
// such as "ılık" (lukewarm) and "ilik" (marrow), become one tag. That collision
// is rare in a tag vocabulary; the opposite rule would split EVERY capitalised
// Turkish tag typed on an English keyboard, and every English tag typed on a
// Turkish one. The display name is never touched, so nothing is lost on screen.
//
// Accents stay significant: "müşteri" and "musteri", "şık" and "sık" are
// different words, and a person means different things by them.
//
// Both functions normalise to NFC first. The same "ü" arrives either as one
// code point (most keyboards) or as "u" + a combining diaeresis (text pasted
// from a macOS file name); without NFC those two would be two tags that look
// identical on screen — the worst kind of duplicate, because nobody can see why.
package tagname

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/namefold"
)

// MaxRunes is the longest tag name accepted, in characters. It was 64 BYTES
// until v0.43.0, which let an English tag be twice as long as a Turkish one;
// characters are what a person counts.
const MaxRunes = 64

// Errors Clean reports. The handler turns them into a 400 the client words.
var (
	ErrEmpty   = errors.New("tag name is empty")
	ErrTooLong = errors.New("tag name is longer than 64 characters")
	ErrControl = errors.New("tag name contains a control character")
)

// folder is stateless and safe for concurrent use (x/text documents Fold as
// such), so one is shared.
var folder = cases.Fold()

// Clean returns the name a person sees: NFC, trimmed, inner whitespace runs
// collapsed to one space. Case is PRESERVED — that is the point of this
// package. A control character (a newline pasted along with a word, a NUL) is
// refused rather than stripped: stripping would silently merge "a\nb" with
// "ab", and nothing on screen would explain it.
func Clean(raw string) (string, error) {
	s := norm.NFC.String(raw)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "", ErrControl
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if out == "" {
		return "", ErrEmpty
	}
	if utf8.RuneCountInString(out) > MaxRunes {
		return "", ErrTooLong
	}
	return out, nil
}

// Key is the identity of a tag name: two names with one Key are one tag. See
// the package comment for the rules. Key of an invalid name is still defined
// (it only ever compares), so callers that match against stored names need not
// re-validate what they read back.
func Key(name string) string {
	// The letters are folded by internal/namefold — the rule a search compares
	// file names by — so a tag and the file name it was copied from are the
	// same word to both. Canonical FIRST: a decomposed "İ" (I + U+0307) becomes
	// the single code point namefold.Rune folds, rather than an `I` followed by
	// a stray dot that would survive folding as "i" + U+0307; and the dot a full
	// Unicode lower-casing leaves on an `i` is dropped.
	s := namefold.Canonical(strings.TrimSpace(name))
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(namefold.Rune(r))
	}
	// Full folding can emit a combining sequence (e.g. "ǰ" → "ǰ"), so NFC
	// once more: the Key must have exactly one spelling.
	return norm.NFC.String(folder.String(b.String()))
}

// Same reports whether two names are one tag.
func Same(a, b string) bool { return Key(a) == Key(b) }

package shortcode

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// ── One spelling of a code ─────────────────────────────────────────────────
//
// A code reaches filex spelled many ways: typed, pasted from a chat that added
// or dropped U+FE0F after an emoji, percent-encoded with upper- or lower-case
// hex, decomposed (`e` + U+0301) by a keyboard, in another case. Canonical is
// the ONE function that turns any of them into the code's canonical form, and
// both doors use it: the one that makes a link and the one that answers a
// visit. Two spellings that fold to the same canonical form are the same
// link; two that do not are two links, on every database engine alike.
//
// ⚠ The database compares Key(canonical) - a sha256 in hex - never the code
// itself. MySQL's default collations compare text case- and
// accent-insensitively (`abc` = `ABC`, `e` = `é`), so a code stored as text
// would be found by a spelling that is a different code; hex is ASCII and
// compares the same on SQLite, PostgreSQL and MySQL.

// isVariationSelector reports U+FE0E (text style) and U+FE0F (emoji style):
// they ask for a glyph style and are added and dropped in transit.
func isVariationSelector(r rune) bool { return r == 0xFE0E || r == 0xFE0F }

// FoldRune is the case-insensitive form of one symbol: the lower case of its
// upper case. So `A`, `a` fold to `a`, and `i`, `ı`, `I`, `İ` all fold to `i`
// - which is why a case-insensitive custom set may hold only one of them.
func FoldRune(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }

// DecodeSegment reads one ESCAPED path segment (from URL.EscapedPath) into
// text: percent escapes decoded once, the result valid UTF-8 with no `/`
// (an escaped slash is not part of a code). ok is false for anything else.
func DecodeSegment(escaped string) (string, bool) {
	if escaped == "" || len(escaped) > 4*MaxLength*4 {
		return "", false
	}
	s, err := url.PathUnescape(escaped)
	if err != nil || s == "" || !utf8.ValidString(s) || strings.ContainsRune(s, '/') {
		return "", false
	}
	return s, true
}

// Canonical is the canonical form of a code: NFC, variation selectors
// removed, and - when case does not matter - every symbol folded (FoldRune).
// ok is false for what can never be a code: empty, longer than MaxLength
// symbols, or holding a symbol SymbolAllowed refuses (a space, a control or
// invisible character, a URL delimiter, an emoji sequence joiner or a
// skin-tone modifier).
func Canonical(code string, caseSensitive bool) (string, bool) {
	if !utf8.ValidString(code) {
		return "", false
	}
	var b strings.Builder
	n := 0
	for _, r := range norm.NFC.String(code) {
		if isVariationSelector(r) {
			continue
		}
		if !SymbolAllowed(r) {
			return "", false
		}
		if !caseSensitive {
			r = FoldRune(r)
		}
		b.WriteRune(r)
		n++
		if n > MaxLength {
			return "", false
		}
	}
	if n == 0 {
		return "", false
	}
	// Folding can leave a letter that composes with nothing new, but say it
	// once more so the form is NFC whatever the folding did.
	return norm.NFC.String(b.String()), true
}

// Key is what the database stores and compares for a canonical code.
func Key(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// EscapePath is the code as it goes into a link that is copied, mailed or
// handed to a chat: every byte outside ASCII percent-encoded (UTF-8), so a
// messenger that would stop a link at the first emoji sees one unbroken
// address. A browser shows it decoded in its address bar.
func EscapePath(code string) string { return url.PathEscape(code) }

// Symbols is how many symbols a canonical code has.
func Symbols(code string) int { return utf8.RuneCountInString(code) }

// reservedCodes cannot be codes on a host whose links sit at its root: the
// paths a browser or a crawler asks every host for.
var reservedCodes = map[string]bool{
	"healthz": true, "favicon.ico": true, "robots.txt": true,
	".well-known": true, "apple-touch-icon.png": true,
}

// ReservedCode reports a canonical code that is one of those paths.
func ReservedCode(canonical string) bool { return reservedCodes[strings.ToLower(canonical)] }

// CheckAlias validates a code an app chose (an alias) and answers its
// canonical form: at least MinAliasLength symbols, each one SymbolAllowed,
// and not a reserved path. An alias need not be drawn from the alphabet, and
// it is outside the keyspace floor - a word is guessable by nature.
func CheckAlias(alias string, caseSensitive bool) (string, error) {
	c, ok := Canonical(alias, caseSensitive)
	if !ok || Symbols(c) < MinAliasLength || ReservedCode(c) {
		return "", ErrCode
	}
	return c, nil
}

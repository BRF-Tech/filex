// Package shortcode is a kit for apps that hand out short codes (a link
// shortener, an invitation code): the alphabets a code is drawn from, the one
// canonical form a code is stored and looked up by, crypto/rand generation,
// and the guessing arithmetic a setup screen shows. It runs in the app's
// module; filex itself keeps none of an app's codes.
//
// The arithmetic assumes filex's own miss limiter on an app's public route
// (MissesPerDay): the host counts the visits an app answered "miss" and
// holds a client back past that many.
package shortcode

import (
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The alphabets the setup wizard offers. A custom set is the administrator's
// own list of symbols.
const (
	PresetDigits  = "digits"   // 0-9
	PresetAlnum   = "alnum"    // A-Z a-z 0-9 (a-z 0-9 when case does not matter)
	PresetURLSafe = "url-safe" // alnum and - . _ ~ (RFC 3986 "unreserved")
	PresetEmoji   = "emoji"    // emoji.txt
	PresetCustom  = "custom"
)

// emojiSet is the emoji alphabet: 975 single code points with
// Emoji_Presentation=Yes in Unicode 13.0 (drawn as emoji with no variation
// selector on every platform since 2020), in the supplementary planes, without
// skin-tone modifiers, regional indicators, and the families told apart only
// by a detail (clock faces, moon phases, coloured circles, squares and
// diamonds, squared letters). One rune each, in code point order.
//
// ⚠ Changing it changes no existing link (a link is found by its canonical
// key, never by the alphabet), but it changes every keyspace the wizard
// computed. alphabet_test.go pins its size and digest.
//
//go:embed emoji.txt
var emojiSet string

// The limits on a code rule.
const (
	// MinBits is the floor of a generated code's keyspace, in bits
	// (log2 of |alphabet|^length). A rule under it cannot be saved.
	//
	// Why 45: filex's miss limiter lets one client make MissesPerDay wrong
	// guesses a day. An attacker with 10,000 addresses against 100,000 live
	// links finds ONE of them, on average, after
	// 2^45 / (100,000 × 10,000 × 100) ≈ 350 days - and the instance raises
	// an alarm long before. 40 bits is about 11 days under the same attacker.
	MinBits = 45
	// GoodBits is where the wizard stops warning.
	GoodBits = 50
	// MaxLength is the longest generated code (and alias), in symbols.
	MaxLength = 64
	// MinCustomSymbols / MaxCustomSymbols bound a custom set.
	MinCustomSymbols = 2
	MaxCustomSymbols = 4096
	// MinAliasLength is the shortest alias an app may ask for.
	MinAliasLength = 3
)

// MissesPerDay is the most wrong guesses filex lets one client make in a day
// on an app's public route before its miss limiter holds the client back
// (internal/shortlink; a test there keeps the two equal).
const MissesPerDay = 100

// The attacker the wizard's estimate assumes (GuessDays).
const (
	ModelAttackerAddresses = 10000
	ModelLiveLinks         = 100000
)

// Errors a rule or a code is refused with. The API answers them as codes.
var (
	ErrPreset        = errors.New("short_link_alphabet_unknown")
	ErrCustomSymbols = errors.New("short_link_alphabet_symbols")
	ErrSymbol        = errors.New("short_link_alphabet_symbol")
	ErrCollision     = errors.New("short_link_alphabet_collision")
	ErrLength        = errors.New("short_link_length")
	ErrTooWeak       = errors.New("short_link_too_weak")
	ErrCode          = errors.New("short_link_code_invalid")
)

// Alphabet is the set of symbols generated codes are drawn from, and whether
// a visitor's code is matched with case.
type Alphabet struct {
	preset        string
	symbols       []rune
	caseSensitive bool
}

const unreservedMarks = "-._~"

// NewAlphabet builds one of the presets, or a custom set from custom (every
// rune one symbol; spaces and commas between them are ignored).
//
// When case does not matter, the letters are matched folded
// (FoldRune): a preset draws lower-case letters only, and a custom set may
// not hold two symbols that fold to the same one (`a` and `A`; `i`, `ı`, `I`
// and `İ`), since a visitor could not tell the codes apart.
func NewAlphabet(preset, custom string, caseSensitive bool) (Alphabet, error) {
	a := Alphabet{preset: preset, caseSensitive: caseSensitive}
	switch preset {
	case PresetDigits:
		a.symbols = []rune("0123456789")
	case PresetAlnum, PresetURLSafe:
		s := "abcdefghijklmnopqrstuvwxyz0123456789"
		if caseSensitive {
			s = "ABCDEFGHIJKLMNOPQRSTUVWXYZ" + s
		}
		if preset == PresetURLSafe {
			s += unreservedMarks
		}
		a.symbols = []rune(s)
	case PresetEmoji:
		a.symbols = []rune(emojiSet)
	case PresetCustom:
		syms, err := customSymbols(custom, caseSensitive)
		if err != nil {
			return Alphabet{}, err
		}
		a.symbols = syms
	default:
		return Alphabet{}, ErrPreset
	}
	return a, nil
}

// customSymbols reads a custom set: each rune, NFC-composed, a symbol.
func customSymbols(custom string, caseSensitive bool) ([]rune, error) {
	if !utf8.ValidString(custom) {
		return nil, ErrSymbol
	}
	var out []rune
	seen := map[rune]rune{}
	for _, r := range norm.NFC.String(custom) {
		if r == ' ' || r == ',' || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if isVariationSelector(r) {
			continue
		}
		if !SymbolAllowed(r) {
			return nil, fmt.Errorf("%w: %q", ErrSymbol, r)
		}
		key := r
		if !caseSensitive {
			key = FoldRune(r)
		}
		if prev, dup := seen[key]; dup {
			if prev == r {
				continue // the same symbol twice: one symbol
			}
			return nil, fmt.Errorf("%w: %q and %q", ErrCollision, prev, r)
		}
		seen[key] = r
		if !caseSensitive {
			r = key
		}
		out = append(out, r)
	}
	if len(out) < MinCustomSymbols || len(out) > MaxCustomSymbols {
		return nil, ErrCustomSymbols
	}
	return out, nil
}

// SymbolAllowed reports whether r may be a symbol of a code: a letter, a
// number, one of `- . _ ~`, or an emoji of the emoji set. Nothing that ends a
// path segment or means something in a URL (`/ ? # % + &` ...), no space, no
// control or invisible character, no combining mark on its own.
func SymbolAllowed(r rune) bool {
	switch {
	case r < 0x20 || r == 0x7f:
		return false
	case strings.ContainsRune(unreservedMarks, r):
		return true
	case unicode.IsLetter(r), unicode.IsNumber(r):
		return !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Me, r)
	case isEmoji(r):
		return true
	}
	return false
}

var emojiRunes = func() map[rune]bool {
	m := make(map[rune]bool, len(emojiSet))
	for _, r := range emojiSet {
		m[r] = true
	}
	return m
}()

func isEmoji(r rune) bool { return emojiRunes[r] }

// Preset is the alphabet's preset name.
func (a Alphabet) Preset() string { return a.preset }

// CaseSensitive reports whether codes are matched with case.
func (a Alphabet) CaseSensitive() bool { return a.caseSensitive }

// Size is how many symbols a code is drawn from.
func (a Alphabet) Size() int { return len(a.symbols) }

// Symbols is the alphabet as a string, in order.
func (a Alphabet) Symbols() string { return string(a.symbols) }

// Bits is the keyspace of a code of n symbols, log2(|alphabet|^n).
func (a Alphabet) Bits(n int) float64 { return Bits(a.Size(), n) }

// Bits is log2(size^n).
func Bits(size, n int) float64 {
	if size < 2 || n < 1 {
		return 0
	}
	return float64(n) * math.Log2(float64(size))
}

// MinLengthFor is the shortest code of this alphabet that reaches MinBits.
func (a Alphabet) MinLengthFor(bits float64) int {
	if a.Size() < 2 {
		return 0
	}
	return int(math.Ceil(bits / math.Log2(float64(a.Size()))))
}

// CheckRule refuses a length out of range or under the keyspace floor.
func (a Alphabet) CheckRule(n int) error {
	if n < 1 || n > MaxLength {
		return ErrLength
	}
	if a.Bits(n) < MinBits {
		return ErrTooWeak
	}
	return nil
}

// GuessDays is how many days, on average, an attacker with `addresses`
// addresses, each held to MissesPerDay wrong guesses a day, needs to find ONE
// of `links` live links in a keyspace of `bits` bits. +Inf when nothing is
// guessing.
func GuessDays(bits float64, links, addresses int) float64 {
	if links < 1 {
		links = 1
	}
	rate := float64(links) * float64(addresses) * float64(MissesPerDay)
	if rate <= 0 {
		return math.Inf(1)
	}
	return math.Exp2(bits) / rate
}

// Generate draws a code of n symbols, every symbol uniformly and
// independently from crypto/rand (rejection sampling: no modulo bias, no
// sequence, nothing derived from a clock or a counter).
func (a Alphabet) Generate(n int) (string, error) {
	if a.Size() < 2 {
		return "", ErrCustomSymbols
	}
	if n < 1 || n > MaxLength {
		return "", ErrLength
	}
	max := big.NewInt(int64(a.Size()))
	var b strings.Builder
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteRune(a.symbols[v.Int64()])
	}
	return b.String(), nil
}

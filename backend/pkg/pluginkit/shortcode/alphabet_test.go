package shortcode

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// The emoji alphabet is part of every keyspace the wizard showed: it changes
// only on purpose, with this test.
func TestEmojiSet_IsPinned(t *testing.T) {
	sum := sha256.Sum256([]byte(emojiSet))
	if got := hex.EncodeToString(sum[:]); got != "3cc1a92a1f357bea2a6da0f983b55b53b43c1ab8ad263dd2c2d5740402f2e900" {
		t.Fatalf("emoji.txt changed: sha256 %s", got)
	}
	if n := utf8.RuneCountInString(emojiSet); n != 975 {
		t.Fatalf("emoji set has %d symbols, want 975", n)
	}
	seen := map[rune]bool{}
	for _, r := range emojiSet {
		if seen[r] {
			t.Fatalf("emoji %U twice", r)
		}
		seen[r] = true
		if r < 0x1F000 {
			t.Fatalf("emoji %U is in the BMP (those need U+FE0F somewhere)", r)
		}
		if r >= 0x1F3FB && r <= 0x1F3FF {
			t.Fatalf("skin-tone modifier %U in the set", r)
		}
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			t.Fatalf("regional indicator %U in the set", r)
		}
	}
}

func TestAlphabet_Presets(t *testing.T) {
	for _, c := range []struct {
		preset string
		cs     bool
		size   int
	}{
		{PresetDigits, true, 10}, {PresetDigits, false, 10},
		{PresetAlnum, true, 62}, {PresetAlnum, false, 36},
		{PresetURLSafe, true, 66}, {PresetURLSafe, false, 40},
		{PresetEmoji, true, 975}, {PresetEmoji, false, 975},
	} {
		a, err := NewAlphabet(c.preset, "", c.cs)
		if err != nil {
			t.Fatalf("%s/%v: %v", c.preset, c.cs, err)
		}
		if a.Size() != c.size {
			t.Fatalf("%s/%v: size %d, want %d", c.preset, c.cs, a.Size(), c.size)
		}
		// Every symbol of every preset is a symbol a code may hold, and is
		// its own canonical form: a generated code needs no folding.
		for _, r := range a.Symbols() {
			if !SymbolAllowed(r) {
				t.Fatalf("%s: symbol %q not allowed", c.preset, r)
			}
			if got, ok := Canonical(string(r), c.cs); !ok || got != string(r) {
				t.Fatalf("%s/%v: %q canonical %q %v", c.preset, c.cs, r, got, ok)
			}
		}
	}
	if _, err := NewAlphabet("hex", "", true); !errors.Is(err, ErrPreset) {
		t.Fatalf("unknown preset: %v", err)
	}
}

func TestAlphabet_Custom(t *testing.T) {
	a, err := NewAlphabet(PresetCustom, "a b c, ç ğ ı ö ş ü 7", true)
	if err != nil || a.Size() != 10 {
		t.Fatalf("custom: %v size %d", err, a.Size())
	}
	// The same symbol twice is one symbol.
	if a, err := NewAlphabet(PresetCustom, "abca", true); err != nil || a.Size() != 3 {
		t.Fatalf("duplicate: %v %d", err, a.Size())
	}
	// Two symbols a visitor cannot tell apart when case does not matter.
	for _, set := range []string{"aA", "iI", "ıI", "iİ", "ıi"} {
		if _, err := NewAlphabet(PresetCustom, set, false); !errors.Is(err, ErrCollision) {
			t.Fatalf("%q case-insensitive: %v, want collision", set, err)
		}
		if _, err := NewAlphabet(PresetCustom, set, true); err != nil {
			t.Fatalf("%q case-sensitive: %v", set, err)
		}
	}
	// What ends a path segment or means something in a URL.
	for _, set := range []string{"ab/", "ab?", "ab#", "ab%", "ab+", "ab&", "a\u200db", "x\u0301y", "ab\u00ad"} {
		if _, err := NewAlphabet(PresetCustom, set, true); !errors.Is(err, ErrSymbol) {
			t.Fatalf("%q: %v, want symbol refusal", set, err)
		}
	}
	if _, err := NewAlphabet(PresetCustom, "a", true); !errors.Is(err, ErrCustomSymbols) {
		t.Fatalf("one symbol: %v", err)
	}
	// A decomposed letter is composed first: one symbol, é.
	if a, err := NewAlphabet(PresetCustom, "e\u0301x", true); err != nil || a.Symbols() != "éx" {
		t.Fatalf("NFC: %v %q", err, a.Symbols())
	}
	// Emoji with an emoji-style selector: the selector is not a symbol.
	if a, err := NewAlphabet(PresetCustom, "\U0001F600\ufe0f\U0001F680", true); err != nil || a.Size() != 2 {
		t.Fatalf("emoji custom: %v %d", err, a.Size())
	}
}

// The floor, in the lengths the design report names.
func TestAlphabet_Floor(t *testing.T) {
	for _, c := range []struct {
		preset string
		cs     bool
		ok     int // shortest length that saves
	}{
		{PresetAlnum, true, 8},    // 47.6 bits
		{PresetAlnum, false, 9},   // 46.5
		{PresetDigits, true, 14},  // 46.5
		{PresetURLSafe, true, 8},  // 48.4
		{PresetURLSafe, false, 9}, // 47.9
		{PresetEmoji, true, 5},    // 49.6
	} {
		a, _ := NewAlphabet(c.preset, "", c.cs)
		if err := a.CheckRule(c.ok); err != nil {
			t.Fatalf("%s/%v len %d: %v (%.1f bits)", c.preset, c.cs, c.ok, err, a.Bits(c.ok))
		}
		if err := a.CheckRule(c.ok - 1); !errors.Is(err, ErrTooWeak) {
			t.Fatalf("%s/%v len %d: %v (%.1f bits), want too weak", c.preset, c.cs, c.ok-1, err, a.Bits(c.ok-1))
		}
		if got := a.MinLengthFor(MinBits); got != c.ok {
			t.Fatalf("%s/%v MinLengthFor %d, want %d", c.preset, c.cs, got, c.ok)
		}
	}
	a, _ := NewAlphabet(PresetAlnum, "", true)
	if err := a.CheckRule(MaxLength + 1); !errors.Is(err, ErrLength) {
		t.Fatalf("too long: %v", err)
	}
}

// The estimate the wizard shows, at the floor and around it.
func TestGuessDays(t *testing.T) {
	d := GuessDays(MinBits, ModelLiveLinks, ModelAttackerAddresses)
	if d < 340 || d > 360 {
		t.Fatalf("45 bits: %.1f days, want about 352", d)
	}
	if d40 := GuessDays(40, ModelLiveLinks, ModelAttackerAddresses); d40 < 10 || d40 > 12 {
		t.Fatalf("40 bits: %.1f days, want about 11", d40)
	}
	if !math.IsInf(GuessDays(50, 10, 0), 1) {
		t.Fatal("nobody guessing: want +Inf")
	}
	if MissesPerDay != 100 {
		t.Fatalf("MissesPerDay %d: the floor's arithmetic assumes 100", MissesPerDay)
	}
}

func TestGenerate_UniformAndFromTheAlphabet(t *testing.T) {
	a, _ := NewAlphabet(PresetCustom, "ab", true)
	counts := map[rune]int{}
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		c, err := a.Generate(16)
		if err != nil {
			t.Fatal(err)
		}
		if utf8.RuneCountInString(c) != 16 {
			t.Fatalf("length %d", utf8.RuneCountInString(c))
		}
		seen[c] = true
		for _, r := range c {
			counts[r]++
		}
	}
	// 32,000 draws of a fair coin: each side within 2% of half.
	if a, b := counts['a'], counts['b']; a+b != 32000 || a < 15360 || a > 16640 {
		t.Fatalf("a=%d b=%d: not uniform", a, b)
	}
	// 2000 draws of 2^16 codes: about 30 repeats by the birthday bound
	// (n²/2N), so about 1970 distinct; a sequence or a stuck source would
	// give far fewer.
	if len(seen) < 1930 {
		t.Fatalf("only %d distinct codes of 2^16 possible in 2000 draws", len(seen))
	}
	e, _ := NewAlphabet(PresetEmoji, "", true)
	c, err := e.Generate(5)
	if err != nil || utf8.RuneCountInString(c) != 5 {
		t.Fatalf("emoji: %q %v", c, err)
	}
	for _, r := range c {
		if !strings.ContainsRune(emojiSet, r) {
			t.Fatalf("%U not from the set", r)
		}
	}
	if _, err := e.Generate(0); !errors.Is(err, ErrLength) {
		t.Fatalf("length 0: %v", err)
	}
}

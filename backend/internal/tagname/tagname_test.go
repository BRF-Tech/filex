package tagname

import (
	"errors"
	"strings"
	"testing"
)

// The display name keeps the person's capitals. v0.42 lower-cased the NAME
// itself ("Müşteri Teklifi" came back as "müşteri teklifi"), which is what the
// tester reported; this is the assertion that goes red if a ToLower creeps
// back into Clean.
func TestClean_KeepsCaseAsTyped(t *testing.T) {
	for in, want := range map[string]string{
		"Müşteri Teklifi":       "Müşteri Teklifi",
		"  İSTANBUL   Ofisi  ":  "İSTANBUL Ofisi",
		"Q3\tRapor":             "Q3 Rapor",
		"u\u0308nlu\u0308":      "ünlü", // decomposed input arrives composed
		"ÇĞÖŞÜ çğöşü ıİ":        "ÇĞÖŞÜ çğöşü ıİ",
		strings.Repeat("ş", 64): strings.Repeat("ş", 64), // 128 bytes, 64 characters
	} {
		got, err := Clean(in)
		if err != nil {
			t.Fatalf("Clean(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClean_Refuses(t *testing.T) {
	for in, want := range map[string]error{
		"":                      ErrEmpty,
		"   \t ":                ErrEmpty,
		strings.Repeat("a", 65): ErrTooLong,
		"a\x00b":                ErrControl,
		"a\u200bb":              nil, // zero-width space is a format char, not control: allowed
	} {
		_, err := Clean(in)
		if !errors.Is(err, want) && !(want == nil && err == nil) {
			t.Errorf("Clean(%q) err = %v, want %v", in, err, want)
		}
	}
}

// ⚠⚠ The Turkish i. strings.EqualFold says "IŞIK" ≠ "ışık" (it folds I to i,
// never to ı) and a Turkish-locale lower case would say "INVOICE" ≠ "invoice".
// Key must say "same" for BOTH, and for the dotted capital typed either way.
func TestKey_SameTag(t *testing.T) {
	for _, group := range [][]string{
		{"Müşteri Teklifi", "müşteri teklifi", "MÜŞTERİ TEKLİFİ", "MÜŞTERI TEKLIFI", "  müşteri   teklifi "},
		{"IŞIK", "ışık", "Işık", "işik"},
		{"İstanbul", "istanbul", "ISTANBUL", "İSTANBUL", "I\u0307stanbul"},
		{"INVOICE", "invoice", "Invoice"},
		{"Straße", "STRASSE", "strasse"},
		{"ΟΔΟΣ", "οδος", "οδοσ"},
		{"ünlü", "u\u0308nlu\u0308", "ÜNLÜ"},
	} {
		want := Key(group[0])
		for _, name := range group[1:] {
			if got := Key(name); got != want {
				t.Errorf("Key(%q) = %q, want %q (same tag as %q)", name, got, want, group[0])
			}
		}
	}
	if strings.EqualFold("IŞIK", "ışık") {
		t.Fatal("premise: strings.EqualFold must NOT treat IŞIK and ışık as equal, or this test proves nothing")
	}
}

// Accents are part of the word. "müşteri" is not "musteri", "şık" is not "sık".
func TestKey_DifferentTags(t *testing.T) {
	for _, pair := range [][2]string{
		{"müşteri", "musteri"},
		{"şık", "sık"},
		{"çay", "cay"},
		{"rapor", "rapor 2"},
	} {
		if Same(pair[0], pair[1]) {
			t.Errorf("%q and %q must be different tags", pair[0], pair[1])
		}
	}
}

// Key is idempotent: a Key is its own Key. The store keeps the Key in a UNIQUE
// column, and a non-idempotent fold would let a stored key and a freshly
// computed one disagree about the same name.
func TestKey_Idempotent(t *testing.T) {
	for _, s := range []string{"Müşteri Teklifi", "IŞIK", "Straße", "ΟΔΟΣ", "İstanbul", "ǰ"} {
		k := Key(s)
		if Key(k) != k {
			t.Errorf("Key(Key(%q)) = %q, Key = %q", s, Key(k), k)
		}
	}
}

// TestKey_TheLettersAreNamefolds: the letters of a tag are folded by
// internal/namefold, the rule a search compares file names by, so a tag and
// the file name it was copied from are one word to both. `İ` lower-cased the
// full Unicode way (JavaScript, Python) is `i` + U+0307, which NFC alone
// leaves as it is; it used to be a second tag next to "İstanbul".
func TestKey_TheLettersAreNamefolds(t *testing.T) {
	dot := string(rune(0x0307))
	for _, pair := range [][2]string{
		{"i" + dot + "stanbul", "İSTANBUL"},
		{"ı" + dot + "stanbul", "istanbul"},
		{"KIŞ", "kış"},
	} {
		if !Same(pair[0], pair[1]) {
			t.Errorf("%q and %q must be one tag (keys %q, %q)", pair[0], pair[1], Key(pair[0]), Key(pair[1]))
		}
	}
}

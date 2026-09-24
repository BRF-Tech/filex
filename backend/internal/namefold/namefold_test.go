package namefold

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// dot is U+0307 as a string, spelt without an escape sequence so no editor or
// tool can turn it into an invisible character in this file.
var dot = string(rune(dotAbove))

func TestString_OneNameInEveryForm(t *testing.T) {
	cases := []struct {
		forms []string
		want  string
	}{
		// A Mac writes the name decomposed; a keyboard types it composed.
		{[]string{"Gürel", norm.NFD.String("Gürel"), "GÜREL", norm.NFD.String("GÜREL"), "gürel"}, "gürel"},
		// ⚠⚠ The four i's are one letter: an all-caps Turkish name typed in
		// lower case, and the other way round. strings.ToLower calls "IŞIK"
		// "işik" and leaves "ışık" alone — two words.
		{[]string{"IŞIK", "ışık", "Işık", "işik", norm.NFD.String("IŞIK")}, "işik"},
		{[]string{"KIŞ", "kış", "Kış"}, "kiş"},
		{[]string{"İSTANBUL", "istanbul", "ISTANBUL", "Istanbul", norm.NFD.String("İstanbul")}, "istanbul"},
		// `İ` lower-cased the full Unicode way (JavaScript, Python) is i + dot.
		{[]string{"i" + dot + "pek", "İpek", "IPEK", "ı" + dot + "pek"}, "ipek"},
		{[]string{"J" + dot + "a", "ja"}, "ja"},
		{[]string{"INVOICE", "invoice", "Invoice"}, "invoice"},
	}
	for _, c := range cases {
		for _, f := range c.forms {
			if got := String(f); got != c.want {
				t.Errorf("String(%q) = %q, want %q", f, got, c.want)
			}
		}
	}
	// Accents stay significant, and a dot above is a letter of its own on
	// anything that does not already carry one (Polish ż, decomposed).
	for _, pair := range [][2]string{{"müşteri", "musteri"}, {"şık", "sık"}, {norm.NFD.String("ż"), "z"}} {
		if String(pair[0]) == String(pair[1]) {
			t.Errorf("%q and %q must stay different words", pair[0], pair[1])
		}
	}
}

// TestRune_OneRuneInOneRuneOut: the scorer compares position by position, so
// the fold may not change how many runes a string has.
func TestRune_OneRuneInOneRuneOut(t *testing.T) {
	for _, s := range []string{"IŞIK", "ÇALIŞMA", "İSTANBUL", "ǅemal", "STRASSE"} {
		folded := strings.Map(Rune, s)
		if utf8.RuneCountInString(folded) != utf8.RuneCountInString(s) {
			t.Errorf("Rune changed the length of %q: %q", s, folded)
		}
	}
}

func TestCanonical_ComposedIsReturnedWithoutAllocating(t *testing.T) {
	for _, s := range []string{"plain ascii name.txt", "Müşteri Veritabanı.xlsx", "İPEK"} {
		if n := testing.AllocsPerRun(100, func() { _ = Canonical(s) }); n != 0 {
			t.Errorf("Canonical(%q) allocated %.0f times", s, n)
		}
		folded := String(s)
		if n := testing.AllocsPerRun(100, func() { _ = String(folded) }); n != 0 {
			t.Errorf("String(%q), already folded, allocated %.0f times", folded, n)
		}
	}
	if got := Canonical(norm.NFD.String("Gürel")); got != "Gürel" {
		t.Errorf("Canonical(NFD Gürel) = %q", got)
	}
}

// TestPlain_NothingElseFoldsIntoPlainASCII keeps Plain honest: no code point
// outside ASCII may fold (Canonical + Rune) into text holding a letter or digit
// Plain vouches for. If one does, a database looking for that letter in the
// stored bytes would miss a name the search says matches.
func TestPlain_NothingElseFoldsIntoPlainASCII(t *testing.T) {
	var offenders []string
	for r := rune(utf8.RuneSelf); r <= unicode.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		for _, f := range String(string(r)) {
			if f < utf8.RuneSelf && Plain(f) {
				offenders = append(offenders, string(r)+"→"+string(f))
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these fold into letters Plain calls stored-as-is: %v", offenders)
	}
	// And the three that are excluded really do have another spelling.
	for r, other := range map[rune]string{'i': "ı", 'j': "j" + dot, 'k': string(rune(0x212A))} {
		if Plain(r) {
			t.Errorf("Plain(%q) must be false", r)
		}
		if String(other) != string(r) {
			t.Errorf("String(%q) = %q, want %q — the exclusion has no reason", other, String(other), string(r))
		}
	}
}

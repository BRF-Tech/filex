package shortcode

import (
	"net/url"
	"strings"
	"testing"
)

// One code, every spelling it arrives in: the same canonical form, so the
// same key - and the key is ASCII hex, which every engine compares as bytes.
func TestCanonical_OneCodeManySpellings(t *testing.T) {
	grin := "\U0001F600"
	rocket := "\U0001F680"
	for _, c := range []struct {
		name string
		a, b string
		cs   bool
		same bool
	}{
		{"case matters", "abc", "ABC", true, false},
		{"case does not matter", "abc", "ABC", false, true},
		{"Turkish dotted capital folds to i", "kİz", "kiz", false, true},
		{"Turkish dotless i folds to i", "kız", "kiz", false, true},
		{"dotless i is its own letter when case matters", "kız", "kiz", true, false},
		{"emoji with and without U+FE0F", grin + rocket, grin + "\ufe0f" + rocket + "\ufe0f", true, true},
		{"text-style selector too", grin, grin + "\ufe0e", true, true},
		{"NFC and NFD", "café", "cafe\u0301", true, true},
		{"different emoji", grin, rocket, true, false},
	} {
		ca, oka := Canonical(c.a, c.cs)
		cb, okb := Canonical(c.b, c.cs)
		if !oka || !okb {
			t.Fatalf("%s: not codes (%v %v)", c.name, oka, okb)
		}
		if (Key(ca) == Key(cb)) != c.same {
			t.Fatalf("%s: %q vs %q same=%v, want %v", c.name, ca, cb, Key(ca) == Key(cb), c.same)
		}
	}
}

func TestCanonical_RefusesWhatIsNoCode(t *testing.T) {
	for _, s := range []string{
		"", " ", "a b", "a\tb", "a/b", "a?b", "a#b", "a%b", "a+b",
		"a\u200db",             // an emoji sequence joiner
		"\U0001F44D\U0001F3FD", // a skin-tone modifier
		"\U0001F1F9\U0001F1F7", // a flag (regional indicators)
		"a\u00adb", "a\u200bb", // invisible
		"x\u0301",  // a mark with nothing to compose with
		"\xff\xfe", // not UTF-8
		strings.Repeat("a", MaxLength+1),
	} {
		if c, ok := Canonical(s, true); ok {
			t.Fatalf("%q accepted as %q", s, c)
		}
	}
	if _, ok := Canonical(strings.Repeat("a", MaxLength), true); !ok {
		t.Fatal("a code of MaxLength symbols refused")
	}
}

// What a browser, a chat and a mail do to an emoji link: it travels
// percent-encoded (UTF-8), in upper- or lower-case hex, and comes back to the
// same code.
func TestEmojiURL_PercentEncodedRoundTrip(t *testing.T) {
	code := "\U0001F600\U0001F680\U0001F995"
	esc := EscapePath(code)
	if esc != "%F0%9F%98%80%F0%9F%9A%80%F0%9F%A6%95" {
		t.Fatalf("escaped %q", esc)
	}
	for _, in := range []string{esc, strings.ToLower(esc), code} {
		dec, ok := DecodeSegment(in)
		if !ok {
			t.Fatalf("%q did not decode", in)
		}
		c, ok := Canonical(dec, true)
		if !ok || c != code {
			t.Fatalf("%q -> %q (%v), want the code", in, c, ok)
		}
	}
	// A chat that added U+FE0F after each emoji, then encoded it.
	withVS := url.PathEscape("\U0001F600\ufe0f\U0001F680\ufe0f\U0001F995")
	dec, _ := DecodeSegment(withVS)
	if c, _ := Canonical(dec, true); c != code {
		t.Fatalf("with selectors: %q", c)
	}
	// What goes into a mail or a chat is ASCII, so no client cuts the link
	// at the first emoji.
	link := "https://l.example.com/" + EscapePath(code)
	for _, r := range link {
		if r > 0x7e {
			t.Fatalf("link %q is not ASCII", link)
		}
	}
	// net/url hands the same request the same way whichever form it came in.
	for _, raw := range []string{"/" + esc, "/" + code} {
		u, err := url.Parse("https://l.example.com" + raw)
		if err != nil {
			t.Fatal(err)
		}
		seg := strings.TrimPrefix(u.EscapedPath(), "/")
		dec, ok := DecodeSegment(seg)
		if !ok {
			t.Fatalf("%q: escaped path %q did not decode", raw, seg)
		}
		if c, _ := Canonical(dec, true); c != code {
			t.Fatalf("%q: %q", raw, c)
		}
	}
}

func TestDecodeSegment_Refusals(t *testing.T) {
	for _, s := range []string{"", "%", "%zz", "a%2Fb", "a%2fb", "%FF%FE"} {
		if d, ok := DecodeSegment(s); ok {
			t.Fatalf("%q decoded to %q", s, d)
		}
	}
	if d, ok := DecodeSegment("a%2Bb"); !ok || d != "a+b" {
		t.Fatalf("%%2B: %q %v", d, ok)
	}
}

func TestCheckAlias(t *testing.T) {
	if c, err := CheckAlias("Teklif-2026", false); err != nil || c != "teklif-2026" {
		t.Fatalf("alias: %q %v", c, err)
	}
	for _, a := range []string{"ab", "healthz", "robots.txt", "FAVICON.ICO", "a b c", "a/b/c"} {
		if _, err := CheckAlias(a, false); err == nil {
			t.Fatalf("%q accepted", a)
		}
	}
}

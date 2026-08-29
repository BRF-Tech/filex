package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The property that matters more than any single case: whatever the user named
// their file, the header we write is ASCII. A byte over 127 in a header value
// is what made a strict client throw mid-download.
func TestContentDisposition_IsAlwaysASCII(t *testing.T) {
	names := []string{
		"rapor.txt",
		"Türkçe adlı dosya.txt",
		"日本語のファイル.pdf",
		"emoji 🎉 dosya.png",
		"Ünlü Şarkı — Kürşad.mp3",
		"ıİşŞğĞüÜöÖçÇ.txt",
		`tırnak"lı\ad.txt`,
		"boşluklu   ad .txt",
		"ıı",          // nothing ASCII to fall back on
		"",            // no name at all
		"a/b\\c.txt",  // separators
		"satır\nsonu", // control characters
	}
	for _, n := range names {
		got := ContentDisposition("attachment", n)
		for i := 0; i < len(got); i++ {
			if got[i] > 127 {
				t.Fatalf("%q -> %q: byte %d is 0x%X, which no HTTP header may carry", n, got, i, got[i])
			}
			if got[i] == '\r' || got[i] == '\n' {
				t.Fatalf("%q -> %q: a newline in a header value splits the response", n, got)
			}
		}
		// And it must be a header Go itself accepts.
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Disposition", got)
		if v := rec.Header().Get("Content-Disposition"); v != got {
			t.Fatalf("%q did not survive a real header set", n)
		}
	}
}

func TestContentDisposition_PlainNameStaysPlain(t *testing.T) {
	got := ContentDisposition("attachment", "rapor.txt")
	want := `attachment; filename="rapor.txt"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "filename*") {
		t.Fatal("an ASCII name needs no second copy of itself")
	}
}

func TestContentDisposition_NonASCIICarriesBothForms(t *testing.T) {
	got := ContentDisposition("attachment", "Türkçe adlı dosya.txt")
	// The fallback is readable ASCII and keeps the extension…
	if !strings.Contains(got, `filename="T_rk_e adl_ dosya.txt"`) {
		t.Fatalf("ASCII fallback missing or wrong: %q", got)
	}
	// …and the real name travels percent-encoded, as UTF-8 bytes.
	if !strings.Contains(got, "filename*=UTF-8''T%C3%BCrk%C3%A7e%20adl%C4%B1%20dosya.txt") {
		t.Fatalf("RFC 5987 form missing or wrong: %q", got)
	}
}

func TestContentDisposition_SpaceIsPercentTwenty(t *testing.T) {
	// `url.QueryEscape` would write `+` here, and a client reading RFC 5987
	// keeps that as a literal plus sign in the saved filename.
	got := ContentDisposition("attachment", "iki kelime ı.txt")
	if strings.Contains(got, "+") {
		t.Fatalf("a space became '+': %q", got)
	}
	if !strings.Contains(got, "%20") {
		t.Fatalf("space not encoded as %%20: %q", got)
	}
}

func TestContentDisposition_QuotesCannotEndTheParameter(t *testing.T) {
	got := ContentDisposition("attachment", `bir"iki\uc.txt`)
	inner := got[len(`attachment; filename="`):]
	inner = inner[:strings.Index(inner, `"`)]
	if strings.ContainsAny(inner, `"\`) {
		t.Fatalf("a quote or backslash survived into the quoted string: %q", got)
	}
}

func TestContentDisposition_NameWithNoASCIIAtAllStillSaves(t *testing.T) {
	got := ContentDisposition("attachment", "ıı.txt")
	if !strings.Contains(got, `filename="file.txt"`) {
		t.Fatalf("expected a usable fallback name, got %q", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''%C4%B1%C4%B1.txt") {
		t.Fatalf("the real name should still travel: %q", got)
	}
}

func TestContentDisposition_InlineKeepsItsDisposition(t *testing.T) {
	if got := ContentDisposition("inline", "a.png"); !strings.HasPrefix(got, "inline; ") {
		t.Fatalf("disposition lost: %q", got)
	}
	if got := ContentDisposition("", "a.png"); !strings.HasPrefix(got, "attachment; ") {
		t.Fatalf("empty disposition should mean attachment: %q", got)
	}
}

// A guard for the shape of the thing: it has to parse as a real header.
func TestContentDisposition_ParsesAsAHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Content-Disposition", ContentDisposition("attachment", "Türkçe adlı dosya.txt"))
	if req.Header.Get("Content-Disposition") == "" {
		t.Fatal("header did not round-trip")
	}
}

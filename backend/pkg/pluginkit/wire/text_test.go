package wire

import "testing"

// An app's text in the reader's language, per string: the language, its base
// language, English — never another language the app happens to have, and
// the same answer every time.
func TestTextGet_FallsBackPerString(t *testing.T) {
	sign := Text{"en": "Sign", "tr": "İmzala", "es": "Firmar", "pt": "Assinar"}
	for lang, want := range map[string]string{
		"es": "Firmar", "tr": "İmzala", "en": "Sign",
		"pt-br": "Assinar", // the base language of a regional tag
		"de":    "Sign",    // an app without German: English, not Turkish
		"":      "Sign",
	} {
		if got := sign.Get(lang); got != want {
			t.Errorf("Get(%q) = %q, want %q", lang, got, want)
		}
	}
	noEnglish := Text{"tr": "b", "de": "a"}
	for i := 0; i < 20; i++ {
		if got := noEnglish.Get("es"); got != "a" {
			t.Fatalf("no English: %q, want the first by tag every time", got)
		}
	}
	if got := Text(nil).Get("es"); got != "" {
		t.Errorf("nil Text: %q", got)
	}
}

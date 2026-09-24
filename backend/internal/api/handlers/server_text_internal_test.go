package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// requestLang is the order every server-written text for a signed-in reader
// takes: the flow's explicit choice, the account, the browser, the instance
// default, English — each only if the catalogue speaks it.
func TestRequestLang_Order(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{"es": {"server.mail.greeting": "Hola:"}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "tr-TR,tr;q=0.9")
	assert.Equal(t, "tr", requestLang(r), "no account: the browser")

	withUser := r.WithContext(auth.WithUser(r.Context(), &model.User{ID: 1, Locale: "es"}))
	assert.Equal(t, "es", requestLang(withUser), "the account's pack language beats the browser")
	assert.Equal(t, "en", requestLang(withUser, "en"), "the flow's explicit choice beats the account")
	assert.Equal(t, "es", requestLang(withUser, "de"), "an explicit choice nobody speaks is skipped, not obeyed")

	bare := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, "en", requestLang(bare))
}

// The OIDC bounce page is two words long and a person reads both before the
// panel paints — in the browser's language, since nobody is signed in yet.
func TestOIDCBounce_SpeaksTheBrowsersLanguage(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{"es": {"server.public.signing_in": "Iniciando sesión…", "server.public.continue": "Continuar"}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })

	r := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
	r.Header.Set("Accept-Language", "es-AR,es;q=0.9")
	rec := httptest.NewRecorder()
	writeOIDCBounce(rec, r, "/admin/")
	body := rec.Body.String()
	assert.Contains(t, body, `<html lang="es" dir="ltr">`)
	assert.Contains(t, body, "<title>Iniciando sesión…</title>")
	assert.Contains(t, body, `<a href="/admin/">Continuar</a>`)
	assert.Contains(t, body, `location.replace("\/admin\/")`, "the target is still context-escaped")

	tr := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
	tr.Header.Set("Accept-Language", "tr")
	rec = httptest.NewRecorder()
	writeOIDCBounce(rec, tr, "/admin/")
	assert.Contains(t, rec.Body.String(), "Oturum açılıyor…")
}

// ⚠⚠ What an APP is told: the reader's real language, by the same rule as
// the screens and the server's own text. It was normLang — "tr" for anything
// Turkish and "en" for everything else — so a Spanish user's signing wizard
// was told `en` and an app that ships Spanish could never show it.
func TestLangOf_AppsHearTheRealLanguage(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{"es": {"nav.files": "Archivos"}, "pt-br": {"nav.files": "Arquivos"}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })
	as := func(locale, accept string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		if locale != "-" {
			r = r.WithContext(auth.WithUser(r.Context(), &model.User{ID: 1, Locale: locale}))
		}
		return r
	}
	withEs := wire.Text{"en": "Sign", "tr": "İmzala", "es": "Firmar"}
	noEs := wire.Text{"en": "Sign", "tr": "İmzala"}

	assert.Equal(t, "es", langOf(as("es", "")))
	assert.Equal(t, "Firmar", textOf(withEs, as("es", "")), "an app that ships Spanish shows it to a Spanish reader")
	assert.Equal(t, "Sign", textOf(noEs, as("es", "")), "one that does not shows English — not Turkish, not a key")
	assert.Equal(t, "pt-br", langOf(as("pt-BR", "")), "a region is kept")
	assert.Equal(t, "es", langOf(as("-", "es-AR,es;q=0.9")), "no account: the browser")
	// English and Turkish exactly as before.
	assert.Equal(t, "tr", langOf(as("tr", "en")))
	assert.Equal(t, "tr", langOf(as("", "tr-TR,tr;q=0.9")))
	assert.Equal(t, "en", langOf(as("en", "tr")))
	assert.Equal(t, "en", langOf(as("-", "")))
	assert.Equal(t, "İmzala", textOf(noEs, as("tr", "")))
	// A language nothing offers is English — never Turkish.
	assert.Equal(t, "en", langOf(as("de", "")))
}

// The uploader's name is what the owner reads in the notice, the mail and
// NOT.txt — in whatever script it was typed. It knew ASCII and the Turkish
// letters: "Lucía" arrived as "Luca", an Arabic name as nothing ("anon").
func TestSanitizeSubName_KeepsEveryScript(t *testing.T) {
	for in, want := range map[string]string{
		"Lucía":              "Lucía",
		"François Müller":    "François Müller",
		"Şükrü Öztürk":       "Şükrü Öztürk",
		"عليّ":               "عليّ",
		"  ../../etc/x  ":    "etcx",
		`a/b\c:d*e?`:         "abcde",
		"-_ Ana _-":          "Ana",
		"<script>x</script>": "scriptxscript",
	} {
		assert.Equal(t, want, sanitizeSubName(in), "%q", in)
	}
	long := sanitizeSubName("Çağrı Şükrü Öztürk Çağrı Şükrü Öztürk Çağrı Şükrü")
	assert.True(t, utf8.ValidString(long), "cut by characters, never inside a letter")
	assert.LessOrEqual(t, utf8.RuneCountInString(long), 40)
}

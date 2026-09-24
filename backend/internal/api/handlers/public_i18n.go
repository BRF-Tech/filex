package handlers

/* Public pages speak ONE language per visitor.

They grew one at a time and each picked its own: the PIN gate was English, the
page behind it Turkish, and the "PIN accepted" screen managed both at once — an
English <title> over a Turkish heading. Somebody opening a share link therefore
changed language by entering a PIN.

There is no session and no user here — a share link is opened by strangers — so
the language comes from the request itself: an explicit ?lang= (useful for
testing and for sending a link to somebody whose browser is set to neither),
then Accept-Language, then the server's own default. Resolved ONCE per request
and handed to the template, so every string on a page comes from the same
table.

The strings are the server catalogue's `server.public.*` keys
(internal/srvtext): English and Turkish built in, and any language a
language pack adds — a Spanish browser opening a drop link reads Spanish when
a Spanish pack is installed (it read "Send files" until v0.43.0, because
this file knew two languages and nothing else). */

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// publicLocale picks the language for one public request: ?lang=, then the
// browser's Accept-Language, then the server default — each only when the
// server catalogue speaks it (a pack's language counts) — then English.
func publicLocale(r *http.Request, serverDefault string) string {
	if r != nil {
		if v := srvtext.Resolve(r.URL.Query().Get("lang")); v != "" {
			return v
		}
		if v := srvtext.FromAcceptLanguage(r.Header.Get("Accept-Language")); v != "" {
			return v
		}
	}
	return srvtext.Pick(serverDefault)
}

// publicLocaleList is the languages filex ships (en, tr), sorted so the
// answer is stable across restarts. The languages packs add are appended by
// the caller from the registry (public_api.go Branding).
func publicLocaleList() []string {
	return srvtext.BuiltinLanguages()
}

// userLang is the signed-in caller's account language, "" when there is no
// caller or it never picked one.
func userLang(r *http.Request) string {
	if r == nil {
		return ""
	}
	if u := auth.UserFrom(r.Context()); u != nil {
		return strings.TrimSpace(u.Locale)
	}
	return ""
}

// requestLang is the language server-written text for THIS request's reader
// is in: the flow's own candidates first (an explicit choice the request
// carries), then the caller's account language, then the browser's
// Accept-Language, then the instance default, then English.
//
// ⚠ The same order langOf uses for a plugin call (account, then
// Accept-Language) — lesson #214: a person who never picked a language in
// their profile must not get English text out of a flow whose screen spoke
// to them in Turkish. The difference is that this one keeps a pack's language
// instead of narrowing to en/tr.
func requestLang(r *http.Request, first ...string) string {
	cands := append(append([]string{}, first...), userLang(r))
	if r != nil {
		cands = append(cands, srvtext.FromAcceptLanguage(r.Header.Get("Accept-Language")))
	}
	return srvtext.Pick(cands...)
}

// pageDir is the `dir` a server-rendered page carries on <html>, derived from
// the same `lang` it carries (feat/043-rtl): "rtl" for a right-to-left
// language — a pack's Arabic or Hebrew now reaches these pages through the
// server catalogue (feat/043-srvtext) — "ltr" for everything else. ⚠ The one
// list is wire.IsRTL, as for the explorer and the admin panel; a page that
// set `lang="ar"` without `dir` drew Arabic words in a left-to-right layout.
func pageDir(lang string) string {
	if wire.IsRTL(lang) {
		return "rtl"
	}
	return "ltr"
}

// publicPageLang resolves everything a public page needs to render in ONE
// language: the tag for <html lang>, the string table, the branded chrome and
// the footer that matches the language.
//
// Share and Drop both go through here. Drop used to render its own pages with
// no table at all — the PIN gate on a drop link came out with an empty <title>,
// an empty heading and an unlabelled input, because the template asks for
// {{.T.pin_heading}} and nobody passed T (html/template renders a missing key
// as the empty string, and the Execute error was discarded). A public surface
// that renders its own strings is a surface that can silently lose them.
func publicPageLang(br *BrandingSource, r *http.Request, defaultLocale string) (string, map[string]string, publicChrome, template.HTML) {
	lang := publicLocale(r, defaultLocale)
	c := publicChromeFor(br, r)
	return lang, publicT(lang), c, c.Footer(lang)
}

// publicT returns the string table for a language, always non-nil and always
// complete: every `server.public.*` key, the language's own where it has one
// and English where it does not. Keys are the short names the templates use
// (`pin_title`); values keep their `{placeholders}` for the caller to fill.
func publicT(lang string) map[string]string {
	return srvtext.Table(lang, srvtext.Prefix+"public.")
}

// publicLinkSentence is ONE page sentence whose `{link}` is an anchor.
//
// ⚠⚠ Not three keys around an <a>. The ZIP wait page used to say
// `zip_hint_a` + `zip_hint_b` (the link's words) + `zip_hint_c` (a full
// stop), which is a sentence a translator cannot reorder, cannot punctuate
// and cannot even see whole — the same anti-pattern `access.ui.create_then_send`
// was fixed for in v0.43.0. The sentence is one message with a placeholder;
// only the words INSIDE the link are a second key, because they are a label.
//
// Everything from the catalogue is HTML-escaped: a language pack is installed
// by an administrator, and a pack is not markup.
func publicLinkSentence(t map[string]string, key, linkKey, id, href string) template.HTML {
	esc := template.HTMLEscapeString
	anchor := `<a id="` + esc(id) + `" href="` + esc(href) + `">` + esc(t[linkKey]) + `</a>`
	parts := strings.SplitN(esc(t[key]), "{link}", 2)
	if len(parts) != 2 {
		// srvtext.fits refuses a translation that drops a placeholder, so this
		// is the built-in English going missing — say the link anyway.
		return template.HTML(esc(t[key]) + " " + anchor)
	}
	return template.HTML(parts[0] + anchor + parts[1])
}

// publicFill is one public-page string with its placeholders filled.
func publicFill(t map[string]string, key string, vars map[string]string) string {
	return srvtext.Fill(t[key], vars)
}

// publicCount is a count-bearing public string ("3 files" / "1 file") in
// lang: the form of n's plural category (srvtext.Plural).
func publicCount(lang, key string, n int) string {
	return srvtext.Plural(lang, srvtext.Prefix+"public."+key, n, nil)
}

// publicFooterLine is the "Shared with filex" line in lang, as HTML.
//
// ⚠ The ONE place a translation meets markup: the sentence is the catalogue's
// (`server.public.footer`, "Shared with {filex}") and the brand link is not.
// The translation is HTML-ESCAPED FIRST and the link is put in afterwards, so
// a pack cannot inject markup into a page strangers open — the worst it can do
// is misplace the word "filex".
func publicFooterLine(lang string) string {
	const brandLink = `<a href="https://filex.sh" target="_blank" rel="noopener">filex</a>`
	line := template.HTMLEscapeString(srvtext.Template(lang, srvtext.Prefix+"public.footer"))
	line = strings.Replace(line, "{filex}", brandLink, 1)
	return `<footer class="brand">` + publicBrandMark + `<span>` + line + `</span></footer>`
}

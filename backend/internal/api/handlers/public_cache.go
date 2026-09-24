package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// writePublicJSON serves one of the small public answers that say who this
// instance IS — its branding, its themes, the languages it offers, and the
// strings of one of those languages — with a strong ETag over the exact bytes
// and `no-cache`, so a reader always ASKS whether its copy is still current
// and is told "yes" for nothing.
//
// ⚠⚠ Why not `max-age`. These four were `public, max-age=60`, and the
// offered-language list is on one of them (PublicBranding.Locales /
// .UILocales). An administrator who installed a language pack and reloaded
// therefore did not see the language for up to a minute — measured in the
// v0.43.0 right-to-left round, where it cost a spec its natural shape before
// anybody noticed what it meant. A minute of "I installed it and nothing
// happened" is reported as a broken install, and the same minute applied to a
// theme just composed and to a name just changed.
//
// ⚠ `no-cache` is NOT `no-store`. The browser and any shared cache still KEEP
// the body; they only have to ask before reusing it, and the answer to an
// unchanged question is `304` with no body at all. That is what keeps the
// public share and sign-in pages cheap — including the one payload where it
// really matters, a language pack's strings, which is ~300 KB for Arabic and
// now stops being served from yesterday's pack after an upgrade as well.
// The cost is one conditional request per page load, and every one of these
// payloads is memoised in process (BrandingSource, AppearanceSource — both
// dropped on write — and the app registry, which is live), so a 304 costs a
// hash and not a query.
//
// ⚠ The tag is over the BODY, so it moves for every reason the answer moves —
// an app installed, removed or upgraded, a theme saved, a logo changed —
// rather than for a version stamp somebody has to remember to bump.
//
// ⚠⚠ The one exception to api.APINoStore, and it has to stay exactly that.
// Every other /api answer is `no-store`, because a CDN rule that cached
// everything once served one administrator's GET /api/auth/me to everybody
// (PR #41). These four are the same for every visitor of a host — which is why
// they may be shared — and they are mounted outside every auth chain, so no
// principal ever reaches them. If this helper is ever called from a route that
// DID authenticate somebody, it says `private` instead: an answer written for
// a person is never offered to a shared cache, whatever the caller meant.
// TestAPICache_PublicOnlyOnTheInstanceIdentity (package api) walks the route
// table and TestWritePublicJSON_OnlyTheFourIdentityAnswers pins the callers.
//
// ⚠ This does NOT set `Vary`. Only one of these answers has more than one
// body per URL — /api/public/branding resolves the visitor's language into
// `locale` — and a handler that varies says so itself (`Vary: Accept-Language`,
// ADDED, because the CORS middleware has already put `Origin` there). Telling
// a shared cache that the themes vary by language would only split its keys
// for nothing.
func writePublicJSON(w http.ResponseWriter, r *http.Request, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encode"})
		return
	}
	// The newline json.Encoder wrote before this function existed, so a client
	// comparing bytes across an upgrade sees the same answer.
	b = append(b, '\n')

	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", publicCacheControl(r))
	h.Set("Content-Type", "application/json")

	if r != nil && etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// publicCacheControl is `public, no-cache` for an anonymous request — the only
// kind the four identity routes ever see — and `private, no-cache` for one a
// middleware attached a principal to (see writePublicJSON).
func publicCacheControl(r *http.Request) string {
	if r != nil && auth.UserFrom(r.Context()) != nil {
		return "private, no-cache"
	}
	return "public, no-cache"
}

// etagMatches answers RFC 9110 `If-None-Match`: `*`, or a comma-separated
// list with one entry equal to ours.
//
// ⚠ Weak comparison (`W/"x"` matches `"x"`): a proxy is allowed to weaken a
// tag it re-serves, and for an answer a page merely reads "the same bytes"
// and "equivalent" are the same question.
func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == etag {
			return true
		}
	}
	return false
}

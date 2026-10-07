// Package api - origin_guard.go
//
// The router's cross-site request forgery guard (internal/originguard): what
// it trusts and which routes it leaves alone, read from this router's own
// configuration.
package api

import (
	"net/http"
	"strings"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/originguard"
	"github.com/brf-tech/filex/backend/internal/s3api"
)

// originGuardExempt are the routes whose credential is part of the request
// itself, so a page elsewhere gains nothing by sending one through somebody's
// browser: it could send the same request from anywhere. They are also the
// routes another origin legitimately calls without a key (a share opened in an
// embed, a presigned S3 upload, an upload ticket handed to a script).
//
// Matched by whole path segments, below the base path. A new route belongs
// here only when it reads no session at all: every other route is judged,
// which is what keeps a route nobody thought about protected.
var originGuardExempt = []string{
	// Share, drop and app-page links (handlers/public_api.go): the link's
	// token and, when it has one, its PIN.
	"/api/public",
	// The same links' pages without JavaScript: the PIN form and the
	// multipart drop post to them.
	"/s",
	"/d",
	// Upload ticket redemption: the ticket is single-use and names one write.
	"/u",
	// The document server's save callback, signed with the shared secret.
	"/api/files/onlyoffice/callback",
	// The desktop sign-in's second half: the PKCE verifier is the proof.
	"/api/auth/desktop/exchange",
	// S3 requests are signed (SigV4 headers or a presigned URL).
	s3api.Prefix,
}

// originGuard is the guard BuildRouter installs, built from d.
//
// Trusted is the CORS list (FILEX_CORS_ALLOWED_ORIGINS), on purpose the same
// list and not a second one: a page on another origin that calls filex with
// the visitor's session needs that list's preflight answer anyway, so the
// operator has already named every legitimate one there. The app-interface
// origin (FILEX_APP_UI_ORIGIN) is deliberately not trusted: an interface runs
// sandboxed without allow-same-origin, so its requests carry `Origin: null`,
// and trusting the host would only matter if that sandbox were ever loosened,
// which is when third-party app code must not be able to act as the person.
// It is Untrusted outright, with the ONLYOFFICE frame origin (task #92), so a
// CORS wildcard that happens to cover either changes nothing.
func originGuard(d *Deps) func(http.Handler) http.Handler {
	self := ""
	if d.Cfg.PublicURLSet {
		self = d.Cfg.PublicURL
	}
	return originguard.New(originguard.Config{
		Trusted:       d.Cfg.CORS.AllowedOrigins,
		Untrusted:     untrustedOrigins(d),
		Self:          self,
		Exempt:        originGuardExempt,
		SessionCookie: authlocal.SessionCookieName,
		Store:         d.Store,
	}).Middleware
}

// untrustedOrigins are the origins filex knows run code it did not write: the
// ONLYOFFICE editor's frame origin (FILEX_ONLYOFFICE_FRAME_ORIGIN, normally
// the document server's own, task #92) and the app-interface origin. Neither
// is ever trusted to change something with a person's session, nor to read
// filex's answers, whatever FILEX_CORS_ALLOWED_ORIGINS says: a wildcard there
// (`https://*.example.com`) can cover the document server's host by accident.
//
// ⚠ The frame origin is usually the SAME SITE as filex (docs.example.com
// beside files.example.com), so SameSite=Lax sends the session cookie with its
// requests; this list and the guard's Sec-Fetch-Site rule are what refuse
// them. The cookie itself is HttpOnly, so no script there can read it.
func untrustedOrigins(d *Deps) []string {
	var out []string
	for _, o := range []string{d.Cfg.ExternalServices.OnlyOffice.FrameOrigin, d.Cfg.AppUIOrigin} {
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}

// corsNever keeps the CORS layer's answer from the untrusted origins: their
// Access-Control-* headers are taken off, so a browser lets no script there
// read what filex answered - with credentials or without - and refuses their
// preflights, whatever the CORS list says. Every other request passes with
// its writer untouched (a WebSocket upgrade still finds its Hijacker).
func corsNever(origins []string) func(http.Handler) http.Handler {
	set := map[string]bool{}
	for _, raw := range origins {
		if o, ok := originguard.Canonical(raw); ok {
			set[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		if len(set) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o, ok := originguard.Canonical(r.Header.Get("Origin"))
			if !ok || !set[o] {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&noCORSWriter{ResponseWriter: w}, r)
		})
	}
}

// noCORSWriter drops the Access-Control-* headers at the moment the answer's
// headers go out.
type noCORSWriter struct {
	http.ResponseWriter
	done bool
}

func (w *noCORSWriter) strip() {
	if w.done {
		return
	}
	w.done = true
	h := w.Header()
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Access-Control-") {
			h.Del(k)
		}
	}
}

func (w *noCORSWriter) WriteHeader(code int) {
	w.strip()
	w.ResponseWriter.WriteHeader(code)
}

func (w *noCORSWriter) Write(p []byte) (int, error) {
	w.strip()
	return w.ResponseWriter.Write(p)
}

// FlushError and Unwrap: http.ResponseController reaches the writer
// underneath, after the headers are decided.
func (w *noCORSWriter) FlushError() error {
	w.strip()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *noCORSWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

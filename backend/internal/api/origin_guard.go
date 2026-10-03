// Package api - origin_guard.go
//
// The router's cross-site request forgery guard (internal/originguard): what
// it trusts and which routes it leaves alone, read from this router's own
// configuration.
package api

import (
	"net/http"

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
func originGuard(d *Deps) func(http.Handler) http.Handler {
	self := ""
	if d.Cfg.PublicURLSet {
		self = d.Cfg.PublicURL
	}
	return originguard.New(originguard.Config{
		Trusted:       d.Cfg.CORS.AllowedOrigins,
		Self:          self,
		Exempt:        originGuardExempt,
		SessionCookie: authlocal.SessionCookieName,
		Store:         d.Store,
	}).Middleware
}

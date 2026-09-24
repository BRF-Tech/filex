package api

import (
	"net/http"
	"strings"
)

// APINoStore makes every /api response uncacheable unless its handler sets a
// Cache-Control of its own. Handlers override it with Header().Set — never Add:
// no-store beside a max-age cancels the max-age.
//
// ⚠ Without it a JSON answer carried no Cache-Control at all, and a CDN rule
// that caches everything took that as permission. Measured behind Cloudflare
// (2026-09-24): a zone rule written for the tenant's website, with no host
// condition, kept GET /api/auth/me for two hours and handed one
// administrator's identity to everyone who asked, an anonymous curl included.
// Signing out and in as someone else still showed the administrator, because
// the identity request never reached filex. Per-user data has no business in a
// shared cache, nor in the browser's after sign-out.
//
// ⚠⚠ The ONE deliberate exception is the four answers that say who this
// instance is — GET /api/branding, /api/appearance, /api/public/branding and
// /api/public/ui-locales/{code}. They are the same for every visitor of a host
// and are meant to be KEPT and revalidated (`public, no-cache` + a strong
// ETag, so an unchanged page load costs a 304): handlers/public_cache.go. They
// are mounted outside every auth chain, so no principal reaches them, and
// writePublicJSON downgrades itself to `private` should anybody ever call it
// with one. TestAPICache_PublicOnlyOnTheInstanceIdentity walks the whole route
// table and fails on a fifth `public` answer.
//
// The other policies a handler sets are per-person or unstored: thumbnails,
// file content and share downloads say `private, …`; archives, OnlyOffice and
// the OIDC bounce say `no-store` themselves.
func APINoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// Package api — demo_guard.go
//
// The read-only guard for a public demo.
package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// demoGuardedPrefixes are the paths a public demo refuses to let a visitor
// CHANGE. Reading them stays open, because a demo that hides its admin panel
// is not demonstrating the product.
//
// A demo publishes an admin login on purpose — that is what a demo is for —
// so "admin-only" and "public" are the same set of people here. Two questions
// decide whether a route belongs on this list:
//
//  1. Can it lock the next visitor out? The account is SHARED. One person
//     changing its password, its e-mail or enrolling TOTP takes the demo away
//     from everybody else until the nightly restore. That is the whole
//     identity surface, not just the admin half of it.
//  2. Does it reconfigure the instance or reach OUTWARD from the server?
//     Repointing a storage, adding a webhook, sending an SMTP test, applying
//     a self-update: a stranger deciding what the server connects to.
//
// Everything else a visitor does — uploading, renaming, deleting, sharing,
// tagging, minting themselves a token — is the product, and the nightly
// restore is what makes that safe.
//
// ⚠ Prefixes, not a route list. New admin routes are added constantly (this
// tree grew from 60 to 101 in three months); a guard enumerating handlers is
// one merge away from a hole, while a guard on the mount point covers a route
// that does not exist yet.
//
// ⚠ /api/ai/admin is the SAME admin surface behind an admin-scoped API token.
// Measured on a local demo before this guard existed: the visitor minted one
// at POST /api/admin/ai-tokens (201), then drove PATCH /api/ai/admin/settings
// with it (200) and the change read back through the normal admin API. Guard
// one mount point and the other one is the bypass.
var demoGuardedPrefixes = []string{
	"/api/admin",
	"/api/ai/admin",
	"/api/auth/password",
	"/api/auth/profile",
	"/api/auth/totp",
}

// demoGuardBlocks reports whether a demo instance must refuse this request.
//
// Read verbs always pass: the admin pages have to render. OPTIONS passes too
// — refusing a CORS preflight would answer the browser's question about a
// request that is itself refused, with the wrong status.
func demoGuardBlocks(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	for _, p := range demoGuardedPrefixes {
		// Segment-aware: "/api/admin" must not also claim "/api/administer".
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// DemoGuard refuses state-changing requests on a public demo.
//
// enabled is config.Demo.Mode. When it is false this middleware is a
// pass-through with a single boolean test, so an ordinary install carries no
// behaviour change at all.
func DemoGuard(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !demoGuardBlocks(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "this is a public demo: the admin surface is read-only here, " +
					"and the shared demo account cannot be changed — run your own filex to try this",
				"demo": "read-only",
			})
		})
	}
}

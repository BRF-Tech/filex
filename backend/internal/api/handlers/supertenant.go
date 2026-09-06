// Package handlers — supertenant.go
//
// ONE gate for the admin surfaces that are INSTANCE-WIDE.
//
// # The distinction the /api/admin block does not draw for you
//
// Every route under /api/admin has already passed `auth.RequireAdmin`, and
// `auth.TenantResolver` has attached the requester's tenant scope. Neither of
// those denies anything by itself: the resolver only labels the request, and
// the scoped store filters exactly three list queries (storages, enabled
// storages, users). So in multi-tenant mode "admin" means *an* admin — of any
// tenant — and a route whose effect is instance-wide is, by default, open to
// all of them.
//
// Most admin routes are fine with that because their DATA belongs to a tenant.
// A handful do not: they read and write a single global row, or drive a single
// process, that every tenant then lives with. Those are the ones that must ask
// this question, and the consequence of forgetting is not a leak but a
// takeover — the tenant admin of the smallest customer switching antivirus off
// for the whole platform, or repointing the shared document server at a host
// they control.
//
// # Single-tenant installs are untouched, by construction
//
// `auth.TenantResolver` returns the next handler unchanged when multi-tenant
// mode is off (auth/tenant_middleware.go), so NO scope is ever attached and
// `tenant.FromContext` reports absence. Absence means "unscoped" and passes
// here. The ordinary admin of a non-multi-tenant instance therefore still
// administers everything, with no flag to thread and nothing to configure —
// the gate is invisible until somebody turns tenancy on.
//
// # Why the gate lives in the handler and not on the route
//
// Because the route is not the only door. `/api/ai/admin` (handlers/ai_admin.go
// `Register`) mounts the SAME handler instances behind an `admin`-scoped API
// token, and the MCP admin tools drive those same methods in-process through
// `AIAdmin.invoke`. A chi middleware in routes.go would gate the browser's
// door and leave the other two standing open. A check at the top of the method
// is on the inside of all three.
package handlers

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/tenant"
)

// requireSupertenant reports whether this request may touch an instance-wide
// admin surface, answering 403 itself when it may not. `what` is the sentence
// the operator reads, so it names the thing being refused rather than
// restating the rule.
//
// ⚠ Fails CLOSED on a present-but-unresolvable scope. In multi-tenant mode
// `auth.ScopeForUser` hands an authenticated user whose provider is missing or
// unknown `tenant.DenyAll`, precisely so a broken tenancy record sees nothing
// instead of everything; a gate that read that as "no scope, therefore
// single-tenant, therefore allow" would invert it.
func requireSupertenant(w http.ResponseWriter, r *http.Request, what string) bool {
	scope, ok := tenant.FromContext(r.Context())
	if !ok {
		// Single-tenant mode (or a pre-auth path, which cannot reach an admin
		// route: auth.Middleware(true) runs first). Unscoped = allowed.
		return true
	}
	if scope != nil && scope.IsSupertenant {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "supertenant_only",
		"message": what,
	})
	return false
}

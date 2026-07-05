package auth

import (
	"net/http"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// TenantResolver derives the request's tenant (provider) scope from the
// authenticated user's provider_id and stashes it in the context for the scoped
// store and storage confinement to read. It is a **no-op when multi-tenant mode
// is off** — no scope is set, and callers treat the absence as "unscoped", so
// single-tenant behaviour is byte-unchanged. Mount it AFTER the auth middleware
// so the user is already resolved.
//
// The user's own provider_id — not the request Host — is the source of truth
// for scoping: a user belongs to exactly one tenant regardless of which host
// they reach. (Host binding drives which OIDC realm authenticates a *login*;
// that is the login layer's concern, not request scoping.)
//
// A supertenant scope carries IsSupertenant + no StorageIDs; it is
// confine-exempt and cross-tenant (see docs/MULTI-TENANCY.md).
func TenantResolver(store db.Store, multiTenant bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !multiTenant {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			if u == nil {
				// Unauthenticated / public path — no user, no scope. The scoped
				// store leaves an unscoped context alone (that is how workers and
				// pre-auth paths behave); public handlers do their own checks.
				next.ServeHTTP(w, r)
				return
			}
			// Authenticated request in multi-tenant mode: ALWAYS attach a scope so
			// the scoped store filters. A user whose provider is missing or
			// unknown gets tenant.DenyAll (sees nothing) — fail closed, never a
			// silent fall-through to "see everything".
			scope := tenant.DenyAll
			if u.ProviderID != nil {
				if p, err := store.GetProvider(r.Context(), *u.ProviderID); err == nil && p != nil {
					scope = &tenant.Scope{ProviderID: p.ID, Slug: p.Slug, IsSupertenant: p.IsSupertenant}
					if !p.IsSupertenant {
						scope.StorageIDs, _ = store.ListProviderStorageIDs(r.Context(), p.ID)
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(tenant.WithScope(r.Context(), scope)))
		})
	}
}

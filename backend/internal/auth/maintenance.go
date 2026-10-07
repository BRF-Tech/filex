package auth

import (
	"encoding/json"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/db"
)

// MaintenanceLockout is the tenant gate of a server started with multi-tenant
// mode OFF on an install that has tenants besides the platform's own: the
// maintenance mode of docs/MULTI-TENANCY.md, held on every request and not
// only at sign-in.
//
// # Why the sign-in gate was not enough
//
// LoginBlockReason refuses a tenant's account a NEW session while the mode is
// off. A session that account opened while the mode was on, and every API key
// it holds, kept working after the restart, and with the mode off no request
// is scoped to a tenant (TenantResolver is a no-op): a tenant's administrator
// was an administrator of the whole instance until the session ran out. The
// switch on Admin → Multi-tenant mode made that one click away, so the gate
// is here too.
//
// It answers 401 with `reason: maintenance`, the code the sign-in page already
// explains, to a request whose account belongs to a tenant other than the
// platform's own. The session and the keys are left as they are: turning the
// mode back on makes them work again, like everything else of the tenant.
//
// Mount it where TenantResolver would be, after the authentication
// middleware, and only when the server decided at start that the mode is off
// and tenants exist (internal/server → api.Deps.TenantLockout). A plain
// single-tenant install never pays its lookup.
//
// Like LoginBlockReason it fails toward availability: an account with no
// provider, or a provider that cannot be read, is let through.
func MaintenanceLockout(store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := UserFrom(r.Context()); u != nil && u.ProviderID != nil && store != nil {
				p, err := store.GetProvider(r.Context(), *u.ProviderID)
				if err == nil && p != nil && !p.IsSupertenant {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":  "unauthorized",
						"reason": SSOReasonMaintenance,
					})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

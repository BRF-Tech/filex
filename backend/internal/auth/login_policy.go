package auth

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// LoginAllowed reports whether an authenticated user may start a session.
//
// Two tenant-level gates (docs/MULTI-TENANCY.md):
//   - SUSPEND: a disabled provider's users cannot log in (data intact) — in
//     both modes. The supertenant cannot be suspended out of its own platform.
//   - MAINTENANCE MODE: multi-tenant OFF on an install that has grown tenants
//     ⇒ only the supertenant provider's users may log in; tenants are locked
//     out until the flag returns (reversible, nothing touched). A plain
//     single-tenant install is unaffected — its only provider IS the
//     supertenant.
//
// It fails toward availability: a NULL provider (bootstrap/legacy admin) or an
// unresolvable provider is allowed, so an operator is never locked out by a
// glitch. Only a resolvable, non-supertenant tenant is ever refused.
func LoginAllowed(ctx context.Context, store db.Store, multiTenant bool, u *model.User) bool {
	if u == nil || u.ProviderID == nil {
		return true
	}
	p, err := store.GetProvider(ctx, *u.ProviderID)
	if err != nil || p == nil {
		return true
	}
	if p.IsSupertenant {
		return true
	}
	if !p.Enabled {
		return false // suspended tenant
	}
	return multiTenant // mode off + tenants exist ⇒ maintenance lockout
}

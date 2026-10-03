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
// One user-level gate:
//   - DISABLED ACCOUNT: users.enabled = false (migration 00022) refuses the
//     session outright, in both modes, with files/quota/grants untouched.
//     Checked first so it holds in a plain single-tenant install too.
//
// Otherwise it fails toward availability: a NULL provider (bootstrap/legacy
// admin) or an unresolvable provider is allowed, so an operator is never
// locked out by a glitch. Only a resolvable, non-supertenant tenant is ever
// refused.
func LoginAllowed(ctx context.Context, store db.Store, multiTenant bool, u *model.User) bool {
	return LoginBlockReason(ctx, store, multiTenant, u) == ""
}

// LoginBlockReason is LoginAllowed with the gate that said no: "" (allowed),
// SSOReasonAccountDisabled (SSOReasonAccountPending for an account an SSO
// sign-in opened switched off, still waiting for an administrator),
// SSOReasonTenantSuspended or SSOReasonMaintenance. The SSO callback sends it
// to the sign-in page (handlers.OIDCCallback), which used to land the person
// on a page that said nothing at all.
func LoginBlockReason(ctx context.Context, store db.Store, multiTenant bool, u *model.User) string {
	if u == nil {
		return ""
	}
	if !u.Enabled {
		if u.DisabledReason == model.DisabledPendingApproval {
			return SSOReasonAccountPending
		}
		return SSOReasonAccountDisabled
	}
	if u.ProviderID == nil {
		return ""
	}
	p, err := store.GetProvider(ctx, *u.ProviderID)
	if err != nil || p == nil {
		return ""
	}
	if p.IsSupertenant {
		return ""
	}
	if !p.Enabled {
		return SSOReasonTenantSuspended
	}
	if !multiTenant {
		return SSOReasonMaintenance // mode off + tenants exist ⇒ maintenance lockout
	}
	return ""
}

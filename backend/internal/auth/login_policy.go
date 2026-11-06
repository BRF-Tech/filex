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
	p := tenantOf(ctx, store, u)
	if p == nil {
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

// AccountBlock is why an account that already holds a credential - a session,
// an API token, a socket ticket - may not act with it now: "" (it may),
// SSOReasonAccountDisabled (switched off, an account waiting for approval
// included) or SSOReasonTenantSuspended (its tenant is suspended). ⭐ The one
// reading of that question for every door a held credential opens
// (RefuseBlockedAccount writes its answer; an open realtime socket asks it
// again while it stays open).
//
// It is LoginBlockReason for a credential instead of a sign-in, with one gate
// left out on purpose: maintenance mode is held on every request by
// MaintenanceLockout, which is the one that knows the mode.
//
// ⚠ A suspended tenant's sessions and keys used to keep working: suspension was
// a sign-in gate only ("a disabled provider's users cannot log in"), so an
// account that was signed in, and every key it held, went on until the session
// ran out or the key was revoked.
func AccountBlock(ctx context.Context, store db.Store, u *model.User) string {
	if u == nil {
		return ""
	}
	if !u.Enabled {
		return SSOReasonAccountDisabled
	}
	if p := tenantOf(ctx, store, u); p != nil && !p.Enabled {
		return SSOReasonTenantSuspended
	}
	return ""
}

// tenantOf is the tenant u belongs to when it is one a gate may hold: a
// provider that can be read and is not the platform's own. nil otherwise - no
// provider, an unreadable one, the supertenant - which is how the gates fail
// toward availability.
func tenantOf(ctx context.Context, store db.Store, u *model.User) *model.Provider {
	if store == nil || u == nil || u.ProviderID == nil {
		return nil
	}
	p, err := store.GetProvider(ctx, *u.ProviderID)
	if err != nil || p == nil || p.IsSupertenant {
		return nil
	}
	return p
}

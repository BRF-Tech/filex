package oidc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ── Which account an SSO sign-in opens (docs/SSO.md) ────────────────────────
//
// Until 0.50 the answer was "the account with the e-mail address the identity
// provider sent": looked up across the WHOLE platform when the driver was not
// pinned to a tenant, `email_verified` never read, and the `sub` it kept never
// compared. An identity provider bound to one tenant could therefore sign a
// person in to another tenant's account (a platform administrator's among
// them) by sending that address. The rules now, in order:
//
//  1. Every lookup stays inside the tenant the sign-in is for (Tenant: the
//     flow's, else the driver's own) - db.UserTenant; there is no unscoped
//     lookup on this path, and Store.GetUserByEmail is never called.
//  2. An account bound to the identity (issuer, sub) is that identity's: it
//     signs in, whatever address the provider sends now and whether or not
//     the provider says it is verified.
//  3. Otherwise the account of the tenant with the address:
//     - bound to ANOTHER identity: refused, with a reason the page shows
//     (identity_mismatch); an administrator can remove the bind;
//     - holding only a subject from before 0.50 (no issuer): the same subject
//     binds it (its issuer is filled in), another one is refused;
//     - bound to nobody: only an address the provider marks
//     `email_verified: true` (or a provider the operator trusts,
//     `trust_email`) binds it and signs in; otherwise refused, with
//     a reason the page shows (email_unverified).
//  4. No account: the first-login rule (auth.ProvisionFirstLogin). The new
//     account is bound at once; when the address is not verified it is opened
//     SWITCHED OFF, waiting for an administrator (account_pending).
//  5. Whatever the steps returned, an account of another tenant is refused
//     (Tenant.owns), with no reason code.

// identity is who the identity provider says signed in.
type identity struct {
	issuer, subject string
	email           string
	// verified: the provider marked the address `email_verified: true`, or
	// the operator trusts every address it sends.
	verified bool
}

// emailVerified reads the `email_verified` claim: only true is true (a JSON
// boolean, or the string "true" some providers send). Absent, false or
// anything else is "not verified".
func emailVerified(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	}
	return false
}

// scope is the tenant of one sign-in, with the platform's own resolved to its
// row.
type scope struct {
	db.UserTenant
}

// scopeOf resolves the tenant of a sign-in: the platform's own one is read
// from the store (the supertenant row; 0 when there is none, and then only an
// account with no tenant is its).
func (d *Driver) scopeOf(ctx context.Context, t Tenant) (scope, error) {
	out := scope{db.UserTenant{ProviderID: t.ProviderID, Main: t.Main}}
	if t.Main && t.ProviderID == 0 {
		st, err := d.store.GetSupertenant(ctx)
		if err != nil {
			return out, fmt.Errorf("oidc: the platform's own tenant could not be read: %w", err)
		}
		if st != nil {
			out.ProviderID = st.ID
		}
	}
	return out, nil
}

// owns reports whether the account belongs to the tenant (the rule of
// identity.Tenant.Owns).
func (s scope) owns(u *model.User) bool {
	if u == nil {
		return false
	}
	if u.ProviderID == nil || *u.ProviderID == 0 {
		return s.Main
	}
	return s.ProviderID != 0 && *u.ProviderID == s.ProviderID
}

// errIdentityMismatch: the address has an account in this tenant that is bound
// to another SSO identity. The page says so (identity_mismatch): the account
// is in the tenant the sign-in is for, so nothing about another tenant is
// told.
var errIdentityMismatch = auth.SSORefused(auth.SSOReasonIdentityMismatch,
	errors.New("oidc: this address belongs to an account bound to another SSO identity; an administrator can remove the account's SSO bind"))

// account finds or opens the account of a sign-in (the rules above) and
// reports whether it was opened now.
func (d *Driver) account(ctx context.Context, sc scope, id identity, first auth.FirstLogin) (*model.User, bool, error) {
	if u, err := d.store.GetUserByOIDCIdentity(ctx, sc.UserTenant, id.issuer, id.subject); err != nil {
		return nil, false, fmt.Errorf("oidc: lookup by SSO identity: %w", err)
	} else if u != nil {
		return u, false, nil
	}
	u, err := d.store.GetUserInTenantByEmail(ctx, sc.UserTenant, id.email)
	if err != nil {
		return nil, false, fmt.Errorf("oidc: lookup user: %w", err)
	}
	if u != nil {
		return d.bindExisting(ctx, u, id)
	}
	u, err = auth.ProvisionFirstLogin(ctx, d.store, first)
	if err != nil {
		if errors.Is(err, auth.ErrFirstLoginRefused) {
			return nil, false, err
		}
		// ⚠⚠ No reason code: the page answers exactly as for any failure,
		// so an SSO identity cannot learn that its address has an account in
		// another tenant (the unique e-mail refuses the new row). The log
		// says it.
		return nil, false, fmt.Errorf("oidc: this email is registered to another tenant: %w", err)
	}
	if err := d.store.SetUserOIDCIdentity(ctx, u.ID, id.issuer, id.subject); err != nil {
		return nil, false, fmt.Errorf("oidc: bind the new account %d to its SSO identity: %w", u.ID, err)
	}
	u.OIDCIssuer, u.OIDCSubject = id.issuer, id.subject
	return u, true, nil
}

// bindExisting decides whether an existing account of the tenant that was
// found by its address is this identity's, and binds it when it is.
func (d *Driver) bindExisting(ctx context.Context, u *model.User, id identity) (*model.User, bool, error) {
	switch {
	case u.OIDCIssuer != "":
		// Bound, and not to this identity (the lookup by identity missed).
		slog.Warn("oidc: sign-in refused: the address belongs to an account bound to another SSO identity",
			slog.Int64("user_id", u.ID), slog.String("issuer", id.issuer), slog.String("bound_issuer", u.OIDCIssuer))
		return nil, false, errIdentityMismatch
	case u.OIDCSubject != "":
		// A subject kept before 0.50, with no issuer: the same subject is the
		// same person (it completes the bind), another one is not.
		if u.OIDCSubject != id.subject {
			slog.Warn("oidc: sign-in refused: the address belongs to an account bound to another SSO subject",
				slog.Int64("user_id", u.ID), slog.String("issuer", id.issuer))
			return nil, false, errIdentityMismatch
		}
	case !id.verified:
		return nil, false, auth.SSORefused(auth.SSOReasonEmailUnverified, fmt.Errorf(
			"oidc: the identity provider did not say %s is verified, and account %d is bound to no SSO identity yet: it is not signed in to by its address alone (turn on %q for a provider whose addresses are all its people's own)",
			id.email, u.ID, TrustEmailKey))
	}
	if err := d.store.SetUserOIDCIdentity(ctx, u.ID, id.issuer, id.subject); err != nil {
		return nil, false, fmt.Errorf("oidc: bind account %d to its SSO identity: %w", u.ID, err)
	}
	u.OIDCIssuer, u.OIDCSubject = id.issuer, id.subject
	return u, false, nil
}

// AuditActionAccountPending is the audit action of an account an SSO
// sign-in opened switched off, because the identity provider did not say its
// address is verified.
const AuditActionAccountPending = "auth.account_pending"

// holdForApproval switches a just-opened account off until an administrator
// switches it on (model.DisabledPendingApproval), records why (log and audit
// row) and answers the sign-in with the reason the page shows. The account
// keeps the groups the sign-in carried, so an approved account has them.
func (d *Driver) holdForApproval(ctx context.Context, u *model.User, groups []string) error {
	if err := d.store.SetUserDisabledReason(ctx, u.ID, model.DisabledPendingApproval); err != nil {
		// Not switched off: delete it rather than leave an account its
		// address was never confirmed for.
		if derr := d.store.DeleteUser(ctx, u.ID); derr != nil {
			slog.Error("oidc: an account opened for an unverified address could not be switched off NOR deleted",
				slog.Int64("user_id", u.ID), slog.String("err", err.Error()), slog.String("delete_err", derr.Error()))
		}
		return fmt.Errorf("oidc: switch off the account opened for an unverified address: %w", err)
	}
	u.Enabled, u.DisabledReason = false, model.DisabledPendingApproval
	auth.RecordSignInGroups(ctx, d.store, "oidc", u, groups, true)
	slog.Warn("oidc: an account was opened switched off: the identity provider did not say its address is verified; an administrator switches it on to approve it",
		slog.Int64("user_id", u.ID), slog.String("email", u.Email))
	auth.AddAuditDetail(ctx, "account_pending", u.Email)
	var pid any
	if u.ProviderID != nil {
		pid = *u.ProviderID
	}
	if err := d.store.InsertAuditEntry(ctx, &model.AuditEntry{
		Action: AuditActionAccountPending, TargetType: "user", TargetID: fmt.Sprint(u.ID),
		Metadata: map[string]any{"provider": "oidc", "email": u.Email, "tenant": pid, "reason": "email_unverified"},
	}); err != nil {
		slog.Warn("oidc: could not write the audit row of an account opened switched off", slog.String("err", err.Error()))
	}
	return auth.SSORefused(auth.SSOReasonAccountPending, fmt.Errorf(
		"oidc: account %d (%s) was opened switched off, waiting for an administrator: the identity provider did not say the address is verified",
		u.ID, u.Email))
}

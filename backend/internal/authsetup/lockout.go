package authsetup

import (
	"context"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
)

// AdminPaths lists the ways an administrator can still sign in with the
// running set, leaving out one provider (the one about to be switched off,
// named by its slug: the driver's name for its first instance).
//
//   - "password": password sign-in is on (`local`) and at least one enabled
//     administrator has a password;
//   - "recovery": the bootstrap administrator's recovery sign-in is on, and
//     that account exists, is enabled and has a password;
//   - the slug of every other running identity provider or directory.
//
// ⚠ An identity provider counts as a way in although filex cannot prove an
// administrator's account is on the other side of it: the rule this serves is
// "never switch off the LAST way in", and a provider that is running is a way
// in until shown otherwise. The two password paths are checked for real,
// because an instance whose administrators all came in through SSO has a
// password form nobody can use.
//
// On a multi-tenant install this is the platform's own tenant's answer: the
// providers bound to it (AdminPathsIn).
func (l *Live) AdminPaths(ctx context.Context, without string) []string {
	without = Canonical(without)
	s := l.cur.Load()
	if s.multiTenant && s.binds != nil {
		return l.AdminPathsIn(ctx, s.binds.Main, func(e Entry) bool { return e.Slug == without })
	}
	return l.adminPaths(ctx, s, 0, func(e Entry) bool { return e.Slug == without })
}

// AdminPathsIn is AdminPaths for one tenant (docs/TENANT-ADMIN.md, "the last
// way in"): the providers bound to it that exclude does not leave out, and the
// password form for an enabled administrator OF THAT TENANT who has a
// password. Recovery is the platform's own tenant's only.
func (l *Live) AdminPathsIn(ctx context.Context, tenantID int64, exclude func(Entry) bool) []string {
	return l.adminPaths(ctx, l.cur.Load(), tenantID, exclude)
}

func (l *Live) adminPaths(ctx context.Context, s *Set, tenantID int64, exclude func(Entry) bool) []string {
	var out []string
	store := l.opts.Store
	scoped := s.multiTenant && s.binds != nil
	main := !scoped || tenantID == s.binds.Main

	hasLocal := false
	for _, e := range s.Entries {
		if e.Driver == nil || (exclude != nil && exclude(e)) {
			continue
		}
		if scoped && (!s.binds.Bound(e.InstanceID, tenantID) || (e.Owner != 0 && e.Owner != tenantID)) {
			continue
		}
		switch e.Name {
		case "local":
			hasLocal = true
		// The operating-system sign-in providers (windows, pam): the test account
		// that switched one on is an administrator.
		case "oidc", "ldap", "proxy-header", "windows", "pam":
			out = appendOnce(out, e.Slug)
		}
	}
	if scoped && !main {
		// A tenant's own OIDC on its provider row is a way in for it too.
		if p, err := store.GetProvider(ctx, tenantID); err == nil && p != nil && p.AuthType == model.AuthTypeOIDC && p.OIDCIssuer != "" && p.OIDCClientID != "" {
			out = appendOnce(out, OwnSSOKey)
		}
	}
	if hasLocal {
		if users, err := store.ListUsers(ctx); err == nil {
			for _, u := range users {
				if u.Role != model.RoleAdmin || !u.Enabled || u.PasswordHash == "" {
					continue
				}
				if scoped && !inTenant(u, tenantID, main) {
					continue
				}
				out = append([]string{"password"}, out...)
				break
			}
		}
	}
	if s.recovery && main {
		if id, err := authlocal.BootstrapAdminID(ctx, store); err == nil && id > 0 {
			if u, err := store.GetUser(ctx, id); err == nil && u != nil && u.Enabled && u.PasswordHash != "" {
				out = append([]string{"recovery"}, out...)
			}
		}
	}
	return out
}

// inTenant: the account is the tenant's (an account with no tenant is the
// platform's own).
func inTenant(u *model.User, tenantID int64, main bool) bool {
	if u.ProviderID == nil {
		return main
	}
	return *u.ProviderID == tenantID
}

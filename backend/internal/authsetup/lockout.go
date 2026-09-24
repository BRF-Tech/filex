package authsetup

import (
	"context"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
)

// AdminPaths lists the ways an administrator can still sign in with the
// running set, leaving out one provider (the one about to be switched off).
//
//   - "password": password sign-in is on (`local`) and at least one enabled
//     administrator has a password;
//   - "recovery": the bootstrap administrator's recovery sign-in is on, and
//     that account exists, is enabled and has a password;
//   - the name of every other running identity provider or directory.
//
// ⚠ An identity provider counts as a way in although filex cannot prove an
// administrator's account is on the other side of it: the rule this serves is
// "never switch off the LAST way in", and a provider that is running is a way
// in until shown otherwise. The two password paths are checked for real,
// because an instance whose administrators all came in through SSO has a
// password form nobody can use.
func (l *Live) AdminPaths(ctx context.Context, without string) []string {
	without = Canonical(without)
	s := l.cur.Load()
	var out []string
	store := l.opts.Store

	hasLocal := false
	for _, e := range s.Entries {
		if e.Driver == nil || e.Name == without {
			continue
		}
		switch e.Name {
		case "local":
			hasLocal = true
		case "oidc", "ldap", "proxy-header":
			out = append(out, e.Name)
		}
	}
	if hasLocal {
		if users, err := store.ListUsers(ctx); err == nil {
			for _, u := range users {
				if u.Role == model.RoleAdmin && u.Enabled && u.PasswordHash != "" {
					out = append([]string{"password"}, out...)
					break
				}
			}
		}
	}
	if s.recovery {
		if id, err := authlocal.BootstrapAdminID(ctx, store); err == nil && id > 0 {
			if u, err := store.GetUser(ctx, id); err == nil && u != nil && u.Enabled && u.PasswordHash != "" {
				out = append([]string{"recovery"}, out...)
			}
		}
	}
	return out
}

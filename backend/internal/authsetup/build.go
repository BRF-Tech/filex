// Package authsetup builds the instance's sign-in providers and keeps the live
// set of them — the ONE place a provider is constructed, whether the
// environment/config file or the Admin → Identity providers page asked for it.
//
// # Why this package exists
//
// Until v0.43.0 the admin page wrote `auth.<name>.*` settings rows that
// nothing read: the server built its drivers from FILEX_AUTH_DRIVERS and the
// config file only, once, at boot (release-candidate sweep, 2026-09-21). The
// page looked like it changed who could sign in and changed nothing — the
// worst kind of screen. The owner's decision: the page really manages sign-in,
// applied without a restart, and it can never lock the instance out.
//
// # The rules, in one place
//
//   - One construction path: Build. server.go calls it for every provider the
//     environment lists; Live calls it for every provider the page enabled.
//   - The environment wins, visibly. A provider the environment lists is built
//     from the environment only; rows the page may hold under the same name are
//     kept but not used, and the page shows the provider read-only with where
//     it comes from (Live.EnvDefines, FromWords).
//   - Page providers are ADDED after the environment's, never in front of them:
//     `local` stays the first password judge wherever the operator put it, so a
//     directory that hangs never sits in front of the administrator's password.
//   - Password sign-in (`local`) and the recovery sign-in (the bootstrap
//     administrator's break-glass, FILEX_AUTH_RECOVERY_LOGIN) are the
//     environment's to decide and nobody else's. The page cannot add or remove
//     either — see docs/SSO.md, "Why nothing on this page can lock you out".
//   - A page provider that cannot start is left out and says why; it never
//     takes anything else down with it (assemble).
package authsetup

import (
	"context"
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	authproxyheader "github.com/brf-tech/filex/backend/internal/auth/drivers/proxyheader"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// Where a provider's configuration comes from.
const (
	OriginEnvironment = "environment"
	OriginPage        = "page"
)

// Canonical returns the name a provider is known by. The environment has
// always accepted a few spellings of the header driver; the page and the
// wire use one.
func Canonical(name string) string {
	n := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
	switch n {
	case "proxyheader", "header-proxy":
		return "proxy-header"
	}
	return n
}

// FromWords says where an environment provider is defined, in the reader's
// language: config.Auth.DriversFrom is "FILEX_AUTH_DRIVERS", "file:<path>"
// or "default"; anything else is shown as it is.
//
// The words are the server catalogue's (`server.auth_provider.from_*`), so a
// language pack's language reads them too — not the English half of a
// hard-coded en/tr pair.
func FromWords(from, lang string) string {
	switch {
	case from == "default":
		return srvtext.Text(lang, "server.auth_provider.from_default", nil)
	case strings.HasPrefix(from, "file:"):
		return srvtext.Text(lang, "server.auth_provider.from_file", srvtext.Vars{"path": strings.TrimPrefix(from, "file:")})
	}
	return from
}

// Build constructs and initialises ONE sign-in provider from a configuration
// map (the keys the driver's Init reads).
//
// ⚠⚠ The single construction path. The environment/config file (server.go)
// and the admin page (Live.Reload) both come here, so a provider configured
// on the page is exactly the driver the same values in the environment would
// have produced — no second, subtly different path. A caller that needs a
// retry (the environment's OIDC, whose IdP often boots alongside filex) wraps
// this call; it does not grow its own construction.
func Build(ctx context.Context, store db.Store, name string, cfg map[string]any) (auth.Driver, error) {
	switch Canonical(name) {
	case "local":
		d := authlocal.New(store)
		if err := d.Init(ctx, nil); err != nil {
			return nil, err
		}
		return d, nil
	case "oidc":
		d := authoidc.New(store)
		if err := d.Init(ctx, cfg); err != nil {
			return nil, err
		}
		return d, nil
	case "ldap":
		d := authldap.New(store)
		if err := d.Init(ctx, cfg); err != nil {
			return nil, err
		}
		return d, nil
	case "proxy-header":
		d := authproxyheader.New(store)
		if err := d.Init(ctx, cfg); err != nil {
			return nil, err
		}
		return d, nil
	}
	return nil, fmt.Errorf("unknown sign-in provider %q", name)
}

// WithRecoveryLogin appends the recovery sign-in driver when no enabled login
// driver is `local` and recovery is on, and reports whether it did.
//
// The owner's ruling, 2026-09-14: on an installation that signs in through an
// identity provider alone, the administrator created at installation must
// still be able to sign in with a password — for recovery only. Without it an
// unreachable IdP, an expired client secret or a broken realm locks out the
// one person who can repair filex's side of it. local.RecoveryLogin accepts
// that account and no other, so password sign-in stays off for everyone else.
//
// ⚠ It is switched off by the environment only (FILEX_AUTH_RECOVERY_LOGIN):
// the admin page, which can break the identity provider it would recover
// from, can never switch off the way back.
//
// It goes LAST: a directory driver in the list judges its own accounts first.
func WithRecoveryLogin(loginDrvs []auth.LoginDriver, enabled bool, store db.Store) ([]auth.LoginDriver, bool) {
	if !enabled {
		return loginDrvs, false
	}
	for _, d := range loginDrvs {
		if n, ok := d.(interface{ Name() string }); ok && (n.Name() == "local" || n.Name() == authlocal.RecoveryLoginName) {
			return loginDrvs, false
		}
	}
	return append(loginDrvs, authlocal.NewRecoveryLogin(store)), true
}

// WithSessionAuthenticator makes sure something in the chain can turn a filex
// session back into a user, whichever login drivers are enabled.
//
// ⚠⚠ Issue #24. Every sign-in — a password, an LDAP bind, an OIDC callback —
// ends in the same sessions row and the same `filex_session` cookie, but only
// the `local` driver's Authenticate reads that row: OIDC's and LDAP's answer
// "unauthorized" by design. With `FILEX_AUTH_DRIVERS=oidc` the callback minted
// a session the very next request refused, so nobody could sign in and every
// open session died on the restart that applied the setting.
//
// The validator is not a LoginDriver, so it enables no password sign-in; and
// when `local` is already in the list it adds nothing, because that driver
// does the same lookup.
func WithSessionAuthenticator(enabled []auth.Driver, store db.Store) []auth.Driver {
	for _, d := range enabled {
		if d.Name() == "local" || d.Name() == authlocal.SessionAuthenticatorName {
			return enabled
		}
	}
	return append(enabled, authlocal.NewSessionAuthenticator(store))
}

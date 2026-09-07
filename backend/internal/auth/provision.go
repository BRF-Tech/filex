package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
)

// ── Where a just-in-time account is homed ───────────────────────────────────
//
// db.Store.CreateUser hard-codes `provider_id` to the `default` provider, and
// `default` is seeded `is_supertenant = 1`. A supertenant scope is
// confine-EXEMPT — tenant.Scope.CanAccessStorage returns true for every storage
// on the box — so any code path that creates a user and does not immediately
// re-home it mints a cross-tenant account.
//
// The admin API and the invite path were fixed by inheriting the CALLER's
// tenant. A login has no caller to inherit from: the account being created is
// the caller. What it does have is the HOST the browser asked for, which is the
// same signal multioidc.Dispatcher already uses to pick a realm — so that is
// what homes the account here.
//
// ⚠ On a single-tenant install (the default, and the vast majority) none of
// this runs: MultiTenant is false, the provider is left alone, and a directory
// login behaves exactly as it did.

// ErrNoTenantForLogin is returned when a multi-tenant install cannot tell which
// tenant a just-in-time account belongs to.
//
// ⚠ It is deliberately a REFUSAL and not a fallback. The only fallback
// available is `default`, and homing a directory user there is the bug: it
// hands a confine-exempt, every-storage account to whoever the directory says
// exists. Refusing costs an operator one config line (`provider:`) or one DNS
// host; the fallback costs them the tenant boundary.
var ErrNoTenantForLogin = errors.New("auth: cannot determine which tenant this login belongs to")

// ProvisionStore is the slice of db.Store just-in-time provisioning needs.
type ProvisionStore interface {
	CreateUser(ctx context.Context, email, passwordHash, role, locale, tz string) (*model.User, error)
	DeleteUser(ctx context.Context, id int64) error
	GetUser(ctx context.Context, id int64) (*model.User, error)
	SetUserProvider(ctx context.Context, userID, providerID int64, oidcSubject string) error
	GetProviderByHost(ctx context.Context, host string) (*model.Provider, error)
	GetProviderBySlug(ctx context.Context, slug string) (*model.Provider, error)
}

// TenantHoming is a driver's policy for where its JIT accounts land.
type TenantHoming struct {
	// MultiTenant mirrors cfg.MultiTenant. False = do nothing at all.
	MultiTenant bool
	// Pin is an operator-declared provider SLUG, used when the login carries no
	// host that maps to a tenant.
	//
	// ⚠ It exists for the logins that structurally have no Host: SFTP, FTPS and
	// NFS present a password on a socket, not an HTTP request. Without it, a
	// multi-tenant install simply cannot provision a directory user over those
	// protocols — which is a defensible default (the account still works the
	// moment it has logged in through the web once) but a bad surprise, so the
	// operator gets a way to say it out loud.
	Pin string
}

type loginHostKey struct{}

// WithLoginHost stamps the Host of the HTTP request that started a login onto
// the context.
//
// ⚠ It exists because auth.LoginDriver.Login takes only a ctx — no
// *http.Request — so a driver in the login chain cannot see the host by itself.
// Widening the interface would touch every driver and every protocol caller for
// the benefit of one; the context carries it instead, and a login that arrives
// without one (a protocol login) simply has no host, which is the truth.
func WithLoginHost(ctx context.Context, host string) context.Context {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ctx
	}
	return context.WithValue(ctx, loginHostKey{}, host)
}

// WithRequestLoginHost is WithLoginHost for an *http.Request.
func WithRequestLoginHost(ctx context.Context, r *http.Request) context.Context {
	return WithLoginHost(ctx, tenanturl.RequestHost(r))
}

// LoginHostFrom returns the stamped host, or "".
func LoginHostFrom(ctx context.Context) string {
	h, _ := ctx.Value(loginHostKey{}).(string)
	return h
}

// ProvisionUser creates `email` and homes it in the tenant this login arrived
// for. `driver` names the caller, for the log line an operator will need when a
// directory user cannot sign in.
//
// The three beats are the ones the invite path established: create; home; and
// on a failure to home, DELETE the row rather than leave a half-created
// supertenant account behind.
func ProvisionUser(ctx context.Context, store ProvisionStore, h TenantHoming, driver, email, role string) (*model.User, error) {
	var providerID int64
	if h.MultiTenant {
		p, err := h.resolveProvider(ctx, store)
		if err != nil {
			slog.Warn(driver+": refusing to provision a directory account with no tenant",
				slog.String("email", email),
				slog.String("host", LoginHostFrom(ctx)),
				slog.String("hint", "give the tenant a `host` that matches the login URL, or pin one with the driver's `provider` setting"))
			return nil, err
		}
		providerID = p.ID
	}

	u, err := store.CreateUser(ctx, email, "", role, "en", "UTC")
	if err != nil {
		return nil, err
	}
	if providerID == 0 {
		return u, nil
	}
	// ⚠⚠ Empty oidc_subject: SetUserProvider overwrites the column
	// unconditionally, and a directory account has no OIDC subject to keep.
	if err := store.SetUserProvider(ctx, u.ID, providerID, ""); err != nil {
		if derr := store.DeleteUser(ctx, u.ID); derr != nil {
			// Naming the row is the only way an operator can find and remove a
			// stranded supertenant account.
			slog.Error(driver+": could not home a new account in its tenant AND could not delete it; it is stranded in the supertenant",
				slog.Int64("user_id", u.ID), slog.String("email", email),
				slog.String("home_err", err.Error()), slog.String("delete_err", derr.Error()))
		}
		return nil, fmt.Errorf("%s: could not home the new account in its tenant: %w", driver, err)
	}
	if fresh, gerr := store.GetUser(ctx, u.ID); gerr == nil && fresh != nil {
		u = fresh
	}
	slog.Info(driver+": provisioned a directory account",
		slog.String("email", email), slog.Int64("provider_id", providerID))
	return u, nil
}

// resolveProvider picks the tenant: the login's host first, then the pin.
func (h TenantHoming) resolveProvider(ctx context.Context, store ProvisionStore) (*model.Provider, error) {
	if host := LoginHostFrom(ctx); host != "" {
		// GetProviderByHost already filters on enabled=1, so a suspended tenant
		// cannot gain members.
		if p, err := store.GetProviderByHost(ctx, host); err == nil && p != nil {
			return p, nil
		}
	}
	if pin := strings.TrimSpace(h.Pin); pin != "" {
		p, err := store.GetProviderBySlug(ctx, pin)
		if err != nil {
			return nil, fmt.Errorf("%w: pinned provider %q: %v", ErrNoTenantForLogin, pin, err)
		}
		if p == nil {
			return nil, fmt.Errorf("%w: pinned provider %q does not exist", ErrNoTenantForLogin, pin)
		}
		return p, nil
	}
	return nil, ErrNoTenantForLogin
}

package auth

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/identity"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ── Which tenant a sign-in is for ───────────────────────────────────────────
//
// On a multi-tenant install two tenants may each have a person called `alex`,
// so a sign-in has to say WHICH tenant it is for before an account can be
// looked up. Two things can say it (docs/MULTI-TENANCY.md, Realms):
//
//   - the ADDRESS the client reached — the web page's Host, WebDAV's Host
//     header, the name an FTPS client sent in TLS SNI — when it is a tenant's
//     own host;
//   - the REALM the person typed — the sign-in form's Realm field, or
//     `realm/name` in a protocol user name (SFTP carries no address at all).
//
// Neither → the platform's own tenant (the supertenant): an empty realm is how
// the platform operator signs in. Both, naming different tenants → refused.
// A realm nobody has → refused. Either refusal is the ONE answer a wrong
// password gets: nobody learns from the sign-in form which realms exist.
//
// ⚠ The account lookup then never leaves the tenant (identity.ResolveIn), and
// whatever a provider returns is checked again (Admits): an account of another
// tenant is never signed in to, however the realm and the address are played
// against each other.
//
// On a single-tenant install none of this exists: nothing resolves a
// LoginRealm, and every lookup is identity.Resolve as it always was.

// ErrUnknownRealm: the realm typed is no tenant's.
var ErrUnknownRealm = errors.New("auth: no tenant has this realm")

// ErrRealmConflict: the realm typed names another tenant than the address the
// client reached.
var ErrRealmConflict = errors.New("auth: the realm names another tenant than the address")

// RealmStore is the slice of db.Store realm resolution needs.
type RealmStore interface {
	GetProviderByRealm(ctx context.Context, realm string) (*model.Provider, error)
	GetProviderByHost(ctx context.Context, host string) (*model.Provider, error)
	GetSupertenant(ctx context.Context) (*model.Provider, error)
}

// LoginRealm is the tenant one sign-in is for.
type LoginRealm struct {
	// Tenant is the tenant. The supertenant for the platform's own realm; nil
	// only on a platform with no supertenant row (then only accounts with no
	// tenant belong to the main realm).
	Tenant *model.Provider
	// Named: a realm was typed, or the address named the tenant. False: the
	// platform's own realm because nothing named another.
	Named bool
	// Typed is the realm as typed, normalised ("" when none) — kept for the
	// sign-in limit's counter even when it names nobody.
	Typed string
	// Token is the installation's e-mail token, for derived addresses.
	Token string

	// pinned is a tenant a directory provider's `provider` pin homed this
	// (un-named) sign-in in — see NotePinned.
	pinned atomic.Int64
}

// ResolveLoginRealm decides which tenant a sign-in is for. typed is the realm
// the person wrote and named whether they wrote one at all (`/alex` names the
// platform's own realm outright); host is the address the client reached (""
// for none).
//
// The returned LoginRealm is non-nil even with an error, so the caller can
// still count the attempt under the realm that was typed.
func ResolveLoginRealm(ctx context.Context, store RealmStore, typed string, named bool, host, token string) (*LoginRealm, error) {
	typed = tenant.NormalizeRealm(typed)
	lr := &LoginRealm{Typed: typed, Token: token}
	var byHost *model.Provider
	if host != "" {
		// The platform's own host (the supertenant's) names nobody: it is
		// where a person types the realm they want.
		if p, err := store.GetProviderByHost(ctx, host); err == nil && p != nil && !p.IsSupertenant {
			byHost = p
		}
	}
	switch {
	case named && typed != "":
		p, err := store.GetProviderByRealm(ctx, typed)
		if err != nil {
			return lr, err
		}
		if p == nil {
			return lr, ErrUnknownRealm
		}
		if byHost != nil && byHost.ID != p.ID {
			return lr, ErrRealmConflict
		}
		lr.Tenant, lr.Named = p, true
		return lr, nil
	case named:
		// The platform's own realm said outright, at a tenant's address.
		if byHost != nil {
			return lr, ErrRealmConflict
		}
		lr.Named = true
	case byHost != nil:
		lr.Tenant, lr.Named = byHost, true
		return lr, nil
	}
	st, err := store.GetSupertenant(ctx)
	if err != nil {
		return lr, err
	}
	lr.Tenant = st
	return lr, nil
}

// main reports whether this is the platform's own realm.
func (lr *LoginRealm) main() bool {
	return lr.Tenant == nil || lr.Tenant.IsSupertenant
}

// DeriveRealm is the realm derived addresses are made in (identity.DeriveEmail):
// the tenant's, "" for the platform's own.
func (lr *LoginRealm) DeriveRealm() string {
	if lr == nil {
		return ""
	}
	return lr.Tenant.LoginRealm()
}

// CounterRealm is the realm the sign-in limit counts this attempt under
// (loginguard.Attempt.Realm): what was typed, else the tenant the address
// named, else none. `acme/alex`, `alex` at acme's address and `alex` with Realm
// "acme" are one counter; `beta/alex` and `alex` are others.
func (lr *LoginRealm) CounterRealm() string {
	switch {
	case lr == nil:
		return ""
	case lr.Typed != "":
		return lr.Typed
	case lr.Named:
		return lr.DeriveRealm()
	}
	return ""
}

// Identity is the lookup restriction identity.ResolveIn applies. nil for a nil
// LoginRealm (no tenants).
func (lr *LoginRealm) Identity() *identity.Tenant {
	if lr == nil {
		return nil
	}
	t := &identity.Tenant{Realm: lr.DeriveRealm(), Main: lr.main(), Token: lr.Token}
	if lr.Tenant != nil {
		t.ProviderID = lr.Tenant.ID
	}
	return t
}

// Admits reports whether an account may be signed in to through this realm:
// it belongs to the tenant — or, for the platform's own realm reached without
// naming it, to the tenant a directory provider's pin homed the sign-in in.
func (lr *LoginRealm) Admits(u *model.User) bool {
	if lr == nil {
		return u != nil
	}
	if lr.Identity().Owns(u) {
		return true
	}
	pin := lr.pinned.Load()
	return !lr.Named && pin != 0 && u != nil && u.ProviderID != nil && *u.ProviderID == pin
}

// AdmitsCredential is Admits for a credential that names its account by itself
// — an API token, a registered SSH key: no realm is needed, so only a realm
// that WAS named (typed, or by the address) has to be the account's.
func (lr *LoginRealm) AdmitsCredential(u *model.User) bool {
	if lr == nil || !lr.Named {
		return u != nil
	}
	return lr.Admits(u)
}

// NotePinned records that a directory provider homed this un-named sign-in in
// a tenant by its `provider` pin (TenantHoming.Pin). Called by the provider
// that signed the person in, after it has: the pin is the operator saying
// "realm-less sign-ins of this directory belong to that tenant", and Admits
// honours it for this sign-in only.
func (lr *LoginRealm) NotePinned(providerID int64) {
	if lr != nil && !lr.Named && providerID != 0 {
		lr.pinned.Store(providerID)
	}
}

// PinnedTenant is the tenant NotePinned recorded for this sign-in, 0 for none.
func (lr *LoginRealm) PinnedTenant() int64 {
	if lr == nil {
		return 0
	}
	return lr.pinned.Load()
}

type loginRealmKey struct{}

// WithLoginRealm stamps the sign-in's realm on the context, for the login
// chain's drivers (auth.LoginDriver.Login takes only a ctx — see WithLoginHost
// for why the context carries it). A nil realm leaves ctx as it is.
func WithLoginRealm(ctx context.Context, lr *LoginRealm) context.Context {
	if lr == nil {
		return ctx
	}
	return context.WithValue(ctx, loginRealmKey{}, lr)
}

// LoginRealmFrom returns the stamped realm, or nil (single-tenant, or a caller
// that is not a sign-in).
func LoginRealmFrom(ctx context.Context) *LoginRealm {
	lr, _ := ctx.Value(loginRealmKey{}).(*LoginRealm)
	return lr
}

// LoginRealmAdmits is Admits for the context's realm; true when there is none.
func LoginRealmAdmits(ctx context.Context, u *model.User) bool {
	lr := LoginRealmFrom(ctx)
	if lr == nil {
		return u != nil
	}
	return lr.Admits(u)
}

// ResolveAccount turns what a person typed into an account, inside the
// sign-in's realm when there is one (identity.ResolveIn) and exactly as
// identity.Resolve otherwise. Every password sign-in path uses it.
func ResolveAccount(ctx context.Context, l identity.Lookup, identifier string) (*model.User, error) {
	return identity.ResolveIn(ctx, l, LoginRealmFrom(ctx).Identity(), identifier)
}

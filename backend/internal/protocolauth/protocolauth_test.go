package protocolauth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const testPassword = "ProtocolPass!1"

func newStore(t *testing.T) db.Store {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	// The same wrapper production uses, so accounts here are named the way
	// accounts there are — otherwise "log in by username" would be untestable.
	return identitystore.New(raw)
}

func mkUser(t *testing.T, store db.Store, email string, role string) *model.User {
	t.Helper()
	hash, err := authlocal.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(context.Background(), email, hash, role, "en", "UTC")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func mkToken(t *testing.T, store db.Store, userID int64, label, plain, scopes string) *model.APIToken {
	t.Helper()
	tok, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: userID, Label: label, TokenHash: apitoken.HashToken(plain), Scopes: scopes,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return tok
}

func mkResolver(store db.Store, multiTenant bool) *protocolauth.Resolver {
	return protocolauth.New(store, acl.New(store), multiTenant)
}

func TestPasswordAcceptsEitherIdentifier(t *testing.T) {
	store := newStore(t)
	u := mkUser(t, store, "ada@example.com", model.RoleUser)
	r := mkResolver(store, false)

	for _, id := range []string{"ada@example.com", u.Username, "ADA@EXAMPLE.COM"} {
		p, err := r.Password(context.Background(), id, testPassword)
		if err != nil {
			t.Fatalf("Password(%q) = %v, want a principal", id, err)
		}
		if p.User.ID != u.ID {
			t.Errorf("Password(%q) resolved user %d, want %d", id, p.User.ID, u.ID)
		}
		if p.Token != nil {
			t.Errorf("password auth must not carry a token")
		}
	}

	if _, err := r.Password(context.Background(), "ada", "wrong"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("wrong password: %v, want ErrUnauthorized", err)
	}
	if _, err := r.Password(context.Background(), "nobody", testPassword); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("unknown identifier: %v, want ErrUnauthorized", err)
	}
}

// The failure this whole package exists to prevent: a caller authenticated
// outside the HTTP chain starting UNSCOPED, which tenant.FromContext reads as
// "see everything".
func TestMultiTenantPrincipalAlwaysCarriesAScope(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	p1, err := store.CreateProvider(ctx, &model.Provider{
		Slug: "diyetlif", Name: "diyetlif", AuthType: "local", Enabled: true,
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	// role=admin on purpose: olivov's tenant admins hold filex role admin, and
	// that is exactly what made the original leak reachable.
	u := mkUser(t, store, "berk@diyetlif.test", model.RoleAdmin)
	if err := store.SetUserProvider(ctx, u.ID, p1.ID, ""); err != nil {
		t.Fatalf("set provider: %v", err)
	}

	r := mkResolver(store, true)
	p, err := r.Password(ctx, "berk@diyetlif.test", testPassword)
	if err != nil {
		t.Fatalf("Password: %v", err)
	}
	if p.Scope == nil {
		t.Fatal("multi-tenant principal has a nil scope — an unscoped context means SEE EVERYTHING")
	}
	if p.Scope.ProviderID != p1.ID {
		t.Errorf("scope provider = %d, want %d", p.Scope.ProviderID, p1.ID)
	}
	if p.Scope.IsSupertenant {
		t.Error("a tenant admin must not be scoped as the supertenant")
	}

	// And it must reach the context, because that is where every downstream
	// reader looks.
	ctx2 := p.WithContext(ctx)
	got, ok := tenant.FromContext(ctx2)
	if !ok || got == nil || got.ProviderID != p1.ID {
		t.Errorf("WithContext did not stamp the scope: ok=%v scope=%v", ok, got)
	}
	if auth.UserFrom(ctx2) == nil {
		t.Error("WithContext did not stamp the user — writes would be ownerless and events actorless")
	}
}

// An account whose provider cannot be resolved must see NOTHING. This is the
// fail-closed half of the same rule.
func TestUnresolvableTenantGetsDenyAll(t *testing.T) {
	store := newStore(t)
	// No provider assigned at all is not reachable through CreateUser (it
	// defaults to the supertenant), so the honest way to exercise DenyAll is
	// the helper the resolver uses.
	u := mkUser(t, store, "orphan@nowhere.test", model.RoleAdmin)
	u.ProviderID = nil
	if scope := auth.ScopeForUser(context.Background(), store, u); scope != tenant.DenyAll {
		t.Fatalf("ScopeForUser(no provider) = %v, want DenyAll", scope)
	}
}

// None of these protocols can carry a second factor, so accepting the account
// password on one of them would make it a documented 2FA bypass.
func TestPasswordIsRefusedWhenTOTPIsOn(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "twofa@example.com", model.RoleUser)
	r := mkResolver(store, false)

	if _, err := r.Password(ctx, "twofa@example.com", testPassword); err != nil {
		t.Fatalf("precondition: password must work before TOTP is on: %v", err)
	}
	if err := store.SetTotpPendingSecret(ctx, u.ID, "SECRET", []string{"a", "b"}); err != nil {
		t.Fatalf("totp pending: %v", err)
	}
	if err := store.ActivateTotp(ctx, u.ID); err != nil {
		t.Fatalf("totp activate: %v", err)
	}

	// ⚠ Measured through the cache too: the first call above populated it, so
	// a cache that trusted its stored copy would keep letting this account in
	// for the whole TTL after 2FA was switched on.
	if _, err := r.Password(ctx, "twofa@example.com", testPassword); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Errorf("password accepted on a TOTP account: %v", err)
	}
}

func TestDisabledAccountIsRefused(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "gone@example.com", model.RoleUser)
	r := mkResolver(store, false)

	if _, err := r.Password(ctx, "gone@example.com", testPassword); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := store.SetUserEnabled(ctx, u.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := r.Password(ctx, "gone@example.com", testPassword); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Error("a disabled account still authenticated")
	}
}

func TestTokenMustBelongToTheNamedAccount(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	owner := mkUser(t, store, "owner@example.com", model.RoleUser)
	mkUser(t, store, "other@example.com", model.RoleUser)

	plain := "tok_" + "0123456789abcdef0123456789abcdef"
	mkToken(t, store, owner.ID, "test", plain, "")
	r := mkResolver(store, false)

	p, err := r.Token(ctx, "owner@example.com", plain)
	if err != nil {
		t.Fatalf("Token(owner e-mail): %v", err)
	}
	if p.Token == nil {
		t.Error("token auth must carry the token — verb scopes hang off it")
	}
	if _, err := r.Token(ctx, owner.Username, plain); err != nil {
		t.Errorf("Token(owner username): %v — dual-side login must reach here too", err)
	}
	if _, err := r.Token(ctx, "other@example.com", plain); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Error("a token was accepted against an account it does not belong to")
	}
	if _, err := r.Token(ctx, "", plain); err != nil {
		t.Errorf("Token with no identifier must still work for protocols that have no username field: %v", err)
	}
}

// A confined credential must be honoured or refused, never ignored: accepting
// one on a protocol that cannot enforce the restriction silently promotes a
// subtree-limited credential to whole-tree access.
func TestConfinedTokenIsRefusedOrCarried(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "confined@example.com", model.RoleUser)

	plain := "tok_" + "fedcba9876543210fedcba9876543210"
	mkToken(t, store, u.ID, "confined", plain, apitoken.ScopeRootPrefix+"main://projects/acme")

	refusing := mkResolver(store, false) // zero value == ConfineRefuse
	if _, err := refusing.Token(ctx, "confined@example.com", plain); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Error("a protocol that cannot enforce confinement accepted a confined token")
	}

	honoring := mkResolver(store, false)
	honoring.Confine = protocolauth.ConfineHonor
	p, err := honoring.Token(ctx, "confined@example.com", plain)
	if err != nil {
		t.Fatalf("ConfineHonor rejected a confined token: %v", err)
	}
	if p.Confine == nil {
		t.Fatal("ConfineHonor dropped the confinement — the protocol would enforce nothing")
	}
	if p.Confine.Adapter != "main" || p.Confine.Rel != "projects/acme" {
		t.Errorf("confinement = %+v, want main://projects/acme", *p.Confine)
	}
}

// The cache is a performance device, and its TTL is a security statement. What
// it must never do is answer for an account the identifier no longer names.
func TestCacheDoesNotOutliveTheIdentifier(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "renamed@example.com", model.RoleUser)
	r := mkResolver(store, false)

	if _, err := r.Password(ctx, u.Username, testPassword); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := store.SetUserUsername(ctx, u.ID, "brandnew"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := r.Password(ctx, u.Username, testPassword); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Error("the old username still authenticated from cache after a rename")
	}
	if _, err := r.Password(ctx, "brandnew", testPassword); err != nil {
		t.Errorf("the new username must authenticate: %v", err)
	}
}

func TestCacheCanBeDisabledAndCleared(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	mkUser(t, store, "nocache@example.com", model.RoleUser)

	r := mkResolver(store, false)
	r.CacheTTL = 0
	if _, err := r.Password(ctx, "nocache@example.com", testPassword); err != nil {
		t.Fatalf("with the cache off, verification must still work: %v", err)
	}

	r2 := mkResolver(store, false)
	r2.CacheTTL = time.Hour
	if _, err := r2.Password(ctx, "nocache@example.com", testPassword); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	r2.Forget() // revocation must not have to wait out the TTL
	if _, err := r2.Password(ctx, "nocache@example.com", testPassword); err != nil {
		t.Fatalf("after Forget the slow path must still succeed: %v", err)
	}
}

// Any() is the order the password-carrying protocols share, and the order is
// observable, so it is pinned.
func TestAnyTriesPasswordThenToken(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	u := mkUser(t, store, "both@example.com", model.RoleUser)
	plain := "tok_" + "11112222333344445555666677778888"
	mkToken(t, store, u.ID, "t", plain, "")
	r := mkResolver(store, false)

	p, err := r.Any(ctx, "both@example.com", testPassword)
	if err != nil || p.Token != nil {
		t.Errorf("the password must win as a password: err=%v token=%v", err, p.Token)
	}
	p, err = r.Any(ctx, "both@example.com", plain)
	if err != nil || p.Token == nil {
		t.Errorf("a token must be accepted as a token: err=%v", err)
	}
	if _, err := r.Any(ctx, "both@example.com", "neither"); !errors.Is(err, protocolauth.ErrUnauthorized) {
		t.Error("a secret that is neither must be refused")
	}
}

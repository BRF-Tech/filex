package ldap

// Where a just-in-time directory account lands.
//
// ⚠⚠ `db.Store.CreateUser` hard-codes `provider_id` to the `default` provider,
// and `default` is seeded `is_supertenant = 1` — which
// `tenant.Scope.CanAccessStorage` treats as confine-EXEMPT, returning true for
// every storage on the box. So on a multi-tenant install, every account this
// driver created just-in-time could reach every customer's files. The account
// was not "mis-filed"; it was privileged.
//
// The admin API and the invite path were fixed by inheriting the CALLER's
// tenant. A login has no caller to inherit from — the account being created IS
// the caller — so the signal here is the request Host, stamped onto the context
// by handlers.Auth.Login. It is the same signal multioidc.Dispatcher already
// uses to pick a realm.
//
// ⚠ A protocol login (SFTP / FTPS / NFS, via internal/protocolauth) has no Host
// at all. There the driver refuses to create rather than falling back to
// `default`, unless the operator has pinned a tenant with `provider`.

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// seedProvider creates a non-supertenant tenant reachable at `host`.
func seedProvider(t *testing.T, store db.Store, slug, host string) int64 {
	t.Helper()
	p, err := store.CreateProvider(context.Background(), &model.Provider{
		Slug: slug, Name: slug, Host: host, AuthType: "local", Enabled: true,
	})
	require.NoError(t, err)
	require.False(t, p.IsSupertenant)
	return p.ID
}

// supertenantID is what the pre-fix code homed every directory account in.
func supertenantID(t *testing.T, store db.Store) int64 {
	t.Helper()
	p, err := store.GetSupertenant(context.Background())
	require.NoError(t, err)
	require.NotNil(t, p, "migration 00014 seeds `default` as the supertenant")
	return p.ID
}

func ldapFixture(t *testing.T, cfg map[string]any) (*Driver, db.Store) {
	t.Helper()
	fc := &fakeConn{
		entries:      []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "directory-pw",
	}
	return newDriver(t, fc, cfg)
}

// TestLDAP_HostHomesTheAccountInItsTenant.
//
// Red proof on the unfixed build: the account came back with
// ProviderID == the supertenant, on the very request whose Host named a tenant.
func TestLDAP_HostHomesTheAccountInItsTenant(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{"multi_tenant": true})
	tenantID := seedProvider(t, store, "diyetlif", "diyetlif.example.com")

	ctx := auth.WithLoginHost(context.Background(), "diyetlif.example.com")
	u, tok, err := d.Login(ctx, "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	require.NotNil(t, u.ProviderID, "a directory account must be homed somewhere")
	assert.Equal(t, tenantID, *u.ProviderID,
		"the account must land in the tenant whose host the login arrived on")
	assert.NotEqual(t, supertenantID(t, store), *u.ProviderID,
		"the supertenant is confine-exempt; a directory user must never land there by accident")
}

// TestLDAP_NoHostRefusesToProvision — the protocol path.
//
// ⚠ The refusal is the point. The only fallback available is `default`, and
// `default` is the supertenant, so "provision anyway" is the bug rather than a
// convenience. The account still authenticates over every protocol once it
// exists; what it cannot do is come into existence with no tenant.
func TestLDAP_NoHostRefusesToProvision(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{"multi_tenant": true})
	seedProvider(t, store, "diyetlif", "diyetlif.example.com")

	// VerifyPassword is exactly what internal/protocolauth calls: no request,
	// no Host.
	u, err := d.VerifyPassword(context.Background(), "ayse@example.com", "directory-pw")
	require.Error(t, err)
	require.ErrorIs(t, err, auth.ErrNoTenantForLogin)
	require.Nil(t, u)

	// And nothing half-created: a refusal that still left a supertenant row
	// behind would be worse than the bug.
	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, users, "no account may exist after a refused provisioning")
}

// TestLDAP_UnknownHostRefusesToProvision — a Host that maps to no tenant is
// the same situation as no Host at all, and must not silently fall back.
func TestLDAP_UnknownHostRefusesToProvision(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{"multi_tenant": true})
	seedProvider(t, store, "diyetlif", "diyetlif.example.com")

	ctx := auth.WithLoginHost(context.Background(), "not-a-tenant.example.com")
	_, _, err := d.Login(ctx, "ayse@example.com", "directory-pw")
	require.ErrorIs(t, err, auth.ErrNoTenantForLogin)

	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, users)
}

// TestLDAP_PinnedProviderHomesAHostlessLogin — the operator's escape hatch for
// SFTP/FTPS/NFS, and the "per-driver default provider" the audit floated. It
// is opt-in and names a tenant explicitly, which is the difference between a
// declaration and an accident.
func TestLDAP_PinnedProviderHomesAHostlessLogin(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{
		"multi_tenant": true,
		"provider":     "diyetlif",
	})
	tenantID := seedProvider(t, store, "diyetlif", "diyetlif.example.com")

	u, err := d.VerifyPassword(context.Background(), "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, tenantID, *u.ProviderID)
}

// TestLDAP_HostWinsOverThePin — the pin is a fallback, not an override; a
// login that names a tenant must land in that one.
func TestLDAP_HostWinsOverThePin(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{
		"multi_tenant": true,
		"provider":     "arasboya",
	})
	mine := seedProvider(t, store, "diyetlif", "diyetlif.example.com")
	seedProvider(t, store, "arasboya", "arasboya.example.com")

	ctx := auth.WithLoginHost(context.Background(), "diyetlif.example.com")
	u, _, err := d.Login(ctx, "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, mine, *u.ProviderID)
}

// TestLDAP_SingleTenantUnaffected is the honest half: it passes on `main` too.
// With multi-tenancy off nothing about homing runs, and a directory login
// behaves exactly as it always did.
func TestLDAP_SingleTenantUnaffected(t *testing.T) {
	d, store := ldapFixture(t, nil) // multi_tenant absent → false

	u, tok, err := d.Login(context.Background(), "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	require.NotEmpty(t, tok, "the session must still be minted")
	assert.Equal(t, "ayse@example.com", u.Email)
	assert.NotEmpty(t, u.Username, "identitystore must still have named the account")

	// The install default, untouched — which on a single-tenant install is
	// simply "the only tenant there is".
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, supertenantID(t, store), *u.ProviderID)

	// And the protocol path keeps working with no Host, because there is
	// nothing to disambiguate.
	u2, err := d.VerifyPassword(context.Background(), "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	assert.Equal(t, u.ID, u2.ID)
}

// TestLDAP_ExistingAccountIsNeverRehomed — homing happens at CREATE. A user
// who already belongs to a tenant must not be moved by where they logged in,
// or a login on the wrong host would migrate somebody between customers.
func TestLDAP_ExistingAccountIsNeverRehomed(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{"multi_tenant": true})
	mine := seedProvider(t, store, "diyetlif", "diyetlif.example.com")
	theirs := seedProvider(t, store, "arasboya", "arasboya.example.com")

	ctx := auth.WithLoginHost(context.Background(), "diyetlif.example.com")
	u1, _, err := d.Login(ctx, "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	require.Equal(t, mine, *u1.ProviderID)

	other := auth.WithLoginHost(context.Background(), "arasboya.example.com")
	u2, _, err := d.Login(other, "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	assert.Equal(t, u1.ID, u2.ID, "the same directory identity is one account")
	require.NotNil(t, u2.ProviderID)
	assert.Equal(t, mine, *u2.ProviderID,
		"logging in on another tenant's host must not move the account")
	assert.NotEqual(t, theirs, *u2.ProviderID)
}

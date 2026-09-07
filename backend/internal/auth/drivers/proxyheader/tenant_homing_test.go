package proxyheader

// Where a just-in-time header-trust account lands.
//
// ⚠⚠ `db.Store.CreateUser` hard-codes `provider_id` to the `default` provider,
// and `default` is seeded `is_supertenant = 1`, which
// `tenant.Scope.CanAccessStorage` treats as confine-EXEMPT. So on a
// multi-tenant install, header auto-provisioning minted an account that could
// reach every customer's storages for anybody the upstream proxy named.
//
// Unlike LDAP this driver runs INSIDE an *http.Request, so the Host is always
// available and the homing signal is never missing in practice — which is why
// the "no host" case here is an unmapped host rather than an absent one.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// initDriverWithStore is initDriver, returning the store so the test can look
// at the row that was written.
//
// ⚠ identitystore, as internal/server.New wraps it before handing it to the
// driver. A fixture on the raw store creates accounts nobody named, and
// "unnamed" is exactly the account that cannot log in over SFTP or FTPS.
func initDriverWithStore(t *testing.T, cfg map[string]any) (*Driver, db.Store) {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	store := identitystore.New(raw)
	d := New(store)
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["trusted_proxies"]; !ok {
		cfg["trusted_proxies"] = []string{"127.0.0.0/8", "::1/128"}
	}
	require.NoError(t, d.Init(context.Background(), cfg))
	return d, store
}

func phSeedProvider(t *testing.T, store db.Store, slug, host string) int64 {
	t.Helper()
	p, err := store.CreateProvider(context.Background(), &model.Provider{
		Slug: slug, Name: slug, Host: host, AuthType: "local", Enabled: true,
	})
	require.NoError(t, err)
	return p.ID
}

func phSupertenant(t *testing.T, store db.Store) int64 {
	t.Helper()
	p, err := store.GetSupertenant(context.Background())
	require.NoError(t, err)
	require.NotNil(t, p)
	return p.ID
}

// phRequest is a request from a trusted proxy, for `host`, naming `user`.
func phRequest(host, user string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/files/manager", nil)
	r.RemoteAddr = "127.0.0.1:41000"
	r.Host = host
	r.Header.Set("X-Auth-User", user)
	r.Header.Set("X-Auth-Email", user)
	return r
}

// TestProxyHeader_HostHomesTheAccountInItsTenant.
//
// Red proof on the unfixed build: the provisioned user came back with
// ProviderID == the supertenant even though the request Host named a tenant.
func TestProxyHeader_HostHomesTheAccountInItsTenant(t *testing.T) {
	d, store := initDriverWithStore(t, map[string]any{"multi_tenant": true})
	tenantID := phSeedProvider(t, store, "diyetlif", "diyetlif.example.com")

	u, err := d.Authenticate(phRequest("diyetlif.example.com", "ayse@diyetlif.example.com"))
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, tenantID, *u.ProviderID)
	assert.NotEqual(t, phSupertenant(t, store), *u.ProviderID,
		"the supertenant is confine-exempt; header auth must never land a user there by accident")
}

// TestProxyHeader_UnmappedHostRefusesToProvision — no tenant, no account.
func TestProxyHeader_UnmappedHostRefusesToProvision(t *testing.T) {
	d, store := initDriverWithStore(t, map[string]any{"multi_tenant": true})
	phSeedProvider(t, store, "diyetlif", "diyetlif.example.com")

	u, err := d.Authenticate(phRequest("nowhere.example.com", "mallory@nowhere.example.com"))
	require.Error(t, err)
	require.ErrorIs(t, err, auth.ErrNoTenantForLogin)
	require.Nil(t, u)

	users, lerr := store.ListUsers(context.Background())
	require.NoError(t, lerr)
	assert.Empty(t, users, "a refused provisioning must leave no row behind")
}

// TestProxyHeader_PinnedProviderCoversAnUnmappedHost — the operator's
// declaration, for an install where the tenant hosts are not in filex's
// provider rows.
func TestProxyHeader_PinnedProviderCoversAnUnmappedHost(t *testing.T) {
	d, store := initDriverWithStore(t, map[string]any{
		"multi_tenant": true,
		"provider":     "diyetlif",
	})
	tenantID := phSeedProvider(t, store, "diyetlif", "diyetlif.example.com")

	u, err := d.Authenticate(phRequest("proxy.internal", "ayse@diyetlif.example.com"))
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, tenantID, *u.ProviderID)
}

// TestProxyHeader_SingleTenantUnaffected is the honest half: it passes on
// `main` too.
func TestProxyHeader_SingleTenantUnaffected(t *testing.T) {
	d, store := initDriverWithStore(t, nil) // multi_tenant absent → false

	u, err := d.Authenticate(phRequest("files.example.com", "ayse@example.com"))
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u.Email)
	assert.Equal(t, model.RoleUser, u.Role)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, phSupertenant(t, store), *u.ProviderID,
		"on a single-tenant install the only tenant there is stays the default")

	// A second request reuses the account rather than creating another.
	u2, err := d.Authenticate(phRequest("files.example.com", "ayse@example.com"))
	require.NoError(t, err)
	assert.Equal(t, u.ID, u2.ID)
}

// TestProxyHeader_ExistingAccountIsNeverRehomed — homing happens at CREATE.
func TestProxyHeader_ExistingAccountIsNeverRehomed(t *testing.T) {
	d, store := initDriverWithStore(t, map[string]any{"multi_tenant": true})
	mine := phSeedProvider(t, store, "diyetlif", "diyetlif.example.com")
	phSeedProvider(t, store, "arasboya", "arasboya.example.com")

	u1, err := d.Authenticate(phRequest("diyetlif.example.com", "ayse@diyetlif.example.com"))
	require.NoError(t, err)
	require.Equal(t, mine, *u1.ProviderID)

	u2, err := d.Authenticate(phRequest("arasboya.example.com", "ayse@diyetlif.example.com"))
	require.NoError(t, err)
	assert.Equal(t, u1.ID, u2.ID)
	require.NotNil(t, u2.ProviderID)
	assert.Equal(t, mine, *u2.ProviderID,
		"arriving on another tenant's host must not move an existing account")
}

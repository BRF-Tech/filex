package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// multiTenantServer is a test server with multi-tenant mode on, which is what
// makes auth.TenantResolver attach a scope to admin requests.
func multiTenantServer(t *testing.T) (*httptest.Server, *http.Client, db.Store) {
	t.Helper()
	return testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.MultiTenant = true
	})
}

// seedTenant creates an enabled provider plus an admin user homed in it, and
// returns (providerID, email, password). Providers are created disabled and
// auth.LoginAllowed refuses those, so Enabled matters.
func seedTenant(t *testing.T, store db.Store, slug, email string, supertenant bool) (int64, string, string) {
	t.Helper()
	ctx := context.Background()

	p, err := store.CreateProvider(ctx, &model.Provider{
		Slug: slug, Name: slug, AuthType: "local", IsSupertenant: supertenant, Enabled: true,
	})
	require.NoError(t, err)

	password := "TenantAdmin!1"
	hash, err := local.HashPassword(password)
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, email, hash, model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, p.ID, ""))

	return p.ID, email, password
}

func providerOf(t *testing.T, store db.Store, userID int64) int64 {
	t.Helper()
	u, err := store.GetUser(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID, "user %d has no provider", userID)
	return *u.ProviderID
}

// TestCreateUser_HonoursProviderID — POST /api/admin/users dropped
// provider_id on the floor (the request struct had no such field), so every
// user created through the admin API landed in provider 1.
//
// Provider 1 is `default`, and `default` is the SUPERTENANT: the account
// olivov meant to pre-provision for one tenant came out confine-exempt,
// able to see every tenant's storages. Reported as a pre-provisioning gap
// (G1, 2026-08-05); it is also a privilege one.
func TestCreateUser_HonoursProviderID(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, password := testutil.SeedAdmin(t, store) // provider 1 = supertenant
	testutil.LoginAs(t, srv, client, email, password)

	tenantID, _, _ := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "rbac-probe@diyetlif.test", "password": "ProbePass!1",
		"role": "user", "provider_id": tenantID,
	})
	require.Equal(t, http.StatusOK, status, "%v", body)

	newID := int64(body["id"].(float64))
	require.Equal(t, tenantID, providerOf(t, store, newID),
		"the requested provider must be the one the user lands in")
}

// TestCreateUser_DefaultsToCallersProvider — with no provider_id, a tenant
// admin's new user belongs to that admin's tenant. It used to default to
// provider 1 (the supertenant) regardless of who was asking.
func TestCreateUser_DefaultsToCallersProvider(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	tenantID, email, password := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "yeni@diyetlif.test", "password": "ProbePass!1", "role": "user",
	})
	require.Equal(t, http.StatusOK, status, "%v", body)

	newID := int64(body["id"].(float64))
	require.Equal(t, tenantID, providerOf(t, store, newID),
		"a tenant admin's new user must stay inside that tenant")
}

// TestCreateUser_TenantAdminCannotPlantElsewhere — a tenant admin may not
// create a user inside another tenant, nor inside the supertenant.
func TestCreateUser_TenantAdminCannotPlantElsewhere(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	_, email, password := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	otherID, _, _ := seedTenant(t, store, "arasboya", "admin@arasboya.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "planted@arasboya.test", "password": "ProbePass!1",
		"role": "user", "provider_id": otherID,
	})
	require.Equal(t, http.StatusForbidden, status, "%v", body)
}

// TestCreateUser_UnknownProviderIs400 — a provider_id that names nothing is
// a client error, not a silent fallback. provider_id has no FK, so an
// unchecked value would strand the user where nothing can reach them.
func TestCreateUser_UnknownProviderIs400(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "nowhere@test.local", "password": "ProbePass!1",
		"role": "user", "provider_id": 99999,
	})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
}

// TestUpdateUser_ReHomesProvider — the remediation path. Users already
// created into provider 1 by the bug above need a way out, and until now no
// admin endpoint could set a user's provider at all (only OIDC JIT did).
func TestUpdateUser_ReHomesProvider(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	tenantID, _, _ := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)

	status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{
		"email": "stranded@diyetlif.test", "password": "ProbePass!1", "role": "user",
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	newID := int64(body["id"].(float64))

	status, body = doJSON(t, client, http.MethodPatch,
		fmt.Sprintf("%s/api/admin/users/%d", srv.URL, newID),
		map[string]any{"provider_id": tenantID})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, tenantID, providerOf(t, store, newID))
}

package handlers_test

// The instance-wide admin surfaces, under multi-tenancy.
//
// `/api/admin` requires an admin, and in multi-tenant mode that means an admin
// of ANY tenant: auth.TenantResolver labels the request but denies nothing, and
// the scoped store filters three list queries and nothing else. A route whose
// effect is a single global row is therefore open to every tenant admin unless
// its handler asks. These tests are the negative half of that — a real tenant
// admin, signed in over HTTP, trying to change what the whole platform runs on.
//
// ⚠ The single-tenant case is asserted just as explicitly, because it is the
// one a careless gate breaks: the ordinary admin of a non-multi-tenant install
// must still administer everything.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// instanceWideRoutes is the list this change closes. Each entry is one call
// that reaches an instance-wide global row or the process itself.
var instanceWideRoutes = []struct {
	name   string
	method string
	path   string
	body   any
}{
	// The antivirus switch, its mode and the clamd address, plus trash and
	// version retention — one global row each.
	{"protection read", http.MethodGet, "/api/admin/protection", nil},
	{"protection disable antivirus", http.MethodPatch, "/api/admin/protection", map[string]any{"av_enabled": false}},
	// The shared document server + converter, and the JWT secret behind them.
	{"external services read", http.MethodGet, "/api/admin/external", nil},
	{"external services repoint", http.MethodPatch, "/api/admin/external/onlyoffice",
		map[string]any{"enabled": true, "url": "https://attacker.example", "secret": "mine"}},
	// Who can sign in to the instance at all.
	{"auth drivers read", http.MethodGet, "/api/admin/auth-providers", nil},
	{"auth drivers rewrite", http.MethodPatch, "/api/admin/auth-providers/oidc",
		map[string]any{"enabled": true, "config": map[string]any{"issuer": "https://attacker.example"}}},
	// The binary every tenant is served by.
	{"update status", http.MethodGet, "/api/admin/update", nil},
	{"update apply", http.MethodPost, "/api/admin/update/apply", map[string]any{}},
	// Already gated before this change — included so the list is the whole
	// class and not just the part that moved.
	{"tenant lifecycle", http.MethodGet, "/api/admin/providers", nil},
	{"storage plugins", http.MethodGet, "/api/admin/plugins", nil},
}

// TestInstanceWideAdmin_TenantAdminIsRefused is the red proof: on the pre-fix
// code the four ungated surfaces answer 200 to an admin of a tenant the
// platform operator does not control, and the antivirus PATCH really does
// disable scanning for every tenant on the box.
func TestInstanceWideAdmin_TenantAdminIsRefused(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	// Provider 1 (`default`) is the supertenant; this one is a customer.
	_, email, password := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	for _, rt := range instanceWideRoutes {
		t.Run(rt.name, func(t *testing.T) {
			status, body := doJSON(t, client, rt.method, srv.URL+rt.path, rt.body)
			require.Equal(t, http.StatusForbidden, status,
				"a tenant admin reached an instance-wide surface: %v", body)
			assert.Equal(t, "supertenant_only", body["error"],
				"the refusal must name the boundary, so it is visible rather than merely effective")
		})
	}
}

// TestProtection_TenantAdminCannotDisableAntivirus is the specific harm,
// asserted on the stored setting rather than on the status code: the point is
// not that a request was refused, it is that scanning stayed on.
func TestProtection_TenantAdminCannotDisableAntivirus(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	_, email, password := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	before := settingValue(t, store, "antivirus.enabled")

	status, _ := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/protection",
		map[string]any{"av_enabled": false, "av_mode": "daemon", "av_clamd_addr": "attacker.example:3310"})
	require.Equal(t, http.StatusForbidden, status)

	assert.Equal(t, before, settingValue(t, store, "antivirus.enabled"),
		"antivirus was switched off for EVERY tenant by an admin of one of them")
	assert.Empty(t, settingValue(t, store, "antivirus.clamd_addr"),
		"the scanner was pointed at a host the tenant admin controls")
}

// TestInstanceWideAdmin_SupertenantStillPasses — the gate must refuse the
// tenant, not the surface. Without this the previous test would also pass
// against a handler that simply answered 403 to everyone.
func TestInstanceWideAdmin_SupertenantStillPasses(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, password := testutil.SeedAdmin(t, store) // provider 1 = supertenant
	testutil.LoginAs(t, srv, client, email, password)

	for _, path := range []string{
		"/api/admin/protection", "/api/admin/external",
		"/api/admin/auth-providers", "/api/admin/update", "/api/admin/providers",
	} {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+path, nil)
		assert.Equal(t, http.StatusOK, status, "%s refused the platform operator: %v", path, body)
	}

	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/protection",
		map[string]any{"trash_retention_days": 14})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, float64(14), body["trash_retention_days"])
}

// TestInstanceWideAdmin_SingleTenantAdminUnaffected is the case a careless
// gate breaks. On a non-multi-tenant install auth.TenantResolver attaches no
// scope at all, so there is no supertenant to be — and the ordinary admin must
// keep administering everything, exactly as before.
func TestInstanceWideAdmin_SingleTenantAdminUnaffected(t *testing.T) {
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.MultiTenant = false
	})
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	for _, path := range []string{
		"/api/admin/protection", "/api/admin/external",
		"/api/admin/auth-providers", "/api/admin/update", "/api/admin/providers",
	} {
		status, body := doJSON(t, client, http.MethodGet, srv.URL+path, nil)
		assert.Equal(t, http.StatusOK, status,
			"%s refused the admin of a single-tenant install: %v", path, body)
	}

	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/protection",
		map[string]any{"av_enabled": false})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.False(t, body["antivirus"].(map[string]any)["scan_enabled"].(bool),
		"a single-tenant admin still owns the antivirus switch")
}

func settingValue(t *testing.T, store db.Store, key string) string {
	t.Helper()
	v, err := store.GetSetting(t.Context(), key)
	if err != nil {
		return ""
	}
	return v
}

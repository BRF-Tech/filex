package handlers_test

// Admin → Multi-tenant mode (#167, handlers/tenancy_admin.go,
// internal/tenancy). The owner's rules, each measured here:
//
//   - with the mode off, the capabilities answer says so and names no tenant
//     and no realm, and Identity providers lists no tenant's own provider;
//   - FILEX_MULTI_TENANT (or the config file) pins the switch: it is shown
//     locked, with the reason, and a change is refused;
//   - turning the mode off while tenants exist needs the number of tenants;
//     without it nothing is saved, and with it nothing is deleted;
//   - turning it back on brings every tenant back as it was;
//   - a tenant's administrator can neither read nor flip the switch (403),
//     and neither can an API key or the generic settings API;
//   - after a start with the mode off and tenants present, a tenant's open
//     session and API key stop working (maintenance mode on every request).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenancy"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func tenancyPut(t *testing.T, client *http.Client, base string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return doJSON(t, client, http.MethodPut, base+"/api/admin/tenancy", body)
}

// switchedOn leaves what server.New's tenancy.Resolve leaves on an install
// whose switch turned the mode on: the setting saved on. testutil builds the
// router straight from a config and never runs that step.
func switchedOn(t *testing.T, store db.Store) {
	t.Helper()
	require.NoError(t, store.UpsertSetting(context.Background(), tenancy.SettingKey, "true"))
}

func TestTenancy_ModeOffCapabilitiesNameNoTenantAndNoRealm(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)

	// Before the sign-in (the login page's own fetch) and after it.
	for _, signedIn := range []bool{false, true} {
		if signedIn {
			testutil.LoginAs(t, srv, client, email, pw)
		}
		status, caps := doJSON(t, client, http.MethodGet, srv.URL+"/api/capabilities", nil)
		require.Equal(t, http.StatusOK, status)
		on, present := caps["multi_tenant"]
		require.True(t, present, "the one field every screen reads is always there (signed in: %v)", signedIn)
		assert.Equal(t, false, on)
		_, hasRealm := caps["realm"]
		_, hasTenant := caps["tenant"]
		assert.False(t, hasRealm, "no Realm field on a single-tenant sign-in page")
		assert.False(t, hasTenant, "no tenant named on a single-tenant install")
	}

	// On, the same answer says so: the screens that draw tenants follow it.
	srvMT, clientMT, _ := multiTenantServer(t)
	status, caps := doJSON(t, clientMT, http.MethodGet, srvMT.URL+"/api/capabilities", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, caps["multi_tenant"])
	_, hasRealm := caps["realm"]
	assert.True(t, hasRealm)
}

func TestTenancy_ModeOffIdentityProvidersListNoTenantsOwn(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	owner := acme.ID
	_, err = store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "acme-oidc", Driver: "oidc", Origin: model.AuthOriginTenant, OwnerProviderID: &owner})
	require.NoError(t, err)

	raw, list := listProviders(t, client, srv.URL)
	_, has := list["acme-oidc"]
	assert.False(t, has, "a tenant's own provider is not listed while the mode is off: %s", raw)
	for name, p := range list {
		_, owned := p["owner_provider_id"]
		assert.False(t, owned, "%s names an owner tenant", name)
		_, bound := p["tenants"]
		assert.False(t, bound, "%s names tenants", name)
	}
}

func TestTenancy_TheEnvironmentPinsTheSwitch(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		from     string
		lockedBy string
		variable string
	}{
		{tenancy.EnvVar, tenancy.LockedByEnvironment, tenancy.EnvVar},
		{"file:/etc/filex/config.yaml", tenancy.LockedByConfigFile, "multi_tenant"},
	} {
		srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
			c.MultiTenant = true
			c.MultiTenantFrom = tc.from
		})
		email, pw := testutil.SeedAdmin(t, store)
		testutil.LoginAs(t, srv, client, email, pw)

		status, view := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tenancy", nil)
		require.Equal(t, http.StatusOK, status, "%v", view)
		assert.Equal(t, true, view["locked"], tc.from)
		assert.Equal(t, tc.lockedBy, view["locked_by"], tc.from)
		assert.Equal(t, tc.variable, view["variable"], tc.from)
		assert.Equal(t, true, view["in_force"], tc.from)
		assert.Equal(t, true, view["next_start"], tc.from)
		assert.Equal(t, false, view["restart_required"], tc.from)

		status, body := tenancyPut(t, client, srv.URL, map[string]any{"enabled": false, "confirm": "0"})
		assert.Equal(t, http.StatusConflict, status, tc.from)
		assert.Equal(t, "tenancy_locked", body["error"], tc.from)
		assert.Equal(t, tc.lockedBy, body["locked_by"], tc.from)
		_, saved := tenancy.Saved(ctx, store)
		assert.False(t, saved, "a pinned switch saves nothing (%s)", tc.from)
	}
}

func TestTenancy_OffWithTenantsNeedsTheirNumberAndDeletesNothing(t *testing.T) {
	ctx := context.Background()
	srv, client, store := multiTenantServer(t)
	switchedOn(t, store)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	acmeID, _, _ := seedTenant(t, store, "acme", "admin@acme.test", false)
	memberID := seedUserIn(t, store, acmeID, "member@acme.test")

	status, view := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/tenancy", nil)
	require.Equal(t, http.StatusOK, status, "%v", view)
	assert.Equal(t, false, view["locked"])
	assert.EqualValues(t, 1, view["tenants"])

	// Without the number, and with a wrong one: refused, nothing saved.
	for _, confirm := range []string{"", "2", "yes"} {
		status, body := tenancyPut(t, client, srv.URL, map[string]any{"enabled": false, "confirm": confirm})
		require.Equal(t, http.StatusConflict, status, "confirm %q: %v", confirm, body)
		assert.Equal(t, "confirm_required", body["error"])
		assert.EqualValues(t, 1, body["tenants"])
		on, _ := tenancy.Saved(ctx, store)
		require.True(t, on, "a refused switch saves nothing (confirm %q)", confirm)
	}

	// With it: saved for the next start, and said so.
	status, body := tenancyPut(t, client, srv.URL, map[string]any{"enabled": false, "confirm": "1"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["in_force"], "the running server keeps its mode until it is restarted")
	assert.Equal(t, false, body["next_start"])
	assert.Equal(t, true, body["restart_required"])

	// The next start: off, the tenant in maintenance mode, nothing deleted.
	next := config.Default()
	tenancy.Resolve(ctx, store, &next)
	require.False(t, next.MultiTenant)
	p, err := store.GetProvider(ctx, acmeID)
	require.NoError(t, err)
	require.NotNil(t, p, "the tenant is still there")
	member, err := store.GetUser(ctx, memberID)
	require.NoError(t, err)
	require.NotNil(t, member, "its people are still there")
	assert.Equal(t, auth.SSOReasonMaintenance, auth.LoginBlockReason(ctx, store, next.MultiTenant, member))
}

func TestTenancy_BackOnBringsEveryTenantBack(t *testing.T) {
	ctx := context.Background()
	srv, client, store := multiTenantServer(t)
	switchedOn(t, store)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	acmeID, _, _ := seedTenant(t, store, "acme", "admin@acme.test", false)
	memberID := seedUserIn(t, store, acmeID, "member@acme.test")

	status, body := tenancyPut(t, client, srv.URL, map[string]any{"enabled": false, "confirm": "1"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	// Back on needs no number: nobody loses anything by it.
	status, body = tenancyPut(t, client, srv.URL, map[string]any{"enabled": true})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["next_start"])
	assert.Equal(t, false, body["restart_required"])

	again := config.Default()
	tenancy.Resolve(ctx, store, &again)
	require.True(t, again.MultiTenant)
	member, err := store.GetUser(ctx, memberID)
	require.NoError(t, err)
	require.NotNil(t, member.ProviderID)
	assert.Equal(t, acmeID, *member.ProviderID, "still the tenant's")
	assert.Equal(t, "", auth.LoginBlockReason(ctx, store, again.MultiTenant, member), "signs in again")

	// Both changes are in the audit log, and a no-op is not.
	status, _ = tenancyPut(t, client, srv.URL, map[string]any{"enabled": true})
	require.Equal(t, http.StatusOK, status)
	rows, err := store.ListAuditRecent(ctx, 100)
	require.NoError(t, err)
	count := map[string]int{}
	for _, r := range rows {
		count[r.Action]++
	}
	assert.Equal(t, 1, count[tenancy.ActionDisable])
	assert.Equal(t, 1, count[tenancy.ActionEnable])
}

func TestTenancy_ATenantsAdministratorCannotSeeOrFlipIt(t *testing.T) {
	srv, _, store := multiTenantServer(t)
	switchedOn(t, store)
	_, tenantEmail, tenantPw := seedTenant(t, store, "acme", "admin@acme.test", false)
	tenantAdmin := freshClient(t)
	testutil.LoginAs(t, srv, tenantAdmin, tenantEmail, tenantPw)

	status, body := doJSON(t, tenantAdmin, http.MethodGet, srv.URL+"/api/admin/tenancy", nil)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "supertenant_only", body["error"])
	status, body = tenancyPut(t, tenantAdmin, srv.URL, map[string]any{"enabled": false, "confirm": "1"})
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "supertenant_only", body["error"])
	on, _ := tenancy.Saved(context.Background(), store)
	assert.True(t, on, "still on: the refusal saved nothing")
}

func TestTenancy_OnlyAPersonOnThisDoorFlipsIt(t *testing.T) {
	ctx := context.Background()
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	// An API key reads it and cannot flip it.
	key := testutil.NewAPIToken(t, store, admin.ID, "read,write,delete,admin")
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/admin/tenancy", strings.NewReader(`{"enabled":true}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	_, saved := tenancy.Saved(ctx, store)
	assert.False(t, saved, "an API key saved nothing")

	// The generic settings API refuses the switch's row, alone or in a batch.
	status, body := doJSON(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+tenancy.SettingKey, map[string]any{"value": "true"})
	assert.Equal(t, http.StatusBadRequest, status, "%v", body)
	assert.Equal(t, "tenancy_setting", body["error"])
	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/settings", map[string]any{tenancy.SettingKey: "true", "share.max_ttl_days": "7"})
	assert.Equal(t, http.StatusBadRequest, status, "%v", body)
	_, saved = tenancy.Saved(ctx, store)
	assert.False(t, saved, "the settings API saved nothing")
	v, _ := store.GetSetting(ctx, "share.max_ttl_days")
	assert.NotEqual(t, "7", v, "a refused batch writes none of its keys")

	// The panel's own door does, for the next start.
	status, body = tenancyPut(t, client, srv.URL, map[string]any{"enabled": true})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["in_force"])
	assert.Equal(t, true, body["next_start"])
	assert.Equal(t, true, body["restart_required"])
}

func TestTenancy_MaintenanceModeEndsATenantsOpenSessionAndKey(t *testing.T) {
	ctx := context.Background()
	// A start with the mode off on an install that has tenants
	// (internal/server: Deps.TenantLockout).
	srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { d.TenantLockout = true })
	acmeID, tenantEmail, _ := seedTenant(t, store, "acme", "admin@acme.test", false)
	require.NotZero(t, acmeID)
	tenantAdmin, err := store.GetUserByEmail(ctx, tenantEmail)
	require.NoError(t, err)
	superEmail, superPw := testutil.SeedAdmin(t, store)

	// A session opened while the mode was on, and an API key.
	_, err = store.CreateSession(ctx, tenantAdmin.ID, "tok-acme-before-restart", time.Now().Add(time.Hour), "", "")
	require.NoError(t, err)
	key := testutil.NewAPIToken(t, store, tenantAdmin.ID, "read,write,delete,admin")

	get := func(path string, set func(*http.Request)) (int, map[string]any) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		set(req)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	bySession := func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "filex_session", Value: "tok-acme-before-restart"})
	}
	byKey := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }

	for _, path := range []string{"/api/auth/me", "/api/admin/settings"} {
		status, body := get(path, bySession)
		assert.Equal(t, http.StatusUnauthorized, status, "session %s: %v", path, body)
		assert.Equal(t, auth.SSOReasonMaintenance, body["reason"], "session %s", path)
		status, body = get(path, byKey)
		assert.Equal(t, http.StatusUnauthorized, status, "key %s: %v", path, body)
		assert.Equal(t, auth.SSOReasonMaintenance, body["reason"], "key %s", path)
	}
	_, err = store.GetSessionByToken(ctx, "tok-acme-before-restart")
	assert.NoError(t, err, "the session is kept for when the mode is back on")

	// The platform's own administrator is not touched.
	super := freshClient(t)
	testutil.LoginAs(t, srv, super, superEmail, superPw)
	status, body := doJSON(t, super, http.MethodGet, srv.URL+"/api/admin/settings", nil)
	assert.Equal(t, http.StatusOK, status, "%v", body)
}

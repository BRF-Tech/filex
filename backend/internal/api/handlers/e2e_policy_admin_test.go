package handlers_test

// Who may encrypt, the administrator's half (handlers/e2e_policy_admin.go):
// whose policy an administrator holds, who may switch a tenant's ceiling, and
// that a change is a signed-in person's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// e2ePolicyWire is GET / PATCH /api/admin/e2e.
type e2ePolicyWire struct {
	Available bool   `json:"available"`
	Policy    string `json:"policy"`
	Scope     string `json:"scope"`
	Tenant    *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"tenant"`
	Pending int `json:"pending"`
}

// e2eTenantWire is one row of GET /api/admin/e2e/tenants, and PATCH's answer.
type e2eTenantWire struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	IsSupertenant bool   `json:"is_supertenant"`
	Allowed       bool   `json:"e2e_allowed"`
	Policy        string `json:"e2e_policy"`
}

func decodeE2EPolicy(t *testing.T, raw []byte) e2ePolicyWire {
	t.Helper()
	var v e2ePolicyWire
	require.NoError(t, json.Unmarshal(raw, &v), string(raw))
	return v
}

// e2eAuditRows are the audit rows of one action, newest first.
func e2eAuditRows(t *testing.T, store db.Store, action string) []*model.AuditEntry {
	t.Helper()
	rows, _, err := store.ListAuditFiltered(context.Background(), nil, action, nil, nil, 50, 0)
	require.NoError(t, err)
	out := make([]*model.AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Entry)
	}
	return out
}

// A tenant administrator holds their own tenant's policy — and nobody else's.
func TestE2EPolicyAdmin_TenantAdminHoldsItsOwnPolicy(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	mine, email, pw := seedTenant(t, store, "acme", "admin@acme.test", false)
	other, _, _ := seedTenant(t, store, "globex", "admin@globex.test", false)
	testutil.LoginAs(t, srv, client, email, pw)
	ctx := context.Background()

	st, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/admin/e2e", nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	v := decodeE2EPolicy(t, raw)
	assert.Equal(t, "tenant", v.Scope)
	require.NotNil(t, v.Tenant)
	assert.Equal(t, mine, v.Tenant.ID)
	assert.Equal(t, "acme", v.Tenant.Name)
	assert.True(t, v.Available, "the ceiling is on until the operator switches it off")
	assert.Equal(t, model.E2EPolicyPermitted, v.Policy, "the default is what filex did before the policy existed")
	assert.Zero(t, v.Pending)

	st, raw = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": "approval"})
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Equal(t, model.E2EPolicyApproval, decodeE2EPolicy(t, raw).Policy)

	got, err := store.GetProviderE2E(ctx, mine)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyApproval}, got)
	theirs, err := store.GetProviderE2E(ctx, other)
	require.NoError(t, err)
	assert.Equal(t, model.E2EPolicyPermitted, theirs.Policy, "a tenant administrator's write reached another tenant")

	rows := e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 1)
	assert.Equal(t, e2epolicy.AuditTargetPolicy, rows[0].TargetType)
	assert.Equal(t, strconv.FormatInt(mine, 10), rows[0].TargetID)
	assert.Equal(t, "acme", rows[0].Metadata["target_name"])
	assert.Equal(t, "permitted", rows[0].Metadata["before"])
	assert.Equal(t, "approval", rows[0].Metadata["after"])

	// The same value again changes nothing, and the log says nothing about it.
	st, _ = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": "approval"})
	require.Equal(t, http.StatusOK, st)
	assert.Len(t, e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate), 1)
	// …and the middleware wrote no generic row beside the handler's.
	assert.Empty(t, e2eAuditRows(t, store, "e2e.update"))
}

// The ceiling is the operator's: a tenant administrator can neither read the
// table nor switch a ceiling — another tenant's, or their own back on.
func TestE2EPolicyAdmin_TenantAdminCannotTouchTheCeiling(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	mine, email, pw := seedTenant(t, store, "acme", "admin@acme.test", false)
	other, _, _ := seedTenant(t, store, "globex", "admin@globex.test", false)
	testutil.LoginAs(t, srv, client, email, pw)
	ctx := context.Background()
	require.NoError(t, store.SetProviderE2E(ctx, mine, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}))

	st, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/e2e/tenants", nil)
	require.Equal(t, http.StatusForbidden, st, "%v", body)
	assert.Equal(t, "supertenant_only", body["error"])
	for _, id := range []int64{other, mine} {
		st, body = doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, id),
			map[string]any{"e2e_allowed": id == mine})
		require.Equal(t, http.StatusForbidden, st, "%v", body)
		assert.Equal(t, "supertenant_only", body["error"])
	}
	theirs, err := store.GetProviderE2E(ctx, other)
	require.NoError(t, err)
	assert.True(t, theirs.Allowed, "a tenant administrator switched another tenant's ceiling off")

	// Their own policy stays theirs to choose while the ceiling is off — and a
	// ceiling smuggled into that body changes nothing.
	st, raw := doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e",
		map[string]any{"policy": "admins", "e2e_allowed": true})
	require.Equal(t, http.StatusOK, st, string(raw))
	v := decodeE2EPolicy(t, raw)
	assert.False(t, v.Available)
	assert.Equal(t, model.E2EPolicyAdmins, v.Policy)
	got, err := store.GetProviderE2E(ctx, mine)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyAdmins}, got)
}

// The operator reads every tenant's ceiling and switches one; their own
// policy is the supertenant's own row.
func TestE2EPolicyAdmin_SupertenantSwitchesATenantsCeiling(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, pw := testutil.SeedAdmin(t, store) // provider 1 (`default`) = supertenant
	testutil.LoginAs(t, srv, client, email, pw)
	tenantID, tEmail, tPw := seedTenant(t, store, "acme", "admin@acme.test", false)
	ctx := context.Background()

	st, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/admin/e2e/tenants", nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	var list struct {
		Tenants []e2eTenantWire `json:"tenants"`
	}
	require.NoError(t, json.Unmarshal(raw, &list), string(raw))
	bySlug := map[string]e2eTenantWire{}
	for _, row := range list.Tenants {
		bySlug[row.Slug] = row
	}
	require.Contains(t, bySlug, "acme")
	assert.Equal(t, e2eTenantWire{ID: tenantID, Slug: "acme", Allowed: true, Policy: model.E2EPolicyPermitted}, bySlug["acme"])
	assert.True(t, bySlug[model.DefaultProviderSlug].IsSupertenant)

	url := fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, tenantID)
	st, raw = doReq(t, client, http.MethodPatch, url, map[string]any{"e2e_allowed": false})
	require.Equal(t, http.StatusOK, st, string(raw))
	var row e2eTenantWire
	require.NoError(t, json.Unmarshal(raw, &row))
	assert.False(t, row.Allowed)
	assert.Equal(t, model.E2EPolicyPermitted, row.Policy, "the ceiling leaves the tenant's policy alone")
	got, err := store.GetProviderE2E(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}, got)

	rows := e2eAuditRows(t, store, e2epolicy.AuditActionTenantUpdate)
	require.Len(t, rows, 1)
	assert.Equal(t, e2epolicy.AuditTargetTenant, rows[0].TargetType)
	assert.Equal(t, strconv.FormatInt(tenantID, 10), rows[0].TargetID)
	assert.Equal(t, true, rows[0].Metadata["before"])
	assert.Equal(t, false, rows[0].Metadata["after"])

	st, _ = doReq(t, client, http.MethodPatch, fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, 999999), map[string]any{"e2e_allowed": true})
	assert.Equal(t, http.StatusNotFound, st)
	st, _ = doReq(t, client, http.MethodPatch, url, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, st, "a PATCH that names no ceiling changes nothing")

	// The operator's own policy is the supertenant's row.
	super, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	st, raw = doReq(t, client, http.MethodGet, srv.URL+"/api/admin/e2e", nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	v := decodeE2EPolicy(t, raw)
	assert.Equal(t, "tenant", v.Scope, "every administrator of a multi-tenant install holds a tenant's row")
	require.NotNil(t, v.Tenant)
	assert.Equal(t, super.ID, v.Tenant.ID)

	// The tenant's administrator sees the ceiling they cannot change.
	tenantClient := freshClient(t)
	testutil.LoginAs(t, srv, tenantClient, tEmail, tPw)
	st, raw = doReq(t, tenantClient, http.MethodGet, srv.URL+"/api/admin/e2e", nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.False(t, decodeE2EPolicy(t, raw).Available)
}

// A single-tenant install has one policy: the instance setting.
func TestE2EPolicyAdmin_SingleTenantAdminWritesTheSetting(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	st, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/admin/e2e", nil)
	require.Equal(t, http.StatusOK, st, string(raw))
	v := decodeE2EPolicy(t, raw)
	assert.Equal(t, e2ePolicyWire{Available: true, Policy: model.E2EPolicyPermitted, Scope: "instance"}, v)

	st, raw = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": "admins"})
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Equal(t, model.E2EPolicyAdmins, decodeE2EPolicy(t, raw).Policy)
	assert.Equal(t, model.E2EPolicyAdmins, settingValue(t, store, model.SettingE2EPolicy))

	rows := e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 1)
	assert.Empty(t, rows[0].TargetID, "the instance's policy names no tenant")

	// Nothing on a single-tenant install is somebody else's to refuse.
	st, _ = doReq(t, client, http.MethodGet, srv.URL+"/api/admin/e2e/tenants", nil)
	assert.Equal(t, http.StatusOK, st)
}

// A policy the rule does not know is refused at every door that writes one —
// the rule would read it as `permitted`.
func TestE2EPolicyAdmin_UnknownPolicyIsRefused(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	for _, door := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/admin/e2e", map[string]any{"policy": "everyone"}},
		{http.MethodPut, "/api/admin/settings/" + model.SettingE2EPolicy, map[string]any{"value": "everyone"}},
		{http.MethodPatch, "/api/admin/settings", map[string]any{model.SettingE2EPolicy: "sometimes"}},
	} {
		st, body := doJSON(t, client, door.method, srv.URL+door.path, door.body)
		require.Equal(t, http.StatusBadRequest, st, "%s %s: %v", door.method, door.path, body)
		assert.Equal(t, "invalid_policy", body["error"])
	}
	assert.Empty(t, settingValue(t, store, model.SettingE2EPolicy), "a refused value was stored")

	st, body := doJSON(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+model.SettingE2EPolicy, map[string]any{"value": "off"})
	require.Equal(t, http.StatusOK, st, "%v", body)
	st, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/admin/e2e", nil)
	require.Equal(t, http.StatusOK, st)
	assert.Equal(t, model.E2EPolicyOff, decodeE2EPolicy(t, raw).Policy, "the settings API and this page are one setting")
}

// An admin-scoped API key reads the policy; changing it, or a ceiling, is a
// person's.
func TestE2EPolicyAdmin_AnAPIKeyReadsButCannotChange(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	useProductionAuthChain(t, store)
	email, _ := testutil.SeedAdmin(t, store)
	admin, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, store, admin.ID, "admin,read")

	st, raw := withToken(t, http.MethodGet, srv.URL+"/api/admin/e2e", tok, nil)
	require.Equal(t, http.StatusOK, st, string(raw))

	for _, call := range []struct {
		path string
		body any
	}{
		{"/api/admin/e2e", map[string]any{"policy": "off"}},
		{"/api/admin/e2e/tenants/1", map[string]any{"e2e_allowed": false}},
	} {
		st, raw = withToken(t, http.MethodPatch, srv.URL+call.path, tok, call.body)
		require.Equal(t, http.StatusForbidden, st, "%s: %s", call.path, raw)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.Equal(t, "session_required", body["error"], call.path)
	}
	assert.Empty(t, settingValue(t, store, model.SettingE2EPolicy))
	got, err := store.GetProviderE2E(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, got.Allowed)
}

// The settings API is a second door to the same setting, and /api/ai/admin and
// the admin_settings_* MCP tools reach the same handlers. An API key is refused
// at every one of them, and a batch that holds the policy is refused whole:
// nothing in it is written.
func TestE2EPolicyAdmin_AnAPIKeyCannotChangeItThroughTheSettingsAPI(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	useProductionAuthChain(t, store)
	email, _ := testutil.SeedAdmin(t, store)
	admin, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	tok := testutil.NewAPIToken(t, store, admin.ID, "admin,read,mcp")
	siteName := settingValue(t, store, "site_name")

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/admin/settings/" + model.SettingE2EPolicy, map[string]any{"value": "off"}},
		{http.MethodPatch, "/api/admin/settings", map[string]any{model.SettingE2EPolicy: "off", "site_name": "a key was here"}},
		{http.MethodPut, "/api/ai/admin/settings/" + model.SettingE2EPolicy, map[string]any{"value": "off"}},
	} {
		st, raw := withToken(t, call.method, srv.URL+call.path, tok, call.body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		assert.Equal(t, http.StatusForbidden, st, "%s %s: %s", call.method, call.path, raw)
		assert.Equal(t, "session_required", body["error"], "%s %s", call.method, call.path)
	}

	// The same handlers, reached as an MCP tool.
	code, out := mcpPost(t, &http.Client{}, srv.URL+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_settings_update",`+
			`"arguments":{"body":{"e2e.policy":"off","site_name":"an agent was here"}}}}`)
	require.Equal(t, http.StatusOK, code, out)
	assert.Contains(t, out, "session_required", "admin_settings_update: %s", out)

	assert.Empty(t, settingValue(t, store, model.SettingE2EPolicy), "an API key changed who may encrypt")
	assert.Equal(t, siteName, settingValue(t, store, "site_name"), "a refused batch wrote the rest of itself")
	assert.Empty(t, e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate))
}

// A change through the settings API is the policy's change, and it is recorded
// as PATCH /api/admin/e2e records it: one e2e_policy.update row with the value
// before and after. Not a generic settings row, which the Audit page's
// "Encryption policy" filter never finds, and not both.
func TestE2EPolicyAdmin_TheSettingsAPIRecordsTheChangeAsThePolicys(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	admin, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)

	st, raw := doReq(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+model.SettingE2EPolicy, map[string]any{"value": "approval"})
	require.Equal(t, http.StatusOK, st, string(raw))
	rows := e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 1)
	assert.Equal(t, e2epolicy.AuditTargetPolicy, rows[0].TargetType)
	assert.Empty(t, rows[0].TargetID, "the instance's policy names no tenant")
	require.NotNil(t, rows[0].UserID)
	assert.Equal(t, admin.ID, *rows[0].UserID)
	assert.Equal(t, "permitted", rows[0].Metadata["before"])
	assert.Equal(t, "approval", rows[0].Metadata["after"])

	st, raw = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/settings", map[string]any{model.SettingE2EPolicy: "off"})
	require.Equal(t, http.StatusOK, st, string(raw))
	rows = e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 2)
	assert.Equal(t, "approval", rows[0].Metadata["before"])
	assert.Equal(t, "off", rows[0].Metadata["after"])
	assert.Empty(t, e2eAuditRows(t, store, "settings.update"), "the middleware wrote a generic row beside the policy's")

	// A batch that also holds other settings: the policy did not change, so it
	// adds no row, and the other keys keep the settings row they always had.
	st, raw = doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/settings",
		map[string]any{model.SettingE2EPolicy: "off", "site_name": "Dosyalar"})
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Len(t, e2eAuditRows(t, store, e2epolicy.AuditActionPolicyUpdate), 2, "the same value again is no change")
	assert.Len(t, e2eAuditRows(t, store, "settings.update"), 1, "the batch's other keys lost their row")
	assert.Equal(t, model.E2EPolicyOff, settingValue(t, store, model.SettingE2EPolicy))
}

// One batch that holds the policy and a sign-in setting (0.50's `login.*`)
// writes both. The policy is written as its own door writes it, with its one
// e2e_policy.update row; the sign-in setting reaches the running limiter at
// once, which reads its settings again; and the request keeps the one
// settings row it always had, naming the sign-in field. Not the Sign-in
// security page's own row: the batch changed more than its settings.
func TestE2EPolicyAdmin_ABatchWithSignInSettingsWritesBoth(t *testing.T) {
	e := newSecEnv(t, nil)
	ctx := context.Background()
	st, raw := doReq(t, e.admin, http.MethodPatch, e.srv.URL+"/api/admin/settings",
		map[string]any{model.SettingE2EPolicy: model.E2EPolicyOff, loginguard.KeyAccountMax: "2"})
	require.Equal(t, http.StatusOK, st, string(raw))

	assert.Equal(t, model.E2EPolicyOff, settingValue(t, e.store, model.SettingE2EPolicy))
	assert.Equal(t, "2", settingValue(t, e.store, loginguard.KeyAccountMax))
	assert.EqualValues(t, 2, e.guard.Config(ctx).AccountMax, "the limiter still holds the value from before")

	rows := e2eAuditRows(t, e.store, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 1)
	assert.Equal(t, "permitted", rows[0].Metadata["before"])
	assert.Equal(t, "off", rows[0].Metadata["after"])
	settings := e2eAuditRows(t, e.store, "settings.update")
	require.Len(t, settings, 1, "one request, one settings row")
	assert.Equal(t, []any{"account_max_fails"}, settings[0].Metadata["login_security_fields"])
	assert.Empty(t, e2eAuditRows(t, e.store, loginguard.ActionSettingsUpdate), "the batch was filed as the sign-in page's")
}

// e2eFlakyStore fails the reads around a policy change: the value it replaces
// (failBefore, once: the instance setting read transiently failing), what is
// read back once the change is stored (failReadBack: the pending count, the
// view's last read), or the tenant a change names (failProvider: its id).
type e2eFlakyStore struct {
	db.Store
	failBefore   atomic.Bool
	failReadBack atomic.Bool
	failProvider atomic.Int64
}

func (s *e2eFlakyStore) GetProvider(ctx context.Context, id int64) (*model.Provider, error) {
	if failing := s.failProvider.Load(); failing != 0 && failing == id {
		return nil, errors.New("disk I/O error")
	}
	return s.Store.GetProvider(ctx, id)
}

func (s *e2eFlakyStore) GetSetting(ctx context.Context, key string) (string, error) {
	if key == model.SettingE2EPolicy && s.failBefore.CompareAndSwap(true, false) {
		return "", errors.New("database is locked")
	}
	return s.Store.GetSetting(ctx, key)
}

func (s *e2eFlakyStore) ListE2ERequests(ctx context.Context, f model.E2ERequestFilter) ([]*model.E2ERequest, error) {
	if s.failReadBack.Load() {
		return nil, errors.New("connection reset by peer")
	}
	return s.Store.ListE2ERequests(ctx, f)
}

// e2eFlakyServer is a test server whose handlers see an e2eFlakyStore.
func e2eFlakyServer(t *testing.T, multiTenant bool) (*httptest.Server, *http.Client, *e2eFlakyStore) {
	t.Helper()
	var fs *e2eFlakyStore
	srv, client, _ := testutil.NewTestServerWith(t, func(c *config.Config) { c.MultiTenant = multiTenant }, func(d *api.Deps) {
		fs = &e2eFlakyStore{Store: d.Store}
		d.Store = fs
	})
	return srv, client, fs
}

// A stored change is audited whatever happens after it: the row is written as
// soon as the change is, before the answer is read back, so a read that fails
// then answers 500 without leaving the change unrecorded.
func TestE2EPolicyAdmin_AStoredChangeIsAuditedWhenReadingItBackFails(t *testing.T) {
	srv, client, fs := e2eFlakyServer(t, true)
	mine, email, pw := seedTenant(t, fs, "acme", "admin@acme.test", false)
	testutil.LoginAs(t, srv, client, email, pw)

	fs.failReadBack.Store(true)
	st, raw := doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": "approval"})
	require.Equal(t, http.StatusInternalServerError, st, string(raw))

	got, err := fs.GetProviderE2E(context.Background(), mine)
	require.NoError(t, err)
	require.Equal(t, model.E2EPolicyApproval, got.Policy, "the change was not stored")
	rows := e2eAuditRows(t, fs, e2epolicy.AuditActionPolicyUpdate)
	require.Len(t, rows, 1, "a stored change was left unaudited")
	assert.Equal(t, strconv.FormatInt(mine, 10), rows[0].TargetID)
	assert.Equal(t, "acme", rows[0].Metadata["target_name"])
	assert.Equal(t, "permitted", rows[0].Metadata["before"])
	assert.Equal(t, "approval", rows[0].Metadata["after"])
}

// The value a change replaces is read before anything is written, and a read
// that fails is a failure, at every door that writes the policy. Read as the
// default, it would store `permitted` over an `off` with before == after, and
// write no row for it.
func TestE2EPolicyAdmin_AFailedReadOfThePolicyItReplacesWritesNothing(t *testing.T) {
	srv, client, fs := e2eFlakyServer(t, false)
	email, pw := testutil.SeedAdmin(t, fs)
	testutil.LoginAs(t, srv, client, email, pw)
	require.NoError(t, fs.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff))
	siteName := settingValue(t, fs, "site_name")

	for _, door := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPatch, "/api/admin/e2e", map[string]any{"policy": "permitted"}},
		{http.MethodPut, "/api/admin/settings/" + model.SettingE2EPolicy, map[string]any{"value": "permitted"}},
		{http.MethodPatch, "/api/admin/settings", map[string]any{model.SettingE2EPolicy: "permitted", "site_name": "Dosyalar"}},
	} {
		fs.failBefore.Store(true)
		st, raw := doReq(t, client, door.method, srv.URL+door.path, door.body)
		assert.Equal(t, http.StatusInternalServerError, st, "%s %s: %s", door.method, door.path, raw)
		assert.Equal(t, model.E2EPolicyOff, settingValue(t, fs, model.SettingE2EPolicy),
			"%s %s stored a change over a value it could not read", door.method, door.path)
	}
	assert.Equal(t, siteName, settingValue(t, fs, "site_name"), "the refused batch wrote the rest of itself")
	assert.Empty(t, e2eAuditRows(t, fs, e2epolicy.AuditActionPolicyUpdate))
}

// e2eReplicaStore runs `between` once, right after the next GetProviderE2E
// has answered: what another filex instance on the same database writes while
// this one is between reading a tenant's row and writing its change.
type e2eReplicaStore struct {
	db.Store
	between atomic.Pointer[func()]
}

func (s *e2eReplicaStore) GetProviderE2E(ctx context.Context, id int64) (model.ProviderE2E, error) {
	e, err := s.Store.GetProviderE2E(ctx, id)
	if f := s.between.Swap(nil); f != nil {
		(*f)()
	}
	return e, err
}

// With several filex instances on one PostgreSQL or MySQL, a tenant
// administrator's policy change and the operator's ceiling change interleave:
// one instance reads the row, the other writes, the first writes. What the
// second wrote stays, for two reasons:
//   - each change writes its own column only, so neither carries a stale copy
//     of the other back: a tenant administrator's policy change never
//     switches the operator's ceiling back on;
//   - a save of the value that was read writes nothing. It would put that
//     value back over another instance's change of the same column, and no
//     audit row would say so: a change is recorded only when the value differs
//     from the one read.
func TestE2EPolicyAdmin_AnotherInstancesChangeStays(t *testing.T) {
	var rs *e2eReplicaStore
	srv, tenantAdmin, _ := testutil.NewTestServerWith(t, func(c *config.Config) { c.MultiTenant = true }, func(d *api.Deps) {
		rs = &e2eReplicaStore{Store: d.Store}
		d.Store = rs
	})
	ctx := context.Background()
	opEmail, opPw := testutil.SeedAdmin(t, rs) // provider 1 (`default`) = supertenant
	operator := freshClient(t)
	testutil.LoginAs(t, srv, operator, opEmail, opPw)
	mine, email, pw := seedTenant(t, rs, "acme", "admin@acme.test", false)
	testutil.LoginAs(t, srv, tenantAdmin, email, pw)
	other := func(e model.ProviderE2E) *func() {
		f := func() { assert.NoError(t, rs.Store.SetProviderE2E(ctx, mine, e)) }
		return &f
	}
	savePolicy := func(policy string) {
		t.Helper()
		st, raw := doReq(t, tenantAdmin, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": policy})
		require.Equal(t, http.StatusOK, st, string(raw))
	}
	saveCeiling := func(allowed bool) {
		t.Helper()
		st, raw := doReq(t, operator, http.MethodPatch, fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, mine),
			map[string]any{"e2e_allowed": allowed})
		require.Equal(t, http.StatusOK, st, string(raw))
	}
	stored := func() model.ProviderE2E {
		t.Helper()
		got, err := rs.GetProviderE2E(ctx, mine)
		require.NoError(t, err)
		return got
	}

	// The operator switches the tenant's encryption off on another instance
	// while this one is between reading the row and writing the tenant's
	// policy.
	rs.between.Store(other(model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyPermitted}))
	savePolicy(model.E2EPolicyAdmins)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyAdmins}, stored(),
		"a tenant administrator's policy change switched the operator's ceiling back on")

	// Another administrator of the tenant switches the policy off on another
	// instance while this one, between reading the row and writing it, saves
	// the policy it had read.
	rs.between.Store(other(model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyOff}))
	savePolicy(model.E2EPolicyAdmins)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyOff}, stored(),
		"a stale save of the policy it had read put it back over another instance's change")

	// The tenant chooses approval on another instance while this one is
	// between reading the row and writing the operator's ceiling.
	rs.between.Store(other(model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}))
	saveCeiling(true)
	assert.Equal(t, model.ProviderE2E{Allowed: true, Policy: model.E2EPolicyApproval}, stored(),
		"the operator's ceiling change put back the policy the tenant had replaced")

	// The operator switches the ceiling off on another instance while this
	// one, between reading the row and writing it, saves the ceiling it had
	// read.
	rs.between.Store(other(model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}))
	saveCeiling(true)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}, stored(),
		"a stale save of the ceiling it had read switched it back on over another instance's switch")
}

// e2eWriteCountingStore counts the writes a policy or a ceiling change makes:
// the tenant's policy and ceiling, and a single-tenant install's e2e.policy
// setting.
type e2eWriteCountingStore struct {
	db.Store
	policy, ceiling, setting atomic.Int64
}

func (s *e2eWriteCountingStore) SetProviderE2EPolicy(ctx context.Context, id int64, policy string) error {
	s.policy.Add(1)
	return s.Store.SetProviderE2EPolicy(ctx, id, policy)
}

func (s *e2eWriteCountingStore) SetProviderE2EAllowed(ctx context.Context, id int64, allowed bool) error {
	s.ceiling.Add(1)
	return s.Store.SetProviderE2EAllowed(ctx, id, allowed)
}

func (s *e2eWriteCountingStore) UpsertSetting(ctx context.Context, key, value string) error {
	if key == model.SettingE2EPolicy {
		s.setting.Add(1)
	}
	return s.Store.UpsertSetting(ctx, key, value)
}

// e2eCountingServer is a test server whose handlers see an e2eWriteCountingStore.
func e2eCountingServer(t *testing.T, multiTenant bool) (*httptest.Server, *http.Client, *e2eWriteCountingStore) {
	t.Helper()
	var cs *e2eWriteCountingStore
	srv, client, _ := testutil.NewTestServerWith(t, func(c *config.Config) { c.MultiTenant = multiTenant }, func(d *api.Deps) {
		cs = &e2eWriteCountingStore{Store: d.Store}
		d.Store = cs
	})
	return srv, client, cs
}

// Saving the policy or the ceiling a tenant already has writes nothing, and
// records nothing: the audit row exists only for a change, and a write that
// changes nothing the row could show would be a write nobody can see (see
// TestE2EPolicyAdmin_AnotherInstancesChangeStays for what it could undo). The
// answer is the same either way. A change is written once and recorded once.
func TestE2EPolicyAdmin_SavingTheValueATenantAlreadyHasWritesNothing(t *testing.T) {
	srv, tenantAdmin, cs := e2eCountingServer(t, true)
	opEmail, opPw := testutil.SeedAdmin(t, cs) // provider 1 (`default`) = supertenant
	operator := freshClient(t)
	testutil.LoginAs(t, srv, operator, opEmail, opPw)
	mine, email, pw := seedTenant(t, cs, "acme", "admin@acme.test", false)
	testutil.LoginAs(t, srv, tenantAdmin, email, pw)
	policyRows := func() int { return len(e2eAuditRows(t, cs, e2epolicy.AuditActionPolicyUpdate)) }
	ceilingRows := func() int { return len(e2eAuditRows(t, cs, e2epolicy.AuditActionTenantUpdate)) }
	savePolicy := func(policy string) {
		t.Helper()
		st, raw := doReq(t, tenantAdmin, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": policy})
		require.Equal(t, http.StatusOK, st, string(raw))
		assert.Equal(t, policy, decodeE2EPolicy(t, raw).Policy, "the answer says what the policy is, written or not")
	}
	saveCeiling := func(allowed bool) {
		t.Helper()
		st, raw := doReq(t, operator, http.MethodPatch, fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, mine),
			map[string]any{"e2e_allowed": allowed})
		require.Equal(t, http.StatusOK, st, string(raw))
		var row e2eTenantWire
		require.NoError(t, json.Unmarshal(raw, &row), string(raw))
		assert.Equal(t, allowed, row.Allowed, "the answer says what the ceiling is, written or not")
	}

	savePolicy(model.E2EPolicyPermitted) // what a new tenant has
	assert.Zero(t, cs.policy.Load(), "saving the policy it already has wrote it")
	assert.Zero(t, policyRows())
	savePolicy(model.E2EPolicyApproval)
	assert.EqualValues(t, 1, cs.policy.Load(), "a change is written once")
	assert.Equal(t, 1, policyRows())
	savePolicy(model.E2EPolicyApproval)
	assert.EqualValues(t, 1, cs.policy.Load(), "saving the policy it already has wrote it")
	assert.Equal(t, 1, policyRows(), "saving the policy it already has was recorded as a change")

	saveCeiling(true) // what a new tenant has
	assert.Zero(t, cs.ceiling.Load(), "saving the ceiling it already has wrote it")
	assert.Zero(t, ceilingRows())
	saveCeiling(false)
	assert.EqualValues(t, 1, cs.ceiling.Load(), "a change is written once")
	assert.Equal(t, 1, ceilingRows())
	saveCeiling(false)
	assert.EqualValues(t, 1, cs.ceiling.Load(), "saving the ceiling it already has wrote it")
	assert.Equal(t, 1, ceilingRows(), "saving the ceiling it already has was recorded as a change")

	got, err := cs.GetProviderE2E(context.Background(), mine)
	require.NoError(t, err)
	assert.Equal(t, model.ProviderE2E{Allowed: false, Policy: model.E2EPolicyApproval}, got)
}

// A single-tenant install keeps its policy in the e2e.policy setting, and the
// three routes that write it (PATCH /api/admin/e2e and the settings API's two)
// share the rule: the policy it already has is not written, nor recorded.
func TestE2EPolicyAdmin_SavingTheSettingItAlreadyHasWritesNothing(t *testing.T) {
	srv, client, cs := e2eCountingServer(t, false)
	email, pw := testutil.SeedAdmin(t, cs)
	testutil.LoginAs(t, srv, client, email, pw)
	rows := func() int { return len(e2eAuditRows(t, cs, e2epolicy.AuditActionPolicyUpdate)) }

	routes := []struct {
		name string
		save func(policy string) (int, []byte)
	}{
		{"PATCH /api/admin/e2e", func(policy string) (int, []byte) {
			return doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": policy})
		}},
		{"PUT /api/admin/settings/e2e.policy", func(policy string) (int, []byte) {
			return doReq(t, client, http.MethodPut, srv.URL+"/api/admin/settings/"+model.SettingE2EPolicy, map[string]any{"value": policy})
		}},
		{"PATCH /api/admin/settings", func(policy string) (int, []byte) {
			return doReq(t, client, http.MethodPatch, srv.URL+"/api/admin/settings", map[string]any{model.SettingE2EPolicy: policy})
		}},
	}
	current := model.E2EPolicyPermitted // what the instance has until somebody chooses
	for i, route := range routes {
		st, raw := route.save(current)
		require.Equal(t, http.StatusOK, st, "%s: %s", route.name, raw)
		assert.EqualValues(t, i, cs.setting.Load(), "%s: saving the policy it already has wrote it", route.name)
		assert.Equal(t, i, rows(), "%s: saving the policy it already has was recorded as a change", route.name)

		next := []string{model.E2EPolicyOff, model.E2EPolicyAdmins, model.E2EPolicyApproval}[i]
		st, raw = route.save(next)
		require.Equal(t, http.StatusOK, st, "%s: %s", route.name, raw)
		assert.EqualValues(t, i+1, cs.setting.Load(), "%s: a change is written once", route.name)
		assert.Equal(t, i+1, rows(), "%s: a change is recorded once", route.name)
		assert.Equal(t, next, settingValue(t, cs, model.SettingE2EPolicy))
		current = next
	}

	// A stored value nobody can choose reads as `permitted` (the rule, and the
	// page). Saving `permitted` over it changes nothing the log could show, so
	// it writes nothing, as it is recorded as nothing.
	require.NoError(t, cs.Store.UpsertSetting(context.Background(), model.SettingE2EPolicy, "everyone"))
	written, recorded := cs.setting.Load(), rows()
	st, raw := routes[0].save(model.E2EPolicyPermitted)
	require.Equal(t, http.StatusOK, st, string(raw))
	assert.Equal(t, written, cs.setting.Load(), "a write the audit log has no row for")
	assert.Equal(t, recorded, rows())
	assert.Equal(t, "everyone", settingValue(t, cs, model.SettingE2EPolicy))
}

// A single-tenant install has no tenant ceiling: the rule reads the column in
// multi-tenant mode only. A `false` stored there would do nothing today, then
// switch encryption off for the supertenant the day multi-tenant mode is
// turned on. So the switch is refused; the table still reads.
func TestE2EPolicyAdmin_TheCeilingSwitchRefusesASingleTenantInstall(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	st, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/e2e/tenants/1", map[string]any{"e2e_allowed": false})
	require.Equal(t, http.StatusConflict, st, "%v", body)
	assert.Equal(t, "single_tenant", body["error"])
	assert.Equal(t, srvtext.Text("en", "server.e2e.ceiling.single_tenant", nil), body["message"])
	got, err := store.GetProviderE2E(context.Background(), 1)
	require.NoError(t, err)
	assert.True(t, got.Allowed, "a single-tenant install stored a ceiling nothing reads")
	assert.Empty(t, e2eAuditRows(t, store, e2epolicy.AuditActionTenantUpdate))

	st, _ = doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/e2e/tenants", nil)
	assert.Equal(t, http.StatusOK, st, "the table still reads")
}

// A tenant that could not be read is not a tenant that does not exist: only a
// missing row answers 404. A failed read answers 500, writes nothing and is
// logged with the tenant's id, so the operator is not told a tenant is gone
// when it was the database that failed.
func TestE2EPolicyAdmin_ACeilingLookupThatFailsIsNotAnUnknownTenant(t *testing.T) {
	srv, client, fs := e2eFlakyServer(t, true)
	email, pw := testutil.SeedAdmin(t, fs) // provider 1 (`default`) = supertenant
	testutil.LoginAs(t, srv, client, email, pw)
	tenantID, _, _ := seedTenant(t, fs, "acme", "admin@acme.test", false)
	logs := captureLogs(t)

	fs.failProvider.Store(tenantID)
	st, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, tenantID),
		map[string]any{"e2e_allowed": false})
	fs.failProvider.Store(0)
	require.Equal(t, http.StatusInternalServerError, st, "%v", body)

	got, err := fs.GetProviderE2E(context.Background(), tenantID)
	require.NoError(t, err)
	assert.True(t, got.Allowed, "a lookup that failed still switched the ceiling")
	assert.Empty(t, e2eAuditRows(t, fs, e2epolicy.AuditActionTenantUpdate))
	logged := logs()
	assert.Contains(t, logged, "e2e policy: could not read a tenant")
	assert.Contains(t, logged, fmt.Sprintf("tenant_id=%d", tenantID))
	assert.Contains(t, logged, "disk I/O error")
}

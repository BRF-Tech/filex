package handlers_test

// Tenant lifecycle API tests (docs/MULTI-TENANCY.md §11): CRUD over the real
// router + the supertenant guards (transfer-only flag moves, undeletable /
// undisablable supertenant, force-cascade) + the multi-tenant management gate.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestProviders_LifecycleAndGuards(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)

	do := func(method, path string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, srv.URL+path, rd)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}

	// Create a tenant.
	resp, acme := do("POST", "/api/admin/providers", map[string]any{"slug": "acme", "host": "files.acme.test"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	acmeID := int64(acme["id"].(float64))

	// Transfer the supertenant flag to a new provider — at most one must hold it.
	resp, beta := do("POST", "/api/admin/providers", map[string]any{"slug": "beta", "is_supertenant": true})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	betaID := int64(beta["id"].(float64))
	assert.True(t, beta["is_supertenant"].(bool))

	resp, list := do("GET", "/api/admin/providers", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	supers := 0
	for _, it := range list["providers"].([]any) {
		if it.(map[string]any)["is_supertenant"].(bool) {
			supers++
		}
	}
	assert.Equal(t, 1, supers, "at most one supertenant (transfer semantics)")

	// The supertenant can be neither un-flagged, disabled nor deleted.
	resp, _ = do("PATCH", "/api/admin/providers/"+itoa(betaID), map[string]any{"is_supertenant": false})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = do("PATCH", "/api/admin/providers/"+itoa(betaID), map[string]any{"enabled": false})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = do("DELETE", "/api/admin/providers/"+itoa(betaID), nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Storage link / unlink (links only — storages are never deleted here).
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "acme-files", Driver: "local", MountPath: "/",
		ConfigJSON: []byte(`{"path":"/tmp/acme"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	resp, out := do("POST", "/api/admin/providers/"+itoa(acmeID)+"/storages", map[string]any{"storage_id": st.ID})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, out["storage_ids"], 1)
	resp, out = do("DELETE", "/api/admin/providers/"+itoa(acmeID)+"/storages/"+itoa(st.ID), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, out["storage_ids"])

	// Delete with users: refused without force, cascades with it.
	u, err := store.CreateUser(context.Background(), "worker@acme.test", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(context.Background(), u.ID, acmeID, ""))
	resp, out = do("DELETE", "/api/admin/providers/"+itoa(acmeID), nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.EqualValues(t, 1, out["user_count"])
	resp, out = do("DELETE", "/api/admin/providers/"+itoa(acmeID)+"?force=1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.EqualValues(t, 1, out["deleted_users"])
	gone, err := store.GetUserByProviderEmail(context.Background(), acmeID, "worker@acme.test")
	require.NoError(t, err)
	assert.Nil(t, gone, "tenant user cascaded")
}

// TestProviders_TenantAdminForbidden — in multi-tenant mode only the
// supertenant's admins manage tenants; a tenant-admin gets 403.
func TestProviders_TenantAdminForbidden(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	h := handlers.NewProviders(store, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/admin/providers", nil)
	req = req.WithContext(tenant.WithScope(req.Context(), &tenant.Scope{ProviderID: 42}))
	h.List(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/admin/providers", nil)
	req = req.WithContext(tenant.WithScope(req.Context(), &tenant.Scope{ProviderID: 1, IsSupertenant: true}))
	h.List(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

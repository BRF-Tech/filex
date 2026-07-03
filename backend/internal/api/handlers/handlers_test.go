package handlers_test

// External _test package — exercises the handler chain via the real
// router rather than each handler in isolation, which catches middleware
// regressions.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// ---------- /healthz ----------

func TestRouter_Healthz(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

// ---------- /api/capabilities ----------

func TestRouter_Capabilities_Public(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/capabilities")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]any
	testutil.ReadJSON(t, resp, &got)
	require.Contains(t, got, "upload")
	require.Contains(t, got, "thumbs")
}

// ---------- /api/auth/login ----------

func TestRouter_Login_BadCreds(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	testutil.SeedAdmin(t, store)

	body, _ := json.Marshal(map[string]string{
		"email":    "admin@test.local",
		"password": "wrong",
	})
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRouter_Login_Good(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)

	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": pw,
	})
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Cookie should be on the jar now.
	hasCookie := false
	for _, c := range resp.Cookies() {
		if c.Name == "filex_session" && c.Value != "" {
			hasCookie = true
		}
	}
	assert.True(t, hasCookie, "expected filex_session cookie in response")
}

func TestRouter_Login_BadJSON(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", strings.NewReader("not-json"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------- /api/auth/me ----------

func TestRouter_AuthMe_Unauthenticated(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/auth/me")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRouter_AuthMe_WithCookie(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/auth/me")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// /api/auth/me wraps the user in {"user": …} (matches LoginResponse + the
	// auth store on the frontend). See commit a494633.
	var got map[string]any
	testutil.ReadJSON(t, resp, &got)
	user, ok := got["user"].(map[string]any)
	require.True(t, ok, "expected wrapped {user:…} payload, got %v", got)
	assert.Equal(t, email, user["email"])
}

// ---------- /api/auth/whoami (public) ----------

func TestRouter_WhoAmI_NoAuth(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/auth/whoami")
	require.NoError(t, err)
	defer resp.Body.Close()
	// whoami is public — returns 200 + {user: null} when not authenticated.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// ---------- /api/admin/storages ----------

func TestRouter_AdminStorages_Unauthenticated(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/admin/storages")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRouter_AdminStorages_NonAdminForbidden(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	testutil.SeedRegularUser(t, store, "joe@test.local", "JoeUserPass1!")
	testutil.LoginAs(t, srv, client, "joe@test.local", "JoeUserPass1!")

	resp, err := client.Get(srv.URL + "/api/admin/storages")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "non-admin → 403")
}

func TestRouter_AdminStorages_AdminEmptyList(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/storages")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(body))
	// The handler returns []*model.Storage; nil slice marshals as "null"
	// in Go's encoding/json. Both empty array and null are acceptable here.
	assert.True(t, bodyStr == "null" || bodyStr == "[]", "expected null or [], got %q", bodyStr)
}

func TestRouter_AdminStorages_CreateAndList(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	st := model.Storage{
		Name:          "main",
		Driver:        "local",
		MountPath:     "/data",
		ConfigJSON:    json.RawMessage(`{"root":"/tmp/filex-test"}`),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
	}
	body, _ := json.Marshal(st)
	resp, err := client.Post(srv.URL+"/api/admin/storages", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Storage
	testutil.ReadJSON(t, resp, &created)
	require.NotZero(t, created.ID)
	assert.Equal(t, "main", created.Name)

	// List again.
	resp2, err := client.Get(srv.URL + "/api/admin/storages")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var list []model.Storage
	testutil.ReadJSON(t, resp2, &list)
	require.Len(t, list, 1)
	assert.Equal(t, "main", list[0].Name)
}

// ---------- /api/files/share/{token} ----------

func TestRouter_ShareMetadata_NotFound(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/files/share/totally-not-a-real-token")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ---------- /api/auth/logout ----------

func TestRouter_Logout(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Post(srv.URL+"/api/auth/logout", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// After logout, /api/auth/me should be 401 again.
	resp2, err := client.Get(srv.URL + "/api/auth/me")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
}

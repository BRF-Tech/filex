package handlers_test

// Tests for the admin AI surface: the token-authenticated /api/ai/admin/*
// REST endpoints and the admin_* MCP tools. They run against the real router
// so the APITokenMiddleware + RequireScope("admin") chain + admin-principal
// elevation are exercised end to end, reusing the same wrapped admin handler
// logic the native /admin SPA drives.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// adminFixture spins up the full router with an in-memory store and returns
// the server + the seeded admin user id. Tokens are issued per-test so each
// can pick its own scope set.
func adminFixture(t *testing.T) (*httptest.Server, *http.Client, db.Store, int64) {
	t.Helper()

	_, store := testutil.NewTestDB(t)

	resolver := func(id int64) (storage.Driver, error) {
		return nil, fmt.Errorf("unknown id %d", id)
	}

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(context.Background(), nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}

	deps := &api.Deps{
		Cfg:             cfg,
		Store:           store,
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           share.NewService(store),
		StorageResolver: resolver,
		LocalAuth:       localDrv,
	}
	srv := httptest.NewServer(api.BuildRouter(deps))
	t.Cleanup(srv.Close)

	uid, _ := testutil.SeedAdminUser(t, store)
	return srv, &http.Client{}, store, uid
}

// ---------- REST: scope gating ----------

func TestAIAdmin_REST_RequiresAdminScope(t *testing.T) {
	srv, client, store, uid := adminFixture(t)

	// A read-only token must NOT reach the admin surface.
	readTok := issueToken(t, store, uid, "read", nil)
	req, _ := http.NewRequest("GET", srv.URL+"/api/ai/admin/users", nil)
	req.Header.Set("X-Filex-Token", readTok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "read token must be forbidden from admin surface")

	// An admin-scoped token gets through.
	adminTok := issueToken(t, store, uid, "admin", nil)
	req2, _ := http.NewRequest("GET", srv.URL+"/api/ai/admin/users", nil)
	req2.Header.Set("X-Filex-Token", adminTok)
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "admin token must reach the admin surface")
}

// ---------- REST: users CRUD via the AI admin surface ----------

func TestAIAdmin_REST_UsersCreateAndList(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	tok := issueToken(t, store, uid, "admin", nil)

	do := func(method, path string, body any) *http.Response {
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, srv.URL+path, rdr)
		req.Header.Set("X-Filex-Token", tok)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		return resp
	}

	// Create a new user through /api/ai/admin/users.
	resp := do("POST", "/api/ai/admin/users", map[string]any{
		"email":    "newbie@test.local",
		"password": "Sup3rSecret!",
		"role":     "user",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created map[string]any
	testutil.ReadJSON(t, resp, &created)
	resp.Body.Close()
	assert.Equal(t, "newbie@test.local", created["email"])

	// List shows both the seeded admin and the new user.
	resp = do("GET", "/api/ai/admin/users", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var users []map[string]any
	testutil.ReadJSON(t, resp, &users)
	resp.Body.Close()
	assert.GreaterOrEqual(t, len(users), 2)
}

func TestAIAdmin_REST_Dashboard(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	tok := issueToken(t, store, uid, "admin", nil)

	req, _ := http.NewRequest("GET", srv.URL+"/api/ai/admin/dashboard", nil)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var dash map[string]any
	testutil.ReadJSON(t, resp, &dash)
	_, hasStorages := dash["storages"]
	assert.True(t, hasStorages, "dashboard payload must include storages")
}

// ---------- token issuance: scope allow-list ----------

func TestAIAdminTokens_ScopeValidation(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	// Unknown scope is rejected at issuance.
	bad, _ := json.Marshal(map[string]any{"label": "x", "scopes": "read,bogus"})
	resp, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json", strings.NewReader(string(bad)))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "unknown scope must be rejected")

	// The new `admin` scope is accepted.
	good, _ := json.Marshal(map[string]any{"label": "x", "scopes": "admin,mcp"})
	resp2, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json", strings.NewReader(string(good)))
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusCreated, resp2.StatusCode, "admin scope must be accepted")
}

// ---------- MCP: admin tools gated by the admin scope ----------

func mcpPost(t *testing.T, client *http.Client, url, tok, payload string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(payload))
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAIAdmin_MCP_ToolsGatedByScope(t *testing.T) {
	srv, client, store, uid := adminFixture(t)

	// mcp-only token: admin tools must NOT be advertised.
	mcpTok := issueToken(t, store, uid, "mcp", nil)
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", mcpTok, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "file_list", "file tools always present")
	assert.NotContains(t, body, "admin_users_list", "admin tools hidden without admin scope")

	// mcp+admin token: admin tools ARE advertised.
	adminTok := issueToken(t, store, uid, "mcp,admin", nil)
	code, body = mcpPost(t, client, srv.URL+"/api/ai/mcp", adminTok, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, body)
	for _, name := range []string{"admin_users_list", "admin_dashboard", "admin_storages_list", "admin_settings_get", "admin_audit_list"} {
		assert.Contains(t, body, name, "admin scope should advertise %s", name)
	}
}

func TestAIAdmin_MCP_CallAdminUsersList(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	adminTok := issueToken(t, store, uid, "mcp,admin", nil)

	payload := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"admin_users_list","arguments":{}}}`
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", adminTok, payload)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, `"isError":true`, "admin_users_list should succeed: %s", body)
	// The wrapped handler's user list (the seeded admin) flows back in the result.
	assert.Contains(t, body, "admin2@test.local", "result should contain the seeded admin email")
}

func TestAIAdmin_MCP_CreateUserViaTool(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	adminTok := issueToken(t, store, uid, "mcp,admin", nil)

	payload := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"admin_users_create","arguments":{"body":{"email":"viatool@test.local","password":"T00lMadeP@ss","role":"user"}}}}`
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", adminTok, payload)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, `"isError":true`, "admin_users_create should succeed: %s", body)

	// Confirm the user really landed in the store.
	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	found := false
	for _, u := range users {
		if u.Email == "viatool@test.local" {
			found = true
		}
	}
	assert.True(t, found, "user created via MCP tool must persist")
}

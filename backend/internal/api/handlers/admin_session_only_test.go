package handlers_test

// An API key may manage ordinary accounts; it may not mint an ADMINISTRATOR's
// credential. Creating an administrator, promoting someone to administrator,
// changing an administrator's role, and setting or resetting an
// administrator's password need an administrator signed in to the admin panel
// — on /api/admin, on /api/ai/admin and in the admin_users_* MCP tools alike,
// because all three reach the same handlers.
//
// Why: what an admin-scoped key may not do itself (install a plugin, approve an
// install request) is decided by a person in a session. A key that could create
// an administrator with a password it chose, or reset one and read the new
// password back, could sign that account in and hold such a session.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// adminCall sends one JSON request with a token (no cookie) or, token == "",
// with the fixture's administrator session.
func (f *tokenGateFixture) adminCall(t *testing.T, token, method, path, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c := f.session
	if token != "" {
		req.Header.Set("X-Filex-Token", token)
		c = &http.Client{}
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *tokenGateFixture) roleOf(t *testing.T, id int64) string {
	t.Helper()
	u, err := f.store.GetUser(context.Background(), id)
	require.NoError(t, err)
	return u.Role
}

func refusedWithoutSession(t *testing.T, code int, body, what string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, code, "%s: an API key must be refused; body: %s", what, body)
	assert.Contains(t, body, "session_required", "%s: the refusal says why", what)
	assert.NotContains(t, body, "new_password", "%s: no password may come back", what)
}

func TestAdminAccounts_AKeyCannotMintAnAdministratorsCredential(t *testing.T) {
	f := newTokenGateFixture(t)
	ctx := context.Background()
	key := testutil.NewAPIToken(t, f.store, f.adminID, "read,write,delete,mcp,admin")
	member := strconv.FormatInt(f.userID, 10)
	admin := strconv.FormatInt(f.adminID, 10)

	// A second administrator, made by the session, to be acted on.
	code, body := f.adminCall(t, "", http.MethodPost, "/api/admin/users",
		`{"email":"second-admin@test.local","password":"SecondAdm!n1","role":"admin"}`)
	require.Equal(t, http.StatusOK, code, "session creates an administrator; body: %s", body)
	second, err := f.store.GetUserByEmail(ctx, "second-admin@test.local")
	require.NoError(t, err)
	secondID := strconv.FormatInt(second.ID, 10)

	for _, base := range []string{"/api/admin", "/api/ai/admin"} {
		t.Run(base, func(t *testing.T) {
			email := strings.TrimPrefix(strings.ReplaceAll(base, "/", "-"), "-") + "-made@test.local"
			code, body := f.adminCall(t, key, http.MethodPost, base+"/users",
				`{"email":"`+email+`","password":"KeyMadeAdm!n1","role":"admin"}`)
			refusedWithoutSession(t, code, body, "create an administrator")
			_, err := f.store.GetUserByEmail(ctx, email)
			assert.Error(t, err, "no account may have been created")

			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+member, `{"role":"admin"}`)
			refusedWithoutSession(t, code, body, "promote to administrator")
			assert.Equal(t, model.RoleUser, f.roleOf(t, f.userID))

			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+secondID, `{"role":"user"}`)
			refusedWithoutSession(t, code, body, "change an administrator's role")
			assert.Equal(t, model.RoleAdmin, f.roleOf(t, second.ID))

			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+secondID, `{"password":"KeyChosen!Pw1"}`)
			refusedWithoutSession(t, code, body, "set an administrator's password")

			code, body = f.adminCall(t, key, http.MethodPost, base+"/users/"+admin+"/reset-password", "")
			refusedWithoutSession(t, code, body, "reset an administrator's password")

			// Ordinary accounts stay manageable with a key.
			plain := strings.Replace(email, "-made@", "-plain@", 1)
			code, body = f.adminCall(t, key, http.MethodPost, base+"/users",
				`{"email":"`+plain+`","password":"PlainUser!1","role":"user"}`)
			assert.Equal(t, http.StatusOK, code, "a key creates an ordinary account; body: %s", body)
			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+member, `{"display_name":"Üye"}`)
			assert.Equal(t, http.StatusOK, code, "a key edits an ordinary account; body: %s", body)
			code, body = f.adminCall(t, key, http.MethodPost, base+"/users/"+member+"/reset-password", "")
			assert.Equal(t, http.StatusOK, code, "a key resets an ordinary account's password; body: %s", body)
			// An administrator's harmless fields stay open too.
			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+secondID, `{"display_name":"İkinci","role":"admin"}`)
			assert.Equal(t, http.StatusOK, code, "an unchanged role is not a role change; body: %s", body)
		})
	}

	// The same handlers, reached as MCP tools.
	tool := func(name, args string) string {
		code, body := mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", key,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		require.Equal(t, http.StatusOK, code, body)
		return body
	}
	body = tool("admin_users_create", `{"body":{"email":"mcp-made@test.local","password":"McpMadeAdm!n1","role":"admin"}}`)
	assert.Contains(t, body, "session_required", "admin_users_create an administrator; body: %s", body)
	_, err = f.store.GetUserByEmail(ctx, "mcp-made@test.local")
	assert.Error(t, err, "no account may have been created through MCP")
	body = tool("admin_users_update", `{"id":`+member+`,"body":{"role":"admin"}}`)
	assert.Contains(t, body, "session_required", "admin_users_update promote; body: %s", body)
	assert.Equal(t, model.RoleUser, f.roleOf(t, f.userID))
	body = tool("admin_users_reset_password", `{"id":`+admin+`}`)
	assert.Contains(t, body, "session_required", "admin_users_reset_password an administrator; body: %s", body)
	assert.NotContains(t, body, "new_password")

	// The administrator's session keeps every one of them.
	code, body = f.adminCall(t, "", http.MethodPatch, "/api/admin/users/"+member, `{"role":"admin"}`)
	assert.Equal(t, http.StatusOK, code, "session promotes; body: %s", body)
	code, body = f.adminCall(t, "", http.MethodPost, "/api/admin/users/"+secondID+"/reset-password", "")
	require.Equal(t, http.StatusOK, code, "session resets an administrator's password; body: %s", body)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	assert.NotEmpty(t, out["new_password"])
}

// TestAdminRights_AKeyCannotHandOutAdministration — the per-user permissions
// (PR #75) add four more doors that make an account an administrator or a
// delegated one: a person's role, a person's exceptions, a custom role's list
// and the built-in roles' defaults. Each is session-only when it GRANTS
// administration (#120); taking it away, and everything that grants no admin
// area, stays open to a key.
func TestAdminRights_AKeyCannotHandOutAdministration(t *testing.T) {
	f := newTokenGateFixture(t)
	key := testutil.NewAPIToken(t, f.store, f.adminID, "read,write,delete,mcp,admin")
	member := strconv.FormatInt(f.userID, 10)

	// A person's role → administrator.
	code, body := f.adminCall(t, key, http.MethodPut, "/api/admin/users/"+member+"/roles", `{"role":"admin"}`)
	refusedWithoutSession(t, code, body, "key: PUT roles admin")
	assert.Equal(t, model.RoleUser, f.roleOf(t, f.userID), "the member must not have become an administrator")

	// A person's exceptions allowing an admin area.
	code, body = f.adminCall(t, key, http.MethodPut, "/api/admin/users/"+member+"/exceptions", `{"overrides":{"admin.users":"allow"}}`)
	refusedWithoutSession(t, code, body, "key: exceptions allow admin.users")
	// …while a plain exception is still a key's to set.
	code, body = f.adminCall(t, key, http.MethodPut, "/api/admin/users/"+member+"/exceptions", `{"overrides":{"files.delete":"deny"}}`)
	assert.Equal(t, http.StatusOK, code, "key: a non-admin exception; body: %s", body)

	// A custom role that allows an admin area.
	code, body = f.adminCall(t, key, http.MethodPost, "/api/admin/roles", `{"name":"Yardım masası","enabled":true,"permissions":["files.download","admin.users"]}`)
	refusedWithoutSession(t, code, body, "key: create a role with admin.users")
	code, body = f.adminCall(t, key, http.MethodPost, "/api/admin/roles", `{"name":"Okur","enabled":true,"permissions":["files.download"]}`)
	require.Equal(t, http.StatusCreated, code, "key: a role without admin areas; body: %s", body)
	var created struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	rid := strconv.FormatInt(created.ID, 10)
	code, body = f.adminCall(t, key, http.MethodPut, "/api/admin/roles/"+rid, `{"name":"Okur","enabled":true,"permissions":["files.download","admin.audit"]}`)
	refusedWithoutSession(t, code, body, "key: add admin.audit to a role")

	// The built-in roles' defaults with an admin area in them.
	code, body = f.adminCall(t, key, http.MethodPut, "/api/admin/roles/builtin?role=user", `{"permissions":["files.download","admin.monitor"]}`)
	refusedWithoutSession(t, code, body, "key: built-in defaults with admin.monitor")

	// The same doors from the admin panel's session all work.
	code, body = f.adminCall(t, "", http.MethodPut, "/api/admin/roles/"+rid, `{"name":"Okur","enabled":true,"permissions":["files.download","admin.audit"]}`)
	assert.Equal(t, http.StatusOK, code, "session: add admin.audit to a role; body: %s", body)
	code, body = f.adminCall(t, "", http.MethodPut, "/api/admin/users/"+member+"/exceptions", `{"overrides":{"admin.users":"allow"}}`)
	assert.Equal(t, http.StatusOK, code, "session: exceptions allow admin.users; body: %s", body)
}

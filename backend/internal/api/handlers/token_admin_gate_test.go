package handlers_test

// A request authenticated by an API token is limited to what THAT TOKEN grants,
// whatever the account behind it could do.
//
// The admin routes (`/api/admin/*` and `/metrics`) used to ask only one
// question — "is the account an administrator" — so a narrowly scoped token
// minted on an administrator's account (read-only, or confined to one folder
// with `root:`) answered 200 there exactly like the administrator's own
// session. Scopes existed and were checked on `/api/ai`, and nowhere on the
// panel's own routes.
//
// ⚠ The harness registers the always-on api-token driver exactly the way
// internal/server does. testutil.NewTestServer installs the local driver only,
// and on that chain a token never authenticates on the admin group at all —
// which is how a suite can be green over this for as long as it was.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// useProductionAuthChain appends the api-token driver to the session driver
// chain the fixture installed, in the position internal/server puts it (last),
// and restores the previous chain when the test ends.
func useProductionAuthChain(t *testing.T, store db.Store) {
	t.Helper()
	prev := auth.Enabled()
	drv := apitoken.New(store)
	require.NoError(t, drv.Init(context.Background(), nil))
	auth.SetEnabled(append(append([]auth.Driver{}, prev...), drv))
	t.Cleanup(func() { auth.SetEnabled(prev) })
}

// adminGateRoutes are the account-wide operator surfaces a token must carry
// the admin scope to reach.
var adminGateRoutes = []string{"/metrics", "/api/admin/dashboard", "/api/admin/users/"}

// tokenGateFixture is one instance with an administrator (session + tokens)
// and an ordinary account.
type tokenGateFixture struct {
	srv     *httptest.Server
	session *http.Client
	store   db.Store
	adminID int64
	userID  int64
}

func newTokenGateFixture(t *testing.T) *tokenGateFixture {
	t.Helper()
	srv, client, store := testutil.NewTestServer(t)
	useProductionAuthChain(t, store)

	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	admin, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)

	testutil.SeedRegularUser(t, store, "member@test.local", "MemberPass!1")
	member, err := store.GetUserByEmail(context.Background(), "member@test.local")
	require.NoError(t, err)

	return &tokenGateFixture{srv: srv, session: client, store: store, adminID: admin.ID, userID: member.ID}
}

// statusWithToken performs one request carrying only the token — no cookie jar,
// so nothing can fall through to a session.
func statusWithToken(t *testing.T, method, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// TestAdminRoutes_TokenIsLimitedToItsOwnScopes — the rule, route by route.
func TestAdminRoutes_TokenIsLimitedToItsOwnScopes(t *testing.T) {
	f := newTokenGateFixture(t)

	refused := map[string]string{
		"read-scoped token on an admin account":           testutil.NewAPIToken(t, f.store, f.adminID, "read"),
		"read,write,delete,mcp token on an admin account": testutil.NewAPIToken(t, f.store, f.adminID, "read,write,delete,mcp"),
		"folder-confined token on an admin account":       testutil.NewAPIToken(t, f.store, f.adminID, "read,write,root:main://projects/acme"),
		"admin-scoped but folder-confined token":          testutil.NewAPIToken(t, f.store, f.adminID, "admin,root:main://projects/acme"),
		"admin-scoped token on a non-admin account":       testutil.NewAPIToken(t, f.store, f.userID, "admin"),
		"confinement-only token on an admin account":      testutil.NewAPIToken(t, f.store, f.adminID, "root:main://projects/acme"),
		"malformed confinement scope on an admin account": testutil.NewAPIToken(t, f.store, f.adminID, "admin,root:projects"),
		"empty-scope token on a non-admin account":        testutil.NewAPIToken(t, f.store, f.userID, ""),
		// ⚠⚠ An empty list grants NOTHING since v0.43.0 — admin least of all.
		// It used to grant every scope, and a token minted on the admin screen
		// with nothing ticked read /api/ai/admin/users (release-candidate sweep,
		// 2026-09-21). No door issues one now, migration 00054 wrote the old
		// ones out explicitly, and a row that is still empty fails closed.
		"empty-scope token on an admin account": testutil.NewAPIToken(t, f.store, f.adminID, ""),
	}
	allowed := map[string]string{
		"admin-scoped token on an admin account": testutil.NewAPIToken(t, f.store, f.adminID, "admin"),
		"admin among other scopes":               testutil.NewAPIToken(t, f.store, f.adminID, "read,write,admin"),
		// What migration 00054 turns a pre-0.43 empty-scope token into.
		"the explicit full list on an admin account": testutil.NewAPIToken(t, f.store, f.adminID, "read,write,delete,mcp,admin"),
	}

	for _, route := range adminGateRoutes {
		url := f.srv.URL + route
		t.Run(route, func(t *testing.T) {
			st, _ := statusWithToken(t, http.MethodGet, url, "")
			assert.Equal(t, http.StatusUnauthorized, st, "no credential at all")

			for name, tok := range refused {
				st, body := statusWithToken(t, http.MethodGet, url, tok)
				assert.Equal(t, http.StatusForbidden, st, "%s must be refused; body: %s", name, body)
				assert.NotContains(t, body, "admin@test.local", "%s: nothing from the admin surface may come back", name)
			}
			for name, tok := range allowed {
				st, body := statusWithToken(t, http.MethodGet, url, tok)
				assert.Equal(t, http.StatusOK, st, "%s must pass; body: %s", name, body)
			}

			// A signed-in administrator's session is unchanged.
			resp, err := f.session.Get(url)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode, "administrator session")
		})
	}
}

// TestAdminRoutes_TokenWinsOverSessionCookie — when a request carries both a
// session cookie and a token, it is judged as the token. A narrower credential
// sent alongside a wider one must not be upgraded by it.
func TestAdminRoutes_TokenWinsOverSessionCookie(t *testing.T) {
	f := newTokenGateFixture(t)
	readTok := testutil.NewAPIToken(t, f.store, f.adminID, "read")

	req, err := http.NewRequest(http.MethodGet, f.srv.URL+"/api/admin/dashboard", nil)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", readTok)
	resp, err := f.session.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestAIAdminSurface_ConfinedTokenIsRefused — the token-only admin surface
// already required the admin scope. A token confined to one folder is not an
// account-wide operator credential either, on the REST routes or as MCP tools.
func TestAIAdminSurface_ConfinedTokenIsRefused(t *testing.T) {
	f := newTokenGateFixture(t)

	confined := testutil.NewAPIToken(t, f.store, f.adminID, "admin,mcp,root:main://projects/acme")
	st, body := statusWithToken(t, http.MethodGet, f.srv.URL+"/api/ai/admin/users", confined)
	assert.Equal(t, http.StatusForbidden, st, "confined admin-scoped token on /api/ai/admin; body: %s", body)
	assert.NotContains(t, body, "admin@test.local")

	code, list := mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", confined,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, list)
	assert.Contains(t, list, "file_list", "a confined token keeps its file tools")
	assert.NotContains(t, list, "admin_users_list", "a confined token is offered no admin tools")

	code, call := mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", confined,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"admin_users_list","arguments":{}}}`)
	assert.NotContains(t, call, "admin@test.local", "status %d body %s", code, call)

	// The unconfined admin-scoped token keeps both.
	full := testutil.NewAPIToken(t, f.store, f.adminID, "admin,mcp")
	st, body = statusWithToken(t, http.MethodGet, f.srv.URL+"/api/ai/admin/users", full)
	assert.Equal(t, http.StatusOK, st, body)
	code, list = mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", full,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, list)
	assert.Contains(t, list, "admin_users_list")
}

// TestAdminTokens_ConfinementScopeMustNameAStorage — a `root:` scope that
// names no storage cannot be enforced as a confinement, so it is not issued.
func TestAdminTokens_ConfinementScopeMustNameAStorage(t *testing.T) {
	f := newTokenGateFixture(t)
	for _, scopes := range []string{"read,root:projects", "root:/projects/acme", "read,root:://x"} {
		req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/ai-tokens",
			strings.NewReader(`{"label":"x","scopes":"`+scopes+`"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := f.session.Do(req)
		require.NoError(t, err)
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "scopes %q: %s", scopes, string(b))
	}
	// A well-formed one is still accepted.
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/ai-tokens",
		strings.NewReader(`{"label":"x","scopes":"read,root:main://projects/acme"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.session.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

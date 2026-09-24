package handlers_test

// Every door that mints an API token refuses a token without scopes, in the
// reader's words — owner's decision, v0.43.0. Until then the admin screen
// minted one with nothing ticked, and it read /api/ai/admin/users (empty
// meant every scope, admin included; release-candidate sweep, 2026-09-21).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestTokenDoors_NoTokenWithoutScopes(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	admin, err := store.GetUserByEmail(t.Context(), email)
	require.NoError(t, err)
	// The reader's language is the ACCOUNT's (langOf), then Accept-Language.
	setLang := func(l string) { require.NoError(t, store.UpdateUserLocale(t.Context(), admin.ID, l, "")) }
	setLang("tr")

	post := func(path, body, lang string) (int, map[string]string) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", lang)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out := map[string]string{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	for _, door := range []string{"/api/admin/ai-tokens", "/api/tokens"} {
		for _, body := range []string{`{"label":"x"}`, `{"label":"x","scopes":""}`, `{"label":"x","scopes":" , "}`, `{"label":"x","scopes":"root:main://docs"}`} {
			code, out := post(door, body, "tr")
			assert.Equal(t, http.StatusBadRequest, code, "%s %s: a token without scopes was minted", door, body)
			assert.Equal(t, "scopes_required", out["error"], "%s %s", door, body)
			assert.Contains(t, out["message"], "En az bir izin seçin", "%s: the refusal is said in the reader's language", door)
		}
	}
	setLang("en")
	code, out := post("/api/admin/ai-tokens", `{"label":"x"}`, "en")
	require.Equal(t, http.StatusBadRequest, code)
	// ⚠ "permission", not "scope": one word for the boxes, on every surface
	// that shows them (docs/CONTRIBUTING.md → Words, v0.43.0).
	assert.Contains(t, out["message"], "Choose at least one permission")

	// Named: fine, and admin is there only because it was named.
	code, _ = post("/api/admin/ai-tokens", `{"label":"ops","scopes":"read,mcp"}`, "en")
	require.Equal(t, http.StatusCreated, code)
	code, _ = post("/api/admin/ai-tokens", `{"label":"root","scopes":"admin"}`, "en")
	require.Equal(t, http.StatusCreated, code)
	var toks []*model.APIToken
	toks, err = store.ListAPITokens(t.Context())
	require.NoError(t, err)
	byLabel := map[string]*model.APIToken{}
	for _, tk := range toks {
		byLabel[tk.Label] = tk
	}
	require.NotNil(t, byLabel["ops"])
	assert.Equal(t, "read,mcp", byLabel["ops"].Scopes)
	assert.False(t, byLabel["ops"].HasScope("admin"), "admin is never implied")
	assert.True(t, byLabel["root"].HasScope("admin"))
	for _, tk := range toks {
		assert.NotEmpty(t, tk.Scopes, "a token with no scopes was stored: %q", tk.Label)
	}
}

// A token whose row is empty anyway (written by hand, or by a version before
// the rule) fails CLOSED on every surface: file routes and the admin surface.
func TestTokenDoors_AnEmptyRowGrantsNothing(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	uid, _ := testutil.SeedAdminUser(t, store)
	tok := testutil.NewAPIToken(t, store, uid, "")
	for _, path := range []string{"/api/ai/files?path=", "/api/ai/admin/users"} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("X-Filex-Token", tok)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s: an empty-scope token got through", path)
	}
}

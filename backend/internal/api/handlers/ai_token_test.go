package handlers_test

// Tests for the AI token lifecycle: admin issuance (/api/admin/ai-tokens),
// the X-Filex-Token / Bearer auth middleware, and per-scope gating on the
// /api/ai namespace. These run against the real router so the middleware
// chain is exercised end to end.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// issueToken seeds an api_tokens row directly in the store and returns the
// plaintext value. Bypasses the admin HTTP route so scope/auth tests don't
// depend on it.
func issueToken(t *testing.T, store db.Store, userID int64, scopes string, expires *time.Time) string {
	t.Helper()
	plain := "tok_" + randToken(t)
	_, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    userID,
		Label:     "test",
		TokenHash: apitoken.HashToken(plain),
		Scopes:    scopes,
		ExpiresAt: expires,
	})
	require.NoError(t, err)
	return plain
}

func randToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// ---------- token usernames ----------

// TestAIToken_UsernameGate exercises the X-Filex-Token-User gate on the real
// middleware chain: empty → default (200), allow-listed → 200, unknown → 403.
func TestAIToken_UsernameGate(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, users)
	plain := "tok_" + randToken(t)
	_, err = store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID:    users[0].ID,
		Label:     "paylasilan",
		TokenHash: apitoken.HashToken(plain),
		Usernames: "work,fishapp",
	})
	require.NoError(t, err)

	call := func(tokenUser string) int {
		req, rerr := http.NewRequest(http.MethodGet, srv.URL+"/api/ai/root", nil)
		require.NoError(t, rerr)
		req.Header.Set("X-Filex-Token", plain)
		if tokenUser != "" {
			req.Header.Set("X-Filex-Token-User", tokenUser)
		}
		resp, derr := client.Do(req)
		require.NoError(t, derr)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusOK, call(""), "empty → default username")
	assert.Equal(t, http.StatusOK, call("fishapp"), "allow-listed username")
	assert.Equal(t, http.StatusForbidden, call("saldirgan"), "unknown username must 403, not blend into default")
}

// TestAITokens_UpdateUsernames covers the new PATCH surface: create with a
// list, edit it, and see the change reflected in the admin listing.
func TestAITokens_UpdateUsernames(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	body, _ := json.Marshal(map[string]any{"label": "entegrasyon", "usernames": []string{"work", "fishapp"}})
	resp, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		Row model.APIToken `json:"row"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Equal(t, "work,fishapp", created.Row.Usernames)

	idStr := strconv.FormatInt(created.Row.ID, 10)
	patch, _ := json.Marshal(map[string]any{"usernames": []string{"work", "pc-mcp"}})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/ai-tokens/"+idStr, bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	presp, err := client.Do(req)
	require.NoError(t, err)
	defer presp.Body.Close()
	require.Equal(t, http.StatusOK, presp.StatusCode)

	lresp, err := client.Get(srv.URL + "/api/admin/ai-tokens")
	require.NoError(t, err)
	defer lresp.Body.Close()
	var listed struct {
		Tokens []model.APIToken `json:"tokens"`
	}
	require.NoError(t, json.NewDecoder(lresp.Body).Decode(&listed))
	found := false
	for _, tk := range listed.Tokens {
		if tk.ID == created.Row.ID {
			found = true
			assert.Equal(t, "work,pc-mcp", tk.Usernames)
		}
	}
	require.True(t, found)

	// Invalid slugs are rejected.
	bad, _ := json.Marshal(map[string]any{"usernames": []string{"kötü ad!"}})
	breq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/ai-tokens/"+idStr, bytes.NewReader(bad))
	breq.Header.Set("Content-Type", "application/json")
	bresp, err := client.Do(breq)
	require.NoError(t, err)
	defer bresp.Body.Close()
	require.Equal(t, http.StatusBadRequest, bresp.StatusCode)
}

// ---------- admin issuance ----------

func TestAITokens_AdminCreateAndList(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	body, _ := json.Marshal(map[string]any{"label": "ci", "scopes": "read,write"})
	resp, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]any
	testutil.ReadJSON(t, resp, &created)
	plain, _ := created["token"].(string)
	require.NotEmpty(t, plain, "plaintext token returned once")
	require.Len(t, plain, 64, "expected 64-char hex token")

	// List should not leak the secret or hash.
	resp2, err := client.Get(srv.URL + "/api/admin/ai-tokens")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var list map[string]any
	testutil.ReadJSON(t, resp2, &list)
	toks, _ := list["tokens"].([]any)
	require.Len(t, toks, 1)
	row, _ := toks[0].(map[string]any)
	_, hasHash := row["token_hash"]
	assert.False(t, hasHash, "token_hash must not be serialized")
	assert.Equal(t, "read,write", row["scopes"])
}

func TestAITokens_AdminCreate_RequiresAdmin(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	testutil.SeedRegularUser(t, store, "joe@test.local", "JoeUserPass1!")
	testutil.LoginAs(t, srv, client, "joe@test.local", "JoeUserPass1!")

	resp, err := client.Post(srv.URL+"/api/admin/ai-tokens", "application/json", bytes.NewReader([]byte(`{}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ---------- token auth + scopes on /api/ai ----------

func TestAIRoutes_NoToken_401(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/ai/files")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAIRoutes_BadToken_401(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	req, _ := http.NewRequest("GET", srv.URL+"/api/ai/files", nil)
	req.Header.Set("X-Filex-Token", "not-a-real-token")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAIRoutes_ExpiredToken_401(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	u, _ := testutil.SeedAdminUser(t, store)
	past := time.Now().Add(-time.Hour)
	tok := issueToken(t, store, u, "", &past)

	req, _ := http.NewRequest("GET", srv.URL+"/api/ai/files", nil)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAIRoutes_ScopeEnforced(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	u, _ := testutil.SeedAdminUser(t, store)
	// read-only token may NOT write.
	tok := issueToken(t, store, u, "read", nil)

	req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mkdir", bytes.NewReader([]byte(`{"path":"main://x"}`)))
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "read token must be forbidden from write verb")
}

func TestAIRoutes_BearerHeaderAccepted(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	u, _ := testutil.SeedAdminUser(t, store)
	tok := issueToken(t, store, u, "read", nil)

	// Authorization: Bearer should authenticate. The test server's resolver
	// errors (no storage), so we expect 503 from the handler — NOT 401/403,
	// which proves auth+scope passed.
	req, _ := http.NewRequest("GET", srv.URL+"/api/ai/files", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

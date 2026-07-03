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

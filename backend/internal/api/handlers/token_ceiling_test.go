package handlers_test

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

	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A credential minted by a token is never wider than that token (review of
// PR #35, 2026-09-22). The self-service doors capped what they minted by the
// OWNER's rights only, so a read-only or folder-confined personal token could
// mint its owner a full one — the hole #35 closed at desktop/complete, still
// open here, and reachable from every paired desktop since #35 made those
// tokens personal ones.

func personalToken(t *testing.T, store interface {
	CreateAPIToken(context.Context, *model.APIToken) (*model.APIToken, error)
}, uid int64, scopes string, expires *time.Time) (secret string, row *model.APIToken) {
	t.Helper()
	var raw [24]byte
	_, _ = rand.Read(raw[:])
	secret = "filex_" + hex.EncodeToString(raw[:])
	row, err := store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: uid, Label: "t-" + scopes, TokenHash: apitoken.HashToken(secret),
		Scopes: scopes, Kind: model.TokenKindUser, ExpiresAt: expires,
	})
	require.NoError(t, err)
	return secret, row
}

func postAs(t *testing.T, base, tok, path string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTokenCeiling_ATokenMintsNothingWiderThanItself(t *testing.T) {
	srv, client, store := newKeyServer(t)
	const pw = "CeilPass!1"
	u := seedUser(t, store, "ceil@example.com", pw)
	// An administrator: the self-service door then checks a `root:` only
	// against the token that asks, which is what this test is about.
	require.NoError(t, store.UpdateUserRole(context.Background(), u.ID, model.RoleAdmin))
	ro, roRow := personalToken(t, store, u.ID, "read", nil)
	conf, _ := personalToken(t, store, u.ID, "read,write,delete,root:main://docs", nil)
	full, fullRow := personalToken(t, store, u.ID, "read,write,delete", nil)

	cases := []struct {
		name, tok, scopes string
		want              int
	}{
		{"read-only asks for write", ro, "read,write,delete", http.StatusForbidden},
		{"read-only asks for read", ro, "read", http.StatusCreated},
		{"confined asks for no folder", conf, "read", http.StatusForbidden},
		{"confined asks for another folder", conf, "read,root:main://other", http.StatusForbidden},
		{"confined asks for a subfolder", conf, "read,root:main://docs/sub", http.StatusCreated},
		{"full asks for what it holds", full, "read,write,delete", http.StatusCreated},
		{"full asks for a scope it lacks", full, "read,mcp", http.StatusForbidden},
	}
	for _, c := range cases {
		code, body := postAs(t, srv.URL, c.tok, "/api/tokens", map[string]any{"label": "x", "scopes": c.scopes})
		assert.Equal(t, c.want, code, "%s: %v", c.name, body)
		if c.want == http.StatusForbidden {
			assert.Equal(t, "token_ceiling", body["reason"], c.name)
		}
	}

	// S3 access keys: a narrower token's key inherits from it…
	code, body := postAs(t, srv.URL, ro, "/api/auth/s3-keys", map[string]any{"label": "rclone"})
	require.Equal(t, http.StatusCreated, code, body)
	key, _ := body["key"].(map[string]any)
	require.NotNil(t, key, body)
	assert.EqualValues(t, roRow.ID, key["api_token_id"], "a read-only token's key must carry the token, not the owner's full access")
	// …and cannot be pointed at a wider sibling token.
	code, body = postAs(t, srv.URL, ro, "/api/auth/s3-keys", map[string]any{"label": "x", "api_token_id": fullRow.ID})
	assert.Equal(t, http.StatusForbidden, code, body)
	// A token that holds everything its owner does mints as before.
	code, body = postAs(t, srv.URL, full, "/api/auth/s3-keys", map[string]any{"label": "desktop"})
	require.Equal(t, http.StatusCreated, code, body)
	key, _ = body["key"].(map[string]any)
	assert.Nil(t, key["api_token_id"])

	// NFS exports follow the same rule.
	code, body = postAs(t, srv.URL, ro, "/api/auth/nfs-exports", map[string]any{"label": "nas"})
	require.Equal(t, http.StatusCreated, code, body)
	export, _ := body["export"].(map[string]any)
	require.NotNil(t, export, body)
	assert.EqualValues(t, roRow.ID, export["api_token_id"])

	// An SSH key carries nothing to inherit: a narrower token cannot add one.
	code, body = postAs(t, srv.URL, ro, "/api/auth/ssh-keys", map[string]any{
		"key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGxHx2Q5y1ydtD8m6d8c3s8sG1V7Q2pCqF1y5G2v1Wn3 x@y",
	})
	assert.Equal(t, http.StatusForbidden, code, body)
	assert.Equal(t, "token_ceiling", body["reason"])

	// The person, signed in, is not a token.
	testutil.LoginAs(t, srv, client, "ceil@example.com", pw)
	code, body = postJSON(t, client, srv.URL+"/api/tokens", map[string]any{"label": "mine", "scopes": "read,write,delete"})
	assert.Equal(t, http.StatusCreated, code, body)
}

// A token minted by an expiring token expires with it.
func TestTokenCeiling_AnExpiringTokenMintsNothingThatOutlivesIt(t *testing.T) {
	srv, _, store := newKeyServer(t)
	u := seedUser(t, store, "exp@example.com", "ExpPass!1")
	exp := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	tok, _ := personalToken(t, store, u.ID, "read,write,delete", &exp)

	code, body := postAs(t, srv.URL, tok, "/api/tokens", map[string]any{"label": "child", "scopes": "read"})
	require.Equal(t, http.StatusCreated, code, body)
	rows, err := store.ListAPITokensByUser(context.Background(), u.ID)
	require.NoError(t, err)
	var child *model.APIToken
	for _, r := range rows {
		if r.Label == "child" {
			child = r
		}
	}
	require.NotNil(t, child)
	require.NotNil(t, child.ExpiresAt, "the child outlives the token that made it")
	assert.False(t, child.ExpiresAt.After(exp))
}

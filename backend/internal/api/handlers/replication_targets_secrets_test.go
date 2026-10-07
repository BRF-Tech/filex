package handlers_test

// A replication target's credentials never go out (#186 follow-up).
//
// ⚠ Every read of a target - the Replication page, an admin API key on
// /api/ai/admin, the admin_replication_targets_* MCP tools - answered with its
// configuration as stored: an S3 secret key, an SMB or SFTP password, a
// private key, in clear. They are masked now ("***"), and a save that sends
// the mask back keeps the stored value.

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
)

const storedSecret = "s3cr3t-never-shown"

func secretTarget(t *testing.T, f *linkFixture) *model.ReplicationTarget {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{
		"bucket": "yedek", "endpoint": "https://fsn1.your-objectstorage.com",
		"access_key": "AKIA-ID", "secret_key": storedSecret,
	})
	rt, err := f.store.CreateReplicationTarget(context.Background(), &model.ReplicationTarget{
		Name: "kova", Driver: "s3", ConfigJSON: cfg, Mode: "async", Enabled: true,
	})
	require.NoError(t, err)
	return rt
}

func storedConfig(t *testing.T, f *linkFixture, id int64) map[string]any {
	t.Helper()
	rt, err := f.store.GetReplicationTarget(context.Background(), id)
	require.NoError(t, err)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rt.ConfigJSON, &out))
	return out
}

func TestReplicationTargets_CredentialsNeverGoOut(t *testing.T) {
	f := newLinkFixture(t)
	rt := secretTarget(t, f)

	req, _ := http.NewRequest(http.MethodGet, f.srv+"/api/admin/replication-targets", nil)
	resp, err := f.client.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, string(raw), storedSecret, "the list sent the secret key")
	assert.NotContains(t, string(raw), "AKIA-ID", "the list sent the access key")
	assert.Contains(t, string(raw), "fsn1.your-objectstorage.com", "the rest of the configuration is still shown")

	code, one := f.do(t, http.MethodGet, "/api/admin/replication-targets/"+strconv.FormatInt(rt.ID, 10), nil)
	require.Equal(t, http.StatusOK, code)
	cfg, _ := one["config"].(map[string]any)
	assert.Equal(t, "***", cfg["secret_key"])
	assert.Equal(t, "yedek", cfg["bucket"])

	code, created := f.do(t, http.MethodPost, "/api/admin/replication-targets", map[string]any{
		"name": "kutu", "driver": "smb",
		"config": map[string]any{"host": "u1.your-storagebox.de", "share": "backup", "user": "u1", "password": "pw-never-shown"},
	})
	require.Equal(t, http.StatusCreated, code)
	createdCfg, _ := created["config"].(map[string]any)
	assert.Equal(t, "***", createdCfg["password"], "the create answer sent the password back")
}

func TestReplicationTargets_TheMaskSentBackKeepsTheSecret(t *testing.T) {
	f := newLinkFixture(t)
	rt := secretTarget(t, f)
	path := "/api/admin/replication-targets/" + strconv.FormatInt(rt.ID, 10)

	// What the page was shown, with the bucket changed: the secret is kept.
	_, shown := f.do(t, http.MethodGet, path, nil)
	cfg, _ := shown["config"].(map[string]any)
	cfg["bucket"] = "yedek-2"
	code, _ := f.do(t, http.MethodPatch, path, map[string]any{"config": cfg})
	require.Equal(t, http.StatusOK, code)
	stored := storedConfig(t, f, rt.ID)
	assert.Equal(t, storedSecret, stored["secret_key"], "saving what the page showed replaced the secret key with the mask")
	assert.Equal(t, "AKIA-ID", stored["access_key"])
	assert.Equal(t, "yedek-2", stored["bucket"])

	// The same keys sent to another endpoint: they have to be typed again.
	elsewhere := map[string]any{}
	for k, v := range cfg {
		elsewhere[k] = v
	}
	elsewhere["endpoint"] = "https://attacker.example"
	code, body := f.do(t, http.MethodPatch, path, map[string]any{"config": elsewhere})
	assert.Equal(t, http.StatusBadRequest, code, "the saved keys would have been sent to a new endpoint")
	assert.Equal(t, "SECRET_NEEDED", body["error"])
	assert.Equal(t, "https://fsn1.your-objectstorage.com", storedConfig(t, f, rt.ID)["endpoint"])

	// A new secret is a new secret.
	cfg["secret_key"] = "rotated"
	code, _ = f.do(t, http.MethodPatch, path, map[string]any{"config": cfg})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "rotated", storedConfig(t, f, rt.ID)["secret_key"])
}

func TestReplicationTargets_TheTokenDoorsMaskToo(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	cfg, _ := json.Marshal(map[string]any{"host": "h", "user": "u", "password": "pw-never-shown"})
	_, err := store.CreateReplicationTarget(context.Background(), &model.ReplicationTarget{
		Name: "sftp-yedek", Driver: "sftp", ConfigJSON: cfg, Mode: "async", Enabled: true,
	})
	require.NoError(t, err)

	tok := issueToken(t, store, uid, "admin", nil)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/ai/admin/replication-targets", nil)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.NotContains(t, string(raw), "pw-never-shown", "an admin API key read the password")
	assert.Contains(t, string(raw), "sftp-yedek")

	mcpTok := issueToken(t, store, uid, "mcp,admin", nil)
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", mcpTok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_replication_targets_list","arguments":{}}}`)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, strings.Contains(body, "sftp-yedek"), body)
	assert.NotContains(t, body, "pw-never-shown", "an MCP client read the password")
}

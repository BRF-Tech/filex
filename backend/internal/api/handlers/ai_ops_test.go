package handlers_test

// End-to-end tests for the AI REST surface and the MCP server against a
// real local-FS storage driver. Builds the full router so the
// APITokenMiddleware + RequireScope chain runs, then drives the documented
// JSON contract the work.example.com FilexClient depends on.

import (
	"bytes"
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
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// aiFixture spins up the full router backed by an in-memory store + a
// tmp-dir local storage named "main", and returns the test server plus a
// full-access token bound to a fresh admin user.
func aiFixture(t *testing.T) (*httptest.Server, *http.Client, db.Store, string) {
	t.Helper()

	_, store := testutil.NewTestDB(t)
	dir := t.TempDir()

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": dir}))

	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(dir, `\`, `\\`) + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
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
	tok := issueToken(t, store, uid, "", nil) // empty scopes = all

	return srv, &http.Client{}, store, tok
}

// aiReq issues an authenticated request to the AI namespace.
func aiReq(t *testing.T, client *http.Client, method, url, tok string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func TestAI_UploadReadListInfo(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	// Upload a text file.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":    "main://notes/hello.txt",
		"content": "hello ai",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var up map[string]any
	testutil.ReadJSON(t, resp, &up)
	resp.Body.Close()
	entry, _ := up["entry"].(map[string]any)
	require.NotNil(t, entry)
	assert.Equal(t, "hello.txt", entry["name"])
	assert.Equal(t, float64(len("hello ai")), entry["size"])

	// Download the bytes back.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://notes/hello.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "hello ai", string(got))

	// Info.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://notes/hello.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info map[string]any
	testutil.ReadJSON(t, resp, &info)
	resp.Body.Close()
	ie, _ := info["entry"].(map[string]any)
	assert.Equal(t, "file", ie["type"])

	// List the directory.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/files?path=main://notes", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls map[string]any
	testutil.ReadJSON(t, resp, &ls)
	resp.Body.Close()
	entries, _ := ls["entries"].([]any)
	require.Len(t, entries, 1)
}

func TestAI_MkdirMoveDelete(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	// Mkdir.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://docs"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Upload then move.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://docs/a.txt", "content": "x",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "main://docs/a.txt", "dst": "main://docs/b.txt",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// b.txt exists, a.txt gone.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/b.txt", tok, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/a.txt", tok, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Delete (soft) — afterwards info should 404.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/delete", tok, map[string]any{"path": "main://docs/b.txt"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=main://docs/b.txt", tok, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestAI_UploadBase64Binary(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	// 0xDEADBEEF is not valid UTF-8 → must round-trip via base64.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":           "main://bin.dat",
		"content_base64": "3q2+7w==", // DE AD BE EF
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://bin.dat", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, got)
}

// ---------- MCP server smoke test ----------

// TestAI_MCP_InitializeAndListTools drives the streamable HTTP endpoint with
// raw JSON-RPC to confirm the transport, auth, and tool registration work.
func TestAI_MCP_InitializeAndListTools(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	post := func(payload string) (int, string) {
		req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(payload))
		req.Header.Set("X-Filex-Token", tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// initialize
	code, body := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	require.Equal(t, http.StatusOK, code, "mcp initialize body=%s", body)
	assert.Contains(t, body, `"serverInfo"`)
	assert.Contains(t, body, `"filex"`)

	// tools/list
	code, body = post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code, "mcp tools/list body=%s", body)
	for _, name := range []string{"file_list", "file_read", "file_write", "file_delete", "file_move", "file_mkdir", "file_search", "file_info"} {
		assert.Contains(t, body, name, "tools/list should advertise %s", name)
	}
}

func TestAI_MCP_NoToken_400(t *testing.T) {
	srv, client, _, _ := aiFixture(t)
	// getServer returns nil without a valid principal → SDK serves 4xx.
	req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// APITokenMiddleware rejects with 401 before the SDK handler runs.
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestAI_MCP_CallToolWrite exercises a real tools/call → file_write round
// trip and verifies the file lands on disk via the REST download path.
func TestAI_MCP_CallToolWrite(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	payload := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"file_write","arguments":{"path":"main://mcp.txt","content":"via mcp"}}}`
	req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(payload))
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "tools/call body=%s", string(b))
	assert.NotContains(t, string(b), `"isError":true`, "write tool should succeed: %s", string(b))

	// Confirm the bytes are really there.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://mcp.txt", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "via mcp", string(got))
}

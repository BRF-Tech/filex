package handlers_test

// The file_root hint is what an agent reads first (GET /api/ai/root, the
// file_root MCP tool), so a sentence in it is a promise. Until task #116 it
// said conversion was "not a server-side MCP operation" and only a person could
// start it in the UI; the Convert app has been a server-side job since 0.48.
// Until task #114 it then said "File conversion has no MCP tool" and sent the
// agent to /api/files; 0.50 gives it the file_convert tool and its REST twin,
// POST /api/ai/convert. These tests pin the sentence and that the route it
// names is really served.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestAIRootHint_ConversionNamesTheRESTRoute(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, users)
	tok := issueToken(t, store, users[0].ID, "read,mcp", nil)

	get := func(path string) (int, string) {
		req, rerr := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		require.NoError(t, rerr)
		req.Header.Set("X-Filex-Token", tok)
		resp, derr := http.DefaultClient.Do(req)
		require.NoError(t, derr)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := get("/api/ai/root")
	require.Equal(t, http.StatusOK, code, body)
	var info struct {
		Hint string `json:"hint"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &info))

	assert.NotContains(t, info.Hint, "not a server-side",
		"conversion IS a server-side job (the Convert app); the hint must not send the agent to a person")
	assert.NotContains(t, info.Hint, "no MCP tool",
		"conversion has a tool since 0.50 (file_convert); the hint must not say it has none")
	assert.Contains(t, info.Hint, "file_convert",
		"the hint names the tool that starts a conversion")
	assert.Contains(t, info.Hint, "POST /api/ai/convert",
		"the hint names the tool's REST twin")
	assert.Contains(t, info.Hint, "op_get",
		"the hint says where the job's result is read")

	// The routes the hint names are served to a token: whatever they answer,
	// it is not the router's own "no such route".
	for _, p := range []string{"/api/ai/ops/1", "/api/ai/apps/actions?path=x"} {
		_, body := get(p)
		assert.False(t, strings.HasPrefix(body, "404 page not found"), "GET %s must be a route, got %q", p, body)
	}
}

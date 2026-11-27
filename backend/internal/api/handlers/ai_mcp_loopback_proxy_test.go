package handlers_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// filex behind a reverse proxy on the same machine (Caddy or nginx in front
// of 127.0.0.1:5212): every MCP request arrives on a loopback connection
// with the public host name in Host. The MCP SDK's DNS-rebinding guard
// refused exactly that ("Forbidden: invalid Host header", 403) after filex
// had let the key in, so a client - an OAuth one included - could not use
// MCP through such a proxy at all. The endpoint takes nothing but an API key
// in a header (auth.APITokenMiddleware), which a page that rebinds a name to
// 127.0.0.1 cannot send; a request without one is filex's 401, wherever it
// comes from. The test server listens on 127.0.0.1, as such an install does.
func TestMCPBehindALoopbackProxy(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = "https://files.example.test" })
	_, uid, _ := desktopSession(t, srv.URL, store, "user", "mcp-proxy@example.test")
	key := testutil.NewAPIToken(t, store, uid, "read,mcp")
	ask := func(host, token string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/ai/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		require.NoError(t, err)
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	for _, host := range []string{"files.example.test", "files.example.test:443"} {
		status, body := ask(host, key)
		require.Equal(t, http.StatusOK, status, "%s: %s", host, body)
		require.Contains(t, body, `"tools"`)
	}
	// Without a key, or with a wrong one, filex's own answer: 401 - from the
	// proxy's host name and from a loopback one alike.
	for _, host := range []string{"files.example.test", "127.0.0.1"} {
		status, body := ask(host, "")
		require.Equal(t, http.StatusUnauthorized, status, "%s: %s", host, body)
		status, body = ask(host, strings.Repeat("0", 64))
		require.Equal(t, http.StatusUnauthorized, status, "%s: %s", host, body)
	}
}

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The admin_* MCP tools answer {status, result} where result is the handler's
// JSON - an object for most of them.
//
// ⚠ Measured 2026-10-01: the output schema the SDK derived for `result` (a
// json.RawMessage, i.e. []byte) allowed only null or an ARRAY, and the SDK
// validates every answer against it. Every tool whose handler answers an
// object - admin_login_security_get / _update / _locks / _attempts,
// admin_dashboard, admin_settings_get, a 4xx refusal… - came back as a
// JSON-RPC error "validating tool output", although the handler had run: an
// agent was told a settings change it had just made had failed. The tests
// that drove these tools asserted only that `isError` was absent, which a
// JSON-RPC error satisfies.
func TestAIAdminMCPToolsAnswerObjects(t *testing.T) {
	e := newSecEnv(t, nil)
	u, err := e.store.GetUserByEmail(context.Background(), e.email)
	require.NoError(t, err)
	tok := issueToken(t, e.store, u.ID, "mcp,admin", nil)

	type envelope struct {
		Error  *struct{ Message string } `json:"error"`
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Status int             `json:"status"`
				Result json.RawMessage `json:"result"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	call := func(name, args string) envelope {
		code, body := mcpPost(t, http.DefaultClient, e.srv.URL+"/api/ai/mcp", tok,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		require.Equal(t, http.StatusOK, code, body)
		var env envelope
		require.NoError(t, json.Unmarshal([]byte(body), &env), body)
		require.Nil(t, env.Error, "%s answered a JSON-RPC error: %s", name, body)
		return env
	}

	for _, c := range []struct{ name, args, key string }{
		{"admin_login_security_get", `{}`, "settings"},
		{"admin_login_security_update", `{"body":{"ip_max_fails":12}}`, "settings"},
		{"admin_login_security_locks", `{}`, "items"},
		{"admin_login_security_attempts", `{}`, "items"},
		{"admin_dashboard", `{}`, "storages"},
		{"admin_settings_get", `{}`, "login.ip_max_fails"},
	} {
		env := call(c.name, c.args)
		require.False(t, env.Result.IsError, c.name)
		require.Equal(t, http.StatusOK, env.Result.StructuredContent.Status, c.name)
		var obj map[string]any
		require.NoError(t, json.Unmarshal(env.Result.StructuredContent.Result, &obj), "%s: %s", c.name, env.Result.StructuredContent.Result)
		require.Contains(t, obj, c.key, c.name)
	}

	// A refusal is a tool error carrying the handler's answer - not a
	// protocol error.
	env := call("admin_login_security_update", `{"body":{"ip_max_fails":0}}`)
	require.True(t, env.Result.IsError)
	require.Equal(t, http.StatusBadRequest, env.Result.StructuredContent.Status)
	require.Contains(t, string(env.Result.StructuredContent.Result), "invalid_setting")

	// A list answer still comes back as a list.
	env = call("admin_users_list", `{}`)
	require.False(t, env.Result.IsError)
	require.Equal(t, byte('['), env.Result.StructuredContent.Result[0])
}

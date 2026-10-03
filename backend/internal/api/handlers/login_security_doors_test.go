package handlers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/loginguard"
)

// The sign-in trail (GET /api/admin/login-security/attempts, the "Recent
// sign-in events" table) tells the whole story of the limit, whichever door an
// administrator came in by: the panel (a session), an admin API key
// (/api/ai/admin) or an MCP tool. Each unlock and each settings change is ONE
// row there - and one row in the audit log.
//
// ⚠ Measured before the fix (2026-10-01): through an API key the unlock was
// filed as `ai.login.unlocked` and the change as `ai.login_security.update` -
// outside the trail's `login.` filter - AND written twice (the /api/ai audit
// middleware and the /api/ai/admin one both recorded the request, the second
// row empty). A session's settings change (`login_security.update`) was not in
// the trail at all.

// tokenCall sends one request to the admin API with an API key.
func tokenCall(t *testing.T, base, tok, method, path, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		t.Logf("%s %s -> %d %s", method, path, resp.StatusCode, b)
	}
	return resp.StatusCode
}

// mcpTool calls one MCP tool and fails the test when it did not succeed.
func mcpTool(t *testing.T, base, tok, name, args string) string {
	t.Helper()
	code, body := mcpPost(t, http.DefaultClient, base+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	require.Equal(t, http.StatusOK, code, body)
	require.NotContains(t, body, `"isError":true`, "%s: %s", name, body)
	// A JSON-RPC error has no `isError` either (TestAIAdminMCPToolsAnswerObjects).
	require.NotContains(t, body, `"error":{`, "%s answered a protocol error: %s", name, body)
	return body
}

type trailItem struct {
	Action        string   `json:"action"`
	Identifier    string   `json:"identifier"`
	Reason        string   `json:"reason"`
	Scope         string   `json:"scope"`
	Via           string   `json:"via"`
	ChangedFields []string `json:"changed_fields"`
}

func (e *secEnv) trail(t *testing.T, query string) ([]trailItem, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/api/admin/login-security/attempts?limit=500"+query, nil)
	require.NoError(t, err)
	resp, err := e.admin.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Items []trailItem `json:"items"`
		Total int         `json:"total"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Items, out.Total
}

func TestLoginSecurityTrailShowsEveryDoorOnce(t *testing.T) {
	e := newSecEnv(t, nil)
	ctx := context.Background()
	admin, err := e.store.GetUserByEmail(ctx, e.email)
	require.NoError(t, err)
	tok := issueToken(t, e.store, admin.ID, "mcp,admin", nil)
	base := e.srv.URL

	// Three locked accounts, one for each door to lift.
	e.wrong(t, "203.0.113.1", "panel@example.com", 5)
	e.wrong(t, "203.0.113.2", "key@example.com", 5)
	e.wrong(t, "203.0.113.3", "mcp@example.com", 5)

	// The panel (a session).
	status, _ := e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "account", "subject": "panel@example.com"})
	require.Equal(t, http.StatusOK, status)
	status, _ = e.do(t, http.MethodPatch, "", map[string]any{"account_max_fails": 6})
	require.Equal(t, http.StatusOK, status)

	// An admin API key.
	require.Equal(t, http.StatusOK, tokenCall(t, base, tok, http.MethodPost, "/api/ai/admin/login-security/unlock",
		`{"scope":"account","subject":"key@example.com"}`))
	require.Equal(t, http.StatusOK, tokenCall(t, base, tok, http.MethodPatch, "/api/ai/admin/login-security",
		`{"ip_max_fails":11}`))

	// An MCP tool.
	mcpTool(t, base, tok, "admin_login_security_unlock", `{"body":{"scope":"account","subject":"mcp@example.com"}}`)
	mcpTool(t, base, tok, "admin_login_security_update", `{"body":{"window_seconds":700}}`)

	items, total := e.trail(t, "")
	require.Equal(t, len(items), total)
	unlocks := map[string][]string{}
	changes := map[string][]string{}
	for _, it := range items {
		switch it.Action {
		case loginguard.ActionUnlocked:
			if it.Reason == "admin" {
				unlocks[it.Identifier] = append(unlocks[it.Identifier], it.Via)
			}
		case "login_security.update":
			require.Len(t, it.ChangedFields, 1, "%+v", it)
			changes[it.ChangedFields[0]] = append(changes[it.ChangedFields[0]], it.Via)
		default:
			require.True(t, strings.HasPrefix(it.Action, "login."), "only the sign-in story is in the trail: %q", it.Action)
		}
	}
	require.Equal(t, map[string][]string{
		"panel@example.com": {"panel"},
		"key@example.com":   {"api"},
		"mcp@example.com":   {"mcp"},
	}, unlocks, "each unlock exactly once, with the door it came through")
	require.Equal(t, map[string][]string{
		"account_max_fails": {"panel"},
		"ip_max_fails":      {"api"},
		"window_seconds":    {"mcp"},
	}, changes, "each settings change exactly once, with the door it came through")

	// The event filter finds them whatever the door.
	only, n := e.trail(t, "&action=login.unlocked")
	require.Equal(t, n, len(only))
	admins := 0
	for _, it := range only {
		require.Equal(t, loginguard.ActionUnlocked, it.Action)
		if it.Reason == "admin" {
			admins++
		}
	}
	require.Equal(t, 3, admins)
	only, n = e.trail(t, "&action=login_security.update")
	require.Equal(t, 3, n)
	require.Len(t, only, 3)

	// …and the audit log holds one row per event, not two.
	rows, err := e.store.ListAuditRecent(ctx, 500)
	require.NoError(t, err)
	perAction := map[string]int{}
	for _, r := range rows {
		if strings.Contains(r.Action, "login") && !strings.HasSuffix(r.Action, ".failed") && !strings.HasSuffix(r.Action, ".locked") {
			if r.Action == loginguard.ActionUnlocked && r.Metadata["reason"] != "admin" {
				continue
			}
			perAction[r.Action]++
		}
	}
	require.Equal(t, map[string]int{loginguard.ActionUnlocked: 3, "login_security.update": 3}, perAction,
		"no `ai.`-prefixed twin and no empty second row")
}

// A sign-in setting written through the generic settings API - the Settings
// endpoints, `admin_settings_set` - is a sign-in security change like any
// other, and the trail says so once.
func TestLoginSecurityTrailShowsAGenericSettingsWrite(t *testing.T) {
	e := newSecEnv(t, nil)
	ctx := context.Background()
	admin, err := e.store.GetUserByEmail(ctx, e.email)
	require.NoError(t, err)
	tok := issueToken(t, e.store, admin.ID, "mcp,admin", nil)

	status, body := doJSON(t, e.admin, http.MethodPut, e.srv.URL+"/api/admin/settings/"+loginguard.KeyLockBase, map[string]any{"value": "90"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	mcpTool(t, e.srv.URL, tok, "admin_settings_set", `{"key":"`+loginguard.KeyLockMax+`","body":{"value":"1800"}}`)
	require.Equal(t, http.StatusOK, tokenCall(t, e.srv.URL, tok, http.MethodPatch, "/api/ai/admin/settings/",
		`{"`+loginguard.KeyWindow+`":"700"}`))

	// A batch that mixes a sign-in key with another setting stays a settings
	// change (one request, one row) and names the sign-in field in its detail.
	status, body = doJSON(t, e.admin, http.MethodPatch, e.srv.URL+"/api/admin/settings",
		map[string]any{loginguard.KeyIPMax: "12", "site_name": "x"})
	require.Equal(t, http.StatusOK, status, "%v", body)

	items, _ := e.trail(t, "&action=login_security.update")
	got := map[string]string{}
	for _, it := range items {
		require.Len(t, it.ChangedFields, 1, "%+v", it)
		got[it.ChangedFields[0]] = it.Via
	}
	require.Equal(t, map[string]string{
		"lock_base_seconds": "panel",
		"lock_max_seconds":  "mcp",
		"window_seconds":    "api",
	}, got)

	rows, err := e.store.ListAuditRecent(ctx, 500)
	require.NoError(t, err)
	var mixed int
	for _, r := range rows {
		if r.Action == "settings.update" {
			mixed++
			require.Equal(t, []any{"ip_max_fails"}, r.Metadata["login_security_fields"], "%v", r.Metadata)
		}
	}
	require.Equal(t, 1, mixed)
}

package handlers_test

// An app's action from the AI surface (task #114): app_actions, app_run and
// their REST twins, against the echo fixture app - the explorer's own run
// route behind them, so the app's `applies` rule, its hidden actions and the
// audit row are the explorer's. Red on the code before 0.50: there was no
// tool and no route (`unknown tool "file_convert"`, measured 2026-09-28).

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// actionIDs lists the `plugin/id` of app_actions' answer.
func actionIDs(t *testing.T, tl e2eAITool) []string {
	t.Helper()
	_, res := doorResult(t, tl)
	rows, _ := res["actions"].([]any)
	out := []string{}
	for _, r := range rows {
		m := r.(map[string]any)
		out = append(out, m["plugin"].(string)+"/"+m["id"].(string))
	}
	return out
}

// TestAIDoors_AppActionsAreTheOnesThatApplyToTheFile - app_actions answers
// the menu the explorer offers for THIS file: the `applies` rule judged on its
// extension and the app's state on it, hidden actions never.
func TestAIDoors_AppActionsAreTheOnesThatApplyToTheFile(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/not.txt", "merhaba")
	f.writeFile(t, "docs/veri.csv", "a,b")
	tok := testutil.NewAPIToken(t, f.store, f.adminID(t), "read,write,mcp")

	tl := doorCall(t, f.srv.URL, tok, "app_actions", map[string]any{"path": "main://docs/not.txt"})
	require.False(t, tl.IsError, tl.Text)
	txt := actionIDs(t, tl)
	assert.Contains(t, txt, "echo/upper", "upper applies to a .txt")
	assert.Contains(t, txt, "echo/fresh", "fresh applies to a .txt with no `runs` state")
	assert.NotContains(t, txt, "echo/again", "again needs the `runs` state, which this file does not have")
	assert.NotContains(t, txt, "echo/applied", "a hidden action is never offered")

	tl = doorCall(t, f.srv.URL, tok, "app_actions", map[string]any{"path": "main://docs/veri.csv"})
	require.False(t, tl.IsError, tl.Text)
	csv := actionIDs(t, tl)
	assert.NotContains(t, csv, "echo/upper", "upper is for .txt only")
	assert.Contains(t, csv, "echo/facts", "an action for any kind applies")

	// The REST twin answers the same list.
	resp := aiReq(t, http.DefaultClient, http.MethodGet, f.srv.URL+"/api/ai/apps/actions?path=main://docs/veri.csv", tok, nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rest struct {
		Actions []struct {
			Plugin string `json:"plugin"`
			ID     string `json:"id"`
		} `json:"actions"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rest))
	got := []string{}
	for _, a := range rest.Actions {
		got = append(got, a.Plugin+"/"+a.ID)
	}
	assert.ElementsMatch(t, csv, got)
}

// TestAIDoors_AppRunQueuesTheJobAndWritesOneRow - app_run starts the action
// the explorer's menu starts: the job runs, its output lands beside the file,
// and the run is ONE audit row, app_plugin.action_run, stamped with the token
// and `via: mcp` (the explorer's own row, not a second generic one beside it).
// A hidden action is refused, as from the menu.
func TestAIDoors_AppRunQueuesTheJobAndWritesOneRow(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/not.txt", "merhaba")
	tok := testutil.NewAPIToken(t, f.store, f.adminID(t), "read,write,mcp")

	before := len(mcpAuditRows(t, f.store))
	tl := doorCall(t, f.srv.URL, tok, "app_run", map[string]any{
		"plugin": "echo", "action": "upper", "paths": []string{"main://docs/not.txt"}, "params": map[string]any{},
	})
	require.False(t, tl.IsError, tl.Text)
	status, res := doorResult(t, tl)
	require.Equal(t, http.StatusAccepted, status, "%v", res)
	op := f.drain(t, opIDOf(t, res))
	require.Equal(t, "ok", op["status"], "%v", op)
	_, err := os.Stat(filepath.Join(f.root, "docs", "not-upper.txt"))
	require.NoError(t, err, "the action's output landed beside the file")

	rows := newRows(t, f.store, before)
	var runs int
	for _, r := range rows {
		if r.Action == "app_plugin.action_run" {
			runs++
			assert.Equal(t, "mcp", r.Metadata["via"], "the run row says the MCP door")
			assert.NotNil(t, r.Metadata["token_id"])
			assert.Equal(t, "echo", r.TargetID)
		}
		assert.False(t, strings.HasPrefix(r.Action, "ai.file."), "no second, generic row beside the run: %s", r.Action)
	}
	assert.Equal(t, 1, runs, "one run, one row: %+v", rows)

	// The REST twin: the same one row, via api.
	before = len(mcpAuditRows(t, f.store))
	resp := aiReq(t, http.DefaultClient, http.MethodPost, f.srv.URL+"/api/ai/apps/run", tok, map[string]any{
		"plugin": "echo", "action": "upper", "paths": []string{"main://docs/not.txt"}, "params": map[string]any{},
	})
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "%v", out)
	f.drain(t, opIDOf(t, out))
	rows = newRows(t, f.store, before)
	runs = 0
	for _, r := range rows {
		if r.Action == "app_plugin.action_run" {
			runs++
			assert.Equal(t, "api", r.Metadata["via"])
		}
	}
	assert.Equal(t, 1, runs, "%+v", rows)

	// A hidden action is the app's own second half: never from here.
	tl = doorCall(t, f.srv.URL, tok, "app_run", map[string]any{
		"plugin": "echo", "action": "applied", "paths": []string{"main://docs/not.txt"}, "params": map[string]any{},
	})
	require.True(t, tl.IsError, tl.Raw)
	assert.True(t, strings.HasPrefix(tl.Text, "NOT_FOUND"), "%q", tl.Text)
	jobs := 0
	require.NoError(t, f.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM app_plugin_jobs WHERE action_id = 'applied'`).Scan(&jobs))
	assert.Zero(t, jobs, "the refused hidden action queued nothing")
}

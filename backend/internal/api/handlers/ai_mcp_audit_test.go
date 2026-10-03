package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// The audit log of the MCP door (docs/MCP.md, docs/BACKEND.md → Audit).
//
// ⚠ Measured before the fix (2026-10-01): every POST to /api/ai/mcp - a read,
// a tools/list, a write alike - left ONE row, `ai.file.mcp`, and no file tool
// left a row of its own. The log said "the agent spoke MCP" twenty times and
// never what it changed. Now a read leaves nothing and each write leaves ONE
// row: the row its REST twin under /api/ai leaves, with `via: mcp`.

func mcpAuditRows(t *testing.T, store db.Store) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 1000)
	require.NoError(t, err)
	return rows
}

// newRows is what was written after the row count was `before` - by id, not
// by time: rows written in the same instant have no order by time.
func newRows(t *testing.T, store db.Store, before int) []*model.AuditEntry {
	t.Helper()
	rows := mcpAuditRows(t, store)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows[before:]
}

// mcpCall calls one MCP tool with a token and answers the raw body; wantErr
// says whether the tool is expected to fail.
func mcpCall(t *testing.T, base, tok, name string, args any, wantErr bool) string {
	t.Helper()
	a, err := json.Marshal(args)
	require.NoError(t, err)
	code, body := mcpPost(t, http.DefaultClient, base+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(a)+`}}`)
	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, wantErr, strings.Contains(body, `"isError":true`), "%s: %s", name, body)
	return body
}

// restCall sends one /api/ai request with a token and requires success.
func restCall(t *testing.T, base, tok, path string, body any) map[string]any {
	t.Helper()
	resp := aiReq(t, http.DefaultClient, http.MethodPost, base+path, tok, body)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %v", path, out)
	return out
}

func TestMCPAudit_ReadsLeaveNoRow(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	restCall(t, srv.URL, tok, "/api/ai/upload", map[string]any{"path": "main://r/a.txt", "content": "hello"})
	before := len(mcpAuditRows(t, store))

	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	} {
		code, body := mcpPost(t, http.DefaultClient, srv.URL+"/api/ai/mcp", tok, payload)
		require.Equal(t, http.StatusOK, code, body)
	}
	mcpCall(t, srv.URL, tok, "file_root", map[string]any{}, false)
	mcpCall(t, srv.URL, tok, "file_list", map[string]any{"path": "main://r"}, false)
	mcpCall(t, srv.URL, tok, "file_info", map[string]any{"path": "main://r/a.txt"}, false)
	mcpCall(t, srv.URL, tok, "file_read", map[string]any{"path": "main://r/a.txt"}, false)
	mcpCall(t, srv.URL, tok, "file_search", map[string]any{"path": "main://", "query": "a", "content": false}, false)
	mcpCall(t, srv.URL, tok, "file_tags", map[string]any{"path": "main://r/a.txt"}, false)
	mcpCall(t, srv.URL, tok, "admin_dashboard", map[string]any{}, false)
	mcpCall(t, srv.URL, tok, "admin_audit_list", map[string]any{}, false)

	require.Empty(t, newRows(t, store, before), "a read through MCP writes no audit row")
}

// TestMCPAudit_EachWriteLeavesTheRowItsRESTTwinLeaves drives every file tool
// that changes something, then its /api/ai twin, and compares the two rows.
func TestMCPAudit_EachWriteLeavesTheRowItsRESTTwinLeaves(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	base := srv.URL
	restCall(t, base, tok, "/api/ai/upload", map[string]any{"path": "main://seed/z.txt", "content": "zip me"})

	type step struct {
		tool     string
		args     func(side string) any
		restPath string
		restBody func(side string) any
	}
	var shareTokens = map[string]string{}
	steps := []step{
		{"file_write", func(s string) any { return map[string]any{"path": "main://" + s + "/a.txt", "content": "x"} },
			"/api/ai/upload", func(s string) any { return map[string]any{"path": "main://" + s + "/a.txt", "content": "x"} }},
		{"file_mkdir", func(s string) any { return map[string]any{"path": "main://" + s + "/dir"} },
			"/api/ai/mkdir", func(s string) any { return map[string]any{"path": "main://" + s + "/dir"} }},
		{"file_move", func(s string) any {
			return map[string]any{"src": "main://" + s + "/a.txt", "dst": "main://" + s + "/dir/a.txt"}
		},
			"/api/ai/move", func(s string) any {
				return map[string]any{"src": "main://" + s + "/a.txt", "dst": "main://" + s + "/dir/a.txt"}
			}},
		{"file_tags", func(s string) any {
			return map[string]any{"path": "main://" + s + "/dir/a.txt", "set": []map[string]string{{"name": "x", "kind": "personal"}}}
		}, "/api/ai/tags", func(s string) any {
			return map[string]any{"path": "main://" + s + "/dir/a.txt", "tags": []map[string]string{{"name": "x", "kind": "personal"}}}
		}},
		{"file_zip", func(s string) any {
			return map[string]any{"sources": []string{"main://" + s + "/dir"}, "dest": "main://" + s + "/d.zip"}
		},
			"/api/ai/zip", func(s string) any {
				return map[string]any{"sources": []string{"main://" + s + "/dir"}, "dest": "main://" + s + "/d.zip"}
			}},
		{"file_unzip", func(s string) any {
			return map[string]any{"src": "main://" + s + "/d.zip", "dest_dir": "main://" + s + "/out"}
		},
			"/api/ai/unzip", func(s string) any {
				return map[string]any{"src": "main://" + s + "/d.zip", "dest": "main://" + s + "/out"}
			}},
		{"file_share", func(s string) any { return map[string]any{"path": "main://" + s + "/dir/a.txt"} },
			"/api/ai/share", func(s string) any { return map[string]any{"path": "main://" + s + "/dir/a.txt"} }},
		{"file_unshare", func(s string) any { return map[string]any{"token": shareTokens[s]} },
			"/api/ai/unshare", func(s string) any { return map[string]any{"token": shareTokens[s]} }},
		{"file_upload_ticket", func(s string) any { return map[string]any{"path": "main://" + s + "/big.bin"} },
			"/api/ai/upload/ticket", func(s string) any { return map[string]any{"path": "main://" + s + "/big.bin"} }},
		{"file_delete", func(s string) any { return map[string]any{"path": "main://" + s + "/dir/a.txt"} },
			"/api/ai/delete", func(s string) any { return map[string]any{"path": "main://" + s + "/dir/a.txt"} }},
	}

	for _, st := range steps {
		before := len(mcpAuditRows(t, store))
		body := mcpCall(t, base, tok, st.tool, st.args("m"), false)
		if st.tool == "file_share" {
			var env struct {
				Result struct {
					StructuredContent struct {
						Token string `json:"token"`
					} `json:"structuredContent"`
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &env), body)
			shareTokens["m"] = env.Result.StructuredContent.Token
		}
		mcpRows := newRows(t, store, before)
		require.Len(t, mcpRows, 1, "%s leaves exactly one row: %+v", st.tool, mcpRows)

		before = len(mcpAuditRows(t, store))
		out := restCall(t, base, tok, st.restPath, st.restBody("r"))
		if st.restPath == "/api/ai/share" {
			shareTokens["r"], _ = out["token"].(string)
		}
		restRows := newRows(t, store, before)
		require.Len(t, restRows, 1, "%s leaves exactly one row", st.restPath)

		m, r := mcpRows[0], restRows[0]
		require.Equal(t, r.Action, m.Action, "%s is filed as its REST twin %s is", st.tool, st.restPath)
		require.Equal(t, r.TargetType, m.TargetType, st.tool)
		require.Equal(t, r.TargetID, m.TargetID, st.tool)
		require.Equal(t, r.UserID, m.UserID, st.tool)
		require.Equal(t, r.IP, m.IP, "%s names the caller's address like REST does", st.tool)
		require.NotEmpty(t, m.IP, st.tool)
		require.Equal(t, "mcp", m.Metadata["via"], st.tool)
		require.Equal(t, "api", r.Metadata["via"], st.restPath)
		require.Equal(t, r.Metadata["token_id"], m.Metadata["token_id"], st.tool)
		require.NotNil(t, m.Metadata["token_id"], st.tool)
		mk, rk := keysExcept(m.Metadata, "via"), keysExcept(r.Metadata, "via")
		require.Equal(t, rk, mk, "%s carries the detail its REST twin carries, no more", st.tool)
	}

	// A write that fails leaves no row, as a REST 4xx leaves none.
	before := len(mcpAuditRows(t, store))
	mcpCall(t, base, tok, "file_mkdir", map[string]any{"path": "nosuch://x"}, true)
	mcpCall(t, base, tok, "file_move", map[string]any{"src": "main://m/never-there.txt", "dst": "main://m/x.txt"}, true)
	require.Empty(t, newRows(t, store, before), "a refused write is not audited")
}

// A token confined to a folder (root: scope) writes through MCP: its rows name
// no path at all - the REST rows do not either - so nothing outside (or
// inside) its root reaches a log read as that token's work.
func TestMCPAudit_ConfinedTokenRowsCarryNoPath(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	restCall(t, srv.URL, tok, "/api/ai/mkdir", map[string]any{"path": "main://secretfolder/inner"})
	admin, err := store.GetUserByEmail(context.Background(), "admin2@test.local")
	require.NoError(t, err)
	confined := issueToken(t, store, admin.ID, "read,write,mcp,root:main://secretfolder/inner", nil)

	before := len(mcpAuditRows(t, store))
	mcpCall(t, srv.URL, confined, "file_write", map[string]any{"path": "note.txt", "content": "x"}, false)
	rows := newRows(t, store, before)
	require.Len(t, rows, 1)
	raw, err := json.Marshal(rows[0])
	require.NoError(t, err)
	for _, s := range []string{"secretfolder", "inner", "note.txt", "main://"} {
		require.NotContains(t, string(raw), s)
	}
}

func keysExcept(m map[string]any, skip string) []string {
	out := []string{}
	for k := range m {
		if k != skip {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

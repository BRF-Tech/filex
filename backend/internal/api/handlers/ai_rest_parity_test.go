package handlers_test

// The AI surface asks what its REST twin asks (task #112, item 3): every
// AI/MCP file operation was compared with the explorer's door for the same
// action, and these are the places it asked less.
//
//   - file_zip / POST /api/ai/zip packed any source the caller could SEE;
//     POST /api/files/archive/create asks files.download on each one. An
//     account whose downloads were taken away zipped a file and fetched the
//     archive.
//   - file_unshare / POST /api/ai/unshare asked only "is it your link (or are
//     you an admin)"; DELETE /api/files/share/{id} also asks the tenant and
//     the token's root.
//   - file_mkdir asked files.create on the new path, file_move the relocate
//     permission on the destination path; the explorer asks them on the
//     FOLDER that gains the entry. A grant on exactly that path (grants are
//     path prefixes, one may name a single file) let an agent add entries to
//     a folder its account may only view.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// parityTool calls one MCP tool and returns its error flag and text, after
// asserting the answer is a tool result and not a JSON-RPC error.
func parityTool(t *testing.T, base, tok, name string, args any) (bool, string, json.RawMessage) {
	t.Helper()
	a, err := json.Marshal(args)
	require.NoError(t, err)
	code, body := mcpPost(t, &http.Client{}, base+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(a)+`}}`)
	require.Equal(t, http.StatusOK, code, body)
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	require.True(t, start >= 0 && end > start, body)
	var rpc struct {
		Error  *json.RawMessage `json:"error"`
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(body[start:end+1]), &rpc), body)
	require.Nil(t, rpc.Error, "%s: a JSON-RPC error, not a tool answer: %s", name, body)
	require.NotNil(t, rpc.Result, body)
	text := ""
	for _, c := range rpc.Result.Content {
		text += c.Text
	}
	return rpc.Result.IsError, text, rpc.Result.Structured
}

// TestAIZip_AsksWhatADownloadAsks - packing takes the bytes, so the AI zip
// asks files.download on every source (packSourceRefusal, the rule
// archive/create asks too).
func TestAIZip_AsksWhatADownloadAsks(t *testing.T) {
	pf := newPermFix(t)
	src := []string{"alpha://report.txt"}

	// With the permission the zip is made: the refusal below is about it.
	status, body := fxPost(t, pf.URL+"/api/ai/zip", pf.memberTok, map[string]any{"sources": src, "dest": "alpha://izinli.zip"})
	require.Equal(t, http.StatusOK, status, body)

	pf.override(t, map[string]string{"files.download": model.PermDeny})

	// The explorer's twin refuses it (archive/download asks the same
	// permission archive/create does).
	status, body = fxPost(t, pf.URL+"/api/files/archive/download", pf.memberTok, map[string]any{"paths": src})
	requireDenied(t, "explorer zip download", status, body, perm.FilesDownload, perm.SourceOverride)

	status, body = fxPost(t, pf.URL+"/api/ai/zip", pf.memberTok, map[string]any{"sources": src, "dest": "alpha://sizinti.zip"})
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Contains(t, body, "files.download", body)
	assert.NoFileExists(t, filepath.Join(pf.RootA, "sizinti.zip"))

	isErr, text, _ := parityTool(t, pf.URL, pf.memberTok, "file_zip", map[string]any{"sources": src, "dest": "alpha://sizinti.zip"})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "files.download", text)
	assert.NoFileExists(t, filepath.Join(pf.RootA, "sizinti.zip"))

	// Unzip's source is read like a preview (viewer), as the explorer's
	// archive/extract reads it: unchanged.
	status, body = fxPost(t, pf.URL+"/api/ai/unzip", pf.memberTok, map[string]any{"src": "alpha://izinli.zip", "dest": "alpha://acilan"})
	assert.Equal(t, http.StatusOK, status, body)
}

// TestAIUnshare_StaysInsideTheTokensRoot - a folder-confined token revokes
// only links on files inside its folder, as DELETE /api/files/share/{id}
// already required (shareRevokeRefusal).
func TestAIUnshare_StaysInsideTheTokensRoot(t *testing.T) {
	srv, client, store, admin := aiFixture(t)
	ctx := context.Background()

	share := func(p string) string {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", admin, map[string]any{"path": p, "content": "x"})
		require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
		resp = aiReq(t, client, "POST", srv.URL+"/api/ai/share", admin, map[string]any{"path": p})
		body := readBody(t, resp)
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		tok, _ := body["token"].(string)
		require.NotEmpty(t, tok, body)
		return tok
	}
	outside := share("main://outside/x.txt")
	inside := share("main://tenant/y.txt")

	// The same account as the full token (aiFixture's admin, testutil
	// SeedAdminUser), narrowed to one folder: an admin, so "is it your link"
	// says yes to every link and only the root can say no.
	u, err := store.GetUserByEmail(ctx, "admin2@test.local")
	require.NoError(t, err)
	confined := issueToken(t, store, u.ID, "read,write,delete,mcp,root:main://tenant", nil)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/unshare", confined, map[string]any{"token": outside})
	body := readBody(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a link outside the token's root reads as one that does not exist: %v", body)
	assert.Equal(t, "share not found", body["error"], body)

	isErr, text, _ := parityTool(t, srv.URL, confined, "file_unshare", map[string]any{"token": outside})
	assert.True(t, isErr, text)
	assert.Equal(t, "share not found", text)

	// A revoke ends the link now (store.RevokeShare sets expires_at); a link
	// is minted with a future expiry by the link policy, so "live" is an
	// expiry still ahead.
	live := func(tok string) bool {
		sh, err := store.GetShareByToken(ctx, tok)
		require.NoError(t, err)
		return sh.ExpiresAt == nil || sh.ExpiresAt.After(time.Now())
	}
	assert.True(t, live(outside), "the link outside the root is still live")

	// Inside the root, somebody else's link is refused as it is in the
	// explorer: 403, the link stays.
	other, err := store.CreateUser(ctx, "baskasi@test.local", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	otherTok := issueToken(t, store, other.ID, "read,write,delete,mcp", nil)
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/unshare", otherTok, map[string]any{"token": inside})
	body = readBody(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%v", body)
	assert.Equal(t, "forbidden: not your share", body["error"], body)

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/unshare", confined, map[string]any{"token": inside})
	assert.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	assert.False(t, live(inside), "inside its root the token revokes as before")
}

// TestAIMkdirMove_AskTheFolderThatGainsTheEntry - a user whose only grants
// name single paths in rbac://docs (where it has no grant of its own) may not
// create a folder there or rename a file there: neither through the explorer
// nor, now, through the AI surface.
func TestAIMkdirMove_AskTheFolderThatGainsTheEntry(t *testing.T) {
	f := newVerbFixture(t)
	ctx := context.Background()
	const email = "verb-dar@test.local"
	testutil.SeedRegularUser(t, f.store, email, verbPassword)
	u, err := f.store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	for _, g := range []struct {
		prefix string
		dir    bool
	}{{"docs/yeni", true}, {"docs/gizli.txt", false}, {"docs/hedef.txt", false}} {
		_, err := f.store.CreateFileGrant(ctx, &model.FileGrant{
			StorageID: f.rbac.ID, PathPrefix: g.prefix, IsDir: g.dir, UserID: u.ID, Level: model.GrantEditor,
		})
		require.NoError(t, err, g.prefix)
	}
	tok := testutil.NewAPIToken(t, f.store, u.ID, "read,write,delete,mcp")
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(f.rbacRoot, filepath.FromSlash(rel)))
		return err == nil
	}

	// The explorer's twins refuse both.
	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=newfolder", `{"path":"rbac://docs","name":"yeni"}`)
	assert.Equal(t, http.StatusForbidden, code, "explorer newfolder: %s", body)
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=rename",
		`{"path":"rbac://docs","item":"rbac://docs/gizli.txt","name":"hedef.txt"}`)
	assert.Equal(t, http.StatusForbidden, code, "explorer rename: %s", body)

	// And so does the AI surface, on both doors.
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/ai/mkdir", `{"path":"rbac://docs/yeni"}`)
	assert.Equal(t, http.StatusForbidden, code, "/api/ai/mkdir: %s", body)
	isErr, text, _ := parityTool(t, f.srv.URL, tok, "file_mkdir", map[string]any{"path": "rbac://docs/yeni"})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "access denied", text)
	assert.False(t, exists("docs/yeni"))

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/ai/move", `{"src":"rbac://docs/gizli.txt","dst":"rbac://docs/hedef.txt"}`)
	assert.Equal(t, http.StatusForbidden, code, "/api/ai/move: %s", body)
	isErr, text, _ = parityTool(t, f.srv.URL, tok, "file_move", map[string]any{"src": "rbac://docs/gizli.txt", "dst": "rbac://docs/hedef.txt"})
	assert.True(t, isErr, text)
	assert.Contains(t, text, "access denied", text)
	assert.True(t, exists("docs/gizli.txt"))
	assert.False(t, exists("docs/hedef.txt"))

	// The editor of docs/ is not affected.
	editor := testutil.NewAPIToken(t, f.store, f.editorID, "read,write,delete,mcp")
	code, body = f.call(t, nil, editor, http.MethodPost, "/api/ai/mkdir", `{"path":"rbac://docs/yeni"}`)
	assert.Equal(t, http.StatusOK, code, body)
	code, body = f.call(t, nil, editor, http.MethodPost, "/api/ai/move", `{"src":"rbac://docs/gizli.txt","dst":"rbac://docs/hedef.txt"}`)
	assert.Equal(t, http.StatusOK, code, body)
}

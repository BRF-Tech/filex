package handlers_test

// An API token does exactly what its verbs name — read, write, delete — on
// EVERY surface it can authenticate on, not only on /api/ai: the same token
// must be as narrow in the web explorer's routes (and so in `filex client`) as
// it is on /api/ai/delete.
//
// The rule these tests hold the router to:
//
//   - `read` is needed for every request on the file and account surfaces;
//   - `write` for everything that changes files, folders and what hangs off
//     them (shares, grants, team tags, versions, uploads, archives, app
//     actions that write), and for changing the account itself (profile,
//     password, two-factor) — without it a token keeps its preferences,
//     stars, recents, personal tags, comments, bell and its own keys;
//   - `delete` for removing files.
//
// A browser session is judged by the account alone, exactly as before.
//
// ⚠ The harness registers the api-token driver the way internal/server does
// (useProductionAuthChain). /api/files authenticates tokens through
// MiddlewareWithToken on its own, but a harness that differs from production is
// how a suite goes green over a broken gate — token_admin_gate_test.go says why.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const (
	verbMemberEmail = "verb-member@test.local"
	verbViewerEmail = "verb-viewer@test.local"
	verbEditorEmail = "verb-editor@test.local"
	verbPassword    = "VerbPass!123"
)

type verbFixture struct {
	srv      *httptest.Server
	store    db.Store
	depoRoot string // storage "depo", RBAC off: a member is an editor everywhere
	rbacRoot string // storage "rbac", RBAC on: viewer / editor grants on docs/
	depo     *model.Storage
	rbac     *model.Storage
	secretID int64 // node rbac://docs/gizli.txt
	memberID int64
	viewerID int64
	editorID int64
}

// verbLocalResolver opens every storage row as a local-disk driver.
func verbLocalResolver(st db.Store) func(int64) (storage.Driver, error) {
	return func(id int64) (storage.Driver, error) {
		row, err := st.GetStorage(context.Background(), id)
		if err != nil || row == nil {
			return nil, fmt.Errorf("unknown storage %d", id)
		}
		var cfg map[string]any
		if err := json.Unmarshal(row.ConfigJSON, &cfg); err != nil {
			return nil, err
		}
		drv := &local.Driver{}
		if err := drv.Init(context.Background(), cfg); err != nil {
			return nil, err
		}
		return drv, nil
	}
}

func newVerbFixture(t *testing.T) *verbFixture {
	t.Helper()
	ctx := context.Background()

	depoRoot, rbacRoot := t.TempDir(), t.TempDir()
	for _, f := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		require.NoError(t, os.MkdirAll(filepath.Join(depoRoot, "docs"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(depoRoot, "docs", f), []byte("content of "+f), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(rbacRoot, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rbacRoot, "docs", "gizli.txt"), []byte("gizli"), 0o644))

	srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.StorageResolver = verbLocalResolver(d.Store)
	})
	useProductionAuthChain(t, store)

	mkStorage := func(name, root string, rbac bool) *model.Storage {
		cfg, err := json.Marshal(map[string]any{"root": root})
		require.NoError(t, err)
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, ConfigJSON: cfg,
			SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true, RBACEnabled: rbac,
		})
		require.NoError(t, err)
		return st
	}
	depo := mkStorage("depo", depoRoot, false)
	rbac := mkStorage("rbac", rbacRoot, true)

	// The share surfaces resolve a path to a catalogue row; list both levels.
	docs, err := store.CreateNode(ctx, &model.Node{
		StorageID: rbac.ID, Name: "docs", Path: "/docs", Type: model.NodeTypeDirectory,
		PathHash: pathkey.Hash(rbac.ID, "/docs"), SeenAt: time.Now(),
	})
	require.NoError(t, err)
	secret, err := store.CreateNode(ctx, &model.Node{
		StorageID: rbac.ID, ParentID: &docs.ID, Name: "gizli.txt", Path: "/docs/gizli.txt",
		Type: model.NodeTypeFile, Size: 5, Etag: "g", Mime: "text/plain",
		PathHash: pathkey.Hash(rbac.ID, "/docs/gizli.txt"), SeenAt: time.Now(),
	})
	require.NoError(t, err)

	testutil.SeedAdmin(t, store)
	idOf := func(email string) int64 {
		testutil.SeedRegularUser(t, store, email, verbPassword)
		u, err := store.GetUserByEmail(ctx, email)
		require.NoError(t, err)
		return u.ID
	}
	f := &verbFixture{
		srv: srv, store: store, depoRoot: depoRoot, rbacRoot: rbacRoot,
		depo: depo, rbac: rbac, secretID: secret.ID,
		memberID: idOf(verbMemberEmail),
		viewerID: idOf(verbViewerEmail),
		editorID: idOf(verbEditorEmail),
	}
	for uid, level := range map[int64]string{f.viewerID: model.GrantViewer, f.editorID: model.GrantEditor} {
		_, err := store.CreateFileGrant(ctx, &model.FileGrant{
			StorageID: rbac.ID, PathPrefix: "docs", IsDir: true, UserID: uid, Level: level,
		})
		require.NoError(t, err)
	}
	return f
}

// session signs one account in and returns its cookie client.
func (f *verbFixture) session(t *testing.T, email string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, c, email, verbPassword)
	return c
}

// call sends one JSON request. token == "" means "no token" (the client's
// cookie, if any, is the credential).
func (f *verbFixture) call(t *testing.T, c *http.Client, token, method, path, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Filex-Token", token)
	}
	if c == nil {
		c = &http.Client{}
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (f *verbFixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.depoRoot, filepath.FromSlash(rel)))
	return err == nil
}

func newFolderBody(name string) string {
	return `{"path":"depo://docs","name":"` + name + `"}`
}

func deleteBody(file string) string {
	return `{"path":"depo://docs","items":[{"path":"depo://docs/` + file + `","type":"file"}]}`
}

// refusedFor asserts the answer is the verb refusal, naming the verb.
func refusedFor(t *testing.T, verb string, code int, body, what string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, code, "%s must be refused; body: %s", what, body)
	assert.Contains(t, body, "token missing scope: "+verb, "%s: the refusal names the missing verb", what)
}

// TestFilesSurface_ReadTokenCannotWriteOrDelete — a `read` token reads and
// changes nothing, through every shape of the explorer's mutations.
func TestFilesSurface_ReadTokenCannotWriteOrDelete(t *testing.T) {
	f := newVerbFixture(t)
	tok := testutil.NewAPIToken(t, f.store, f.memberID, "read,mcp")

	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=newfolder", newFolderBody("ro-klasor"))
	refusedFor(t, "write", code, body, "read token: manager newfolder")
	assert.False(t, f.exists("docs/ro-klasor"), "no folder may be created")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=delete", deleteBody("a.txt"))
	refusedFor(t, "delete", code, body, "read token: manager delete")
	assert.True(t, f.exists("docs/a.txt"), "the file must stay where it was")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/delete", `{"source":["depo://docs/a.txt"]}`)
	refusedFor(t, "delete", code, body, "read token: /api/files/delete")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/ops",
		fmt.Sprintf(`{"kind":"delete","storage_id":%d,"sources":["docs/a.txt"]}`, f.depo.ID))
	refusedFor(t, "delete", code, body, "read token: ops kind=delete")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/ops",
		fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["docs/a.txt"],"dest":"docs/kopya"}`, f.depo.ID))
	refusedFor(t, "write", code, body, "read token: ops kind=copy")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/save-text", `{"path":"depo://docs/b.txt","content":"ezildi"}`)
	refusedFor(t, "write", code, body, "read token: save-text")
	got, _ := os.ReadFile(filepath.Join(f.depoRoot, "docs", "b.txt"))
	assert.Equal(t, "content of b.txt", string(got), "the file must not be overwritten")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/share", `{"path":"depo://docs/b.txt"}`)
	refusedFor(t, "write", code, body, "read token: create a public link")

	// …and what it may do is untouched.
	code, body = f.call(t, nil, tok, http.MethodGet, "/api/files/manager?action=index&path=depo://docs", "")
	assert.Equal(t, http.StatusOK, code, "read token lists; body: %s", body)
	assert.Contains(t, body, "a.txt")

	// ?action=allowed is a QUESTION — which of these may I do there — and it
	// changes nothing, so `read` asks it. Under `write` the explorer's
	// per-folder question failed for every read-only token, and a failed
	// question offers every such action (0.49.0 doc audit).
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=allowed",
		`{"items":[{"path":"depo://docs/a.txt"}],"permissions":["files.download","files.delete"]}`)
	assert.Equal(t, http.StatusOK, code, "read token asks what it may do; body: %s", body)
	assert.NotContains(t, body, "token missing scope")
}

// TestFilesSurface_WriteAndDeleteAreSeparateVerbs — write without delete
// writes and cannot delete; delete without write deletes and cannot write.
func TestFilesSurface_WriteAndDeleteAreSeparateVerbs(t *testing.T) {
	f := newVerbFixture(t)

	writer := testutil.NewAPIToken(t, f.store, f.memberID, "read,write")
	code, body := f.call(t, nil, writer, http.MethodPost, "/api/files/manager?action=newfolder", newFolderBody("yazar"))
	assert.Equal(t, http.StatusOK, code, "read,write token creates a folder; body: %s", body)
	assert.True(t, f.exists("docs/yazar"))

	code, body = f.call(t, nil, writer, http.MethodPost, "/api/files/manager?action=delete", deleteBody("c.txt"))
	refusedFor(t, "delete", code, body, "read,write token: manager delete")
	assert.True(t, f.exists("docs/c.txt"))

	code, body = f.call(t, nil, writer, http.MethodPost, "/api/files/ops",
		fmt.Sprintf(`{"kind":"delete","storage_id":%d,"sources":["docs/c.txt"]}`, f.depo.ID))
	refusedFor(t, "delete", code, body, "read,write token: ops kind=delete")

	deleter := testutil.NewAPIToken(t, f.store, f.memberID, "read,delete")
	code, body = f.call(t, nil, deleter, http.MethodPost, "/api/files/manager?action=delete", deleteBody("c.txt"))
	assert.Equal(t, http.StatusOK, code, "read,delete token deletes; body: %s", body)
	assert.False(t, f.exists("docs/c.txt"), "the file went to the trash")

	code, body = f.call(t, nil, deleter, http.MethodPost, "/api/files/manager?action=newfolder", newFolderBody("silici"))
	refusedFor(t, "write", code, body, "read,delete token: manager newfolder")
	assert.False(t, f.exists("docs/silici"))

	// A token without `read` reaches nothing on these surfaces.
	blind := testutil.NewAPIToken(t, f.store, f.memberID, "write,delete,mcp")
	code, body = f.call(t, nil, blind, http.MethodGet, "/api/files/manager?action=index&path=depo://docs", "")
	refusedFor(t, "read", code, body, "token without read: listing")
}

// TestFilesSurface_TheFullListKeepsEverything — a token minted before verbs
// were required carries the explicit full list (what the v0.43.0 upgrade wrote
// every empty list out as), and the desktop pairing mints read,write,delete:
// both keep every verb.
func TestFilesSurface_TheFullListKeepsEverything(t *testing.T) {
	f := newVerbFixture(t)
	for i, scopes := range []string{"read,write,delete,mcp,admin", "read,write,delete"} {
		tok := testutil.NewAPIToken(t, f.store, f.memberID, scopes)
		name := fmt.Sprintf("tam-%d", i)
		code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=newfolder", newFolderBody(name))
		assert.Equal(t, http.StatusOK, code, "%s: newfolder; body: %s", scopes, body)
		assert.True(t, f.exists("docs/"+name))
		file := []string{"a.txt", "b.txt"}[i]
		code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=delete", deleteBody(file))
		assert.Equal(t, http.StatusOK, code, "%s: delete; body: %s", scopes, body)
		assert.False(t, f.exists("docs/"+file))
	}
}

// TestFilesSurface_SessionIsUnchanged — the browser session of the same
// account keeps every verb its role and grants give it.
func TestFilesSurface_SessionIsUnchanged(t *testing.T) {
	f := newVerbFixture(t)
	s := f.session(t, verbMemberEmail)

	code, body := f.call(t, s, "", http.MethodPost, "/api/files/manager?action=newfolder", newFolderBody("oturum"))
	assert.Equal(t, http.StatusOK, code, body)
	assert.True(t, f.exists("docs/oturum"))

	code, body = f.call(t, s, "", http.MethodPost, "/api/files/manager?action=delete", deleteBody("d.txt"))
	assert.Equal(t, http.StatusOK, code, body)
	assert.False(t, f.exists("docs/d.txt"))
}

// TestFilesSurface_ReadTokenKeepsWhatAViewerKeeps — the personal surfaces are
// not file mutations. A viewer's desktop is paired with a `read` token, and it
// must keep its theme, its stars, its recents and its bell.
func TestFilesSurface_ReadTokenKeepsWhatAViewerKeeps(t *testing.T) {
	f := newVerbFixture(t)
	tok := testutil.NewAPIToken(t, f.store, f.memberID, "read")

	code, body := f.call(t, nil, tok, http.MethodPut, "/api/me/prefs?surface=desktop", `{"prefs":{"theme":"dark"}}`)
	assert.Equal(t, http.StatusOK, code, "prefs; body: %s", body)
	// The harness runs no notification service (503); what matters is that the
	// verb gate lets the request through to it.
	code, body = f.call(t, nil, tok, http.MethodPost, "/api/notifications/read-all", "")
	assert.NotContains(t, body, "token missing scope", "bell; %d %s", code, body)
	code, body = f.call(t, nil, tok, http.MethodGet, "/api/files/manager/star/list", "")
	assert.Equal(t, http.StatusOK, code, "stars; body: %s", body)
}

// TestAccountChanges_NeedWrite — changing the account itself (profile,
// password, two-factor) is a write: a `read` token must not set a new password
// or switch two-factor off, and a `read,write` token (the desktop app's shape)
// still reaches the handlers. Burak 2026-09-28: "write iste".
func TestAccountChanges_NeedWrite(t *testing.T) {
	f := newVerbFixture(t)
	reader := testutil.NewAPIToken(t, f.store, f.memberID, "read")
	writer := testutil.NewAPIToken(t, f.store, f.memberID, "read,write")

	cases := []struct{ method, path, body string }{
		{http.MethodPatch, "/api/auth/profile", `{"name":"Ele Geçiren"}`},
		{http.MethodPost, "/api/auth/password", `{"current_password":"x","new_password":"yeni-parola-12345"}`},
		{http.MethodPost, "/api/auth/totp/enroll", `{}`},
		{http.MethodPost, "/api/auth/totp/verify", `{"code":"000000"}`},
		{http.MethodPost, "/api/auth/totp/disable", `{"code":"000000"}`},
	}
	for _, c := range cases {
		code, body := f.call(t, nil, reader, c.method, c.path, c.body)
		refusedFor(t, "write", code, body, "read token: "+c.method+" "+c.path)

		code, body = f.call(t, nil, writer, c.method, c.path, c.body)
		assert.NotContains(t, body, "token missing scope", "read,write token reaches %s %s; %d %s", c.method, c.path, code, body)
	}
}

// TestPublicLink_EditRightsOnEverySurface — creating a public link is an
// outbound-access grant: the file leaves for people who have no account. So
// it needs edit rights on the item on every door — the explorer's REST route,
// /api/ai/share and the MCP file_share tool — and a viewer gets no link from
// any of them.
func TestPublicLink_EditRightsOnEverySurface(t *testing.T) {
	f := newVerbFixture(t)
	ctx := context.Background()
	const target = `{"path":"rbac://docs/gizli.txt"}`

	viewer := testutil.NewAPIToken(t, f.store, f.viewerID, "read,write,delete,mcp")
	editor := testutil.NewAPIToken(t, f.store, f.editorID, "read,write,delete,mcp")

	code, body := f.call(t, nil, viewer, http.MethodPost, "/api/files/share", target)
	assert.Equal(t, http.StatusForbidden, code, "viewer, REST /api/files/share; body: %s", body)

	code, body = f.call(t, nil, viewer, http.MethodPost, "/api/ai/share", target)
	assert.Equal(t, http.StatusForbidden, code, "viewer, /api/ai/share; body: %s", body)

	code, body = mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", viewer,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"file_share","arguments":{"path":"rbac://docs/gizli.txt"}}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `"isError":true`, "viewer, MCP file_share must be refused; body: %s", body)
	assert.NotContains(t, body, "/s/", "no link may come back; body: %s", body)

	links, err := f.store.ListSharesByNode(ctx, f.secretID)
	require.NoError(t, err)
	assert.Empty(t, links, "a viewer must not have created any public link")

	// The viewer's browser session is refused too (unchanged).
	code, body = f.call(t, f.session(t, verbViewerEmail), "", http.MethodPost, "/api/files/share", target)
	assert.Equal(t, http.StatusForbidden, code, "viewer session; body: %s", body)

	// An editor is allowed on all three.
	code, body = f.call(t, nil, editor, http.MethodPost, "/api/files/share", target)
	assert.Equal(t, http.StatusOK, code, "editor, REST; body: %s", body)
	code, body = f.call(t, nil, editor, http.MethodPost, "/api/ai/share", target)
	assert.Equal(t, http.StatusOK, code, "editor, /api/ai/share; body: %s", body)
	code, body = mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", editor,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"file_share","arguments":{"path":"rbac://docs/gizli.txt"}}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, `"isError":true`, "editor, MCP file_share; body: %s", body)
	assert.Contains(t, body, "/s/")
}

// TestMCP_FileToolsFollowTheTokenVerbs — the MCP file tools are the same
// operations as /api/ai/{upload,delete,…} and need the same verbs; a tool
// the token cannot use is not offered.
func TestMCP_FileToolsFollowTheTokenVerbs(t *testing.T) {
	f := newVerbFixture(t)
	url := f.srv.URL + "/api/ai/mcp"

	list := func(tok string) string {
		code, body := mcpPost(t, &http.Client{}, url, tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		require.Equal(t, http.StatusOK, code, body)
		return body
	}
	callTool := func(tok, name, args string) string {
		code, body := mcpPost(t, &http.Client{}, url, tok,
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		require.Equal(t, http.StatusOK, code, body)
		return body
	}
	quoted := func(s string) string { return `"` + s + `"` }

	reader := testutil.NewAPIToken(t, f.store, f.memberID, "read,mcp")
	tools := list(reader)
	for _, name := range []string{"file_root", "file_list", "file_info", "file_read", "file_search", "file_tags"} {
		assert.Contains(t, tools, quoted(name), "read,mcp keeps %s", name)
	}
	for _, name := range []string{"file_write", "file_upload_ticket", "file_mkdir", "file_move", "file_share", "file_unshare", "file_zip", "file_unzip", "file_delete"} {
		assert.NotContains(t, tools, quoted(name), "read,mcp must not be offered %s", name)
	}
	body := callTool(reader, "file_delete", `{"path":"depo://docs/a.txt"}`)
	assert.NotContains(t, body, `"ok":true`, "read,mcp: file_delete must not succeed; body: %s", body)
	assert.True(t, f.exists("docs/a.txt"), "read,mcp: the file must still be there")
	body = callTool(reader, "file_write", `{"path":"depo://docs/yeni.txt","content":"x"}`)
	assert.False(t, f.exists("docs/yeni.txt"), "read,mcp: file_write must not write; body: %s", body)
	body = callTool(reader, "file_tags", `{"path":"depo://docs/a.txt","set":[{"name":"ekip","kind":"team"}]}`)
	assert.Contains(t, body, `"isError":true`, "read,mcp: setting tags needs write; body: %s", body)

	writer := testutil.NewAPIToken(t, f.store, f.memberID, "read,write,mcp")
	tools = list(writer)
	assert.Contains(t, tools, quoted("file_write"))
	assert.NotContains(t, tools, quoted("file_delete"), "read,write,mcp must not be offered file_delete")

	full := testutil.NewAPIToken(t, f.store, f.memberID, "read,write,delete,mcp")
	tools = list(full)
	for _, name := range []string{"file_write", "file_delete", "file_share", "file_mkdir"} {
		assert.Contains(t, tools, quoted(name), "a full token keeps %s", name)
	}
	body = callTool(full, "file_delete", `{"path":"depo://docs/a.txt"}`)
	assert.NotContains(t, body, `"isError":true`, "full token deletes; body: %s", body)
	assert.False(t, f.exists("docs/a.txt"))

	bare := testutil.NewAPIToken(t, f.store, f.memberID, "mcp")
	tools = list(bare)
	assert.Contains(t, tools, quoted("file_root"), "discovery needs no verb, as GET /api/ai/root")
	assert.NotContains(t, tools, quoted("file_list"), "an mcp-only token reads nothing")
}

// ── the whole table ─────────────────────────────────────────────────────────

// tokenSurfacePrefixes are the route groups a token authenticates on as its
// owner (auth.MiddlewareWithToken). /api/ai has its own per-route gates and the
// admin group its admin scope; neither is walked here.
var tokenSurfacePrefixes = []string{
	"/api/files/", "/api/shares", "/api/notifications", "/api/me/", "/api/tokens", "/api/ws",
	"/api/auth/me", "/api/auth/profile", "/api/auth/password", "/api/auth/totp/",
	"/api/auth/s3-keys", "/api/auth/ssh-keys", "/api/auth/nfs-exports", "/api/auth/desktop/complete",
}

// publicUnderTokenPrefixes are public routes that happen to live under those
// prefixes: signed or anonymous by design.
var publicUnderTokenPrefixes = map[string]bool{
	"GET /api/files/share/{token}":        true,
	"GET /api/files/capabilities":         true,
	"GET /api/files/onlyoffice/fetch":     true,
	"GET /api/files/onlyoffice/probe":     true,
	"POST /api/files/onlyoffice/callback": true,
}

// readLevelMutations are the non-GET routes a `read` token may call: reads that
// travel as POST, and the personal state a viewer keeps. Every OTHER non-GET
// route is expected to need `write`, except deleteRoutes and the two whose verb
// depends on the request (handlerDecided). A new mutating route therefore
// defaults to "needs write", and goes red here if it was registered without it.
var readLevelMutations = map[string]bool{
	"POST /api/files/ws-ticket":        true,
	"POST /api/files/search":           true,
	"POST /api/files/archive/list":     true,
	"POST /api/files/archive/download": true,
	// The editor configuration: view mode unless the token may write.
	"POST /api/files/onlyoffice/config": true,
	// Personal tags; changing a TEAM tag needs write (the handler asks).
	"POST /api/files/manager/tags/":      true,
	"POST /api/files/manager/star/":      true,
	"POST /api/files/manager/recent/":    true,
	"PUT /api/files/manager/view-prefs/": true,
	"POST /api/files/comments":           true,
	"DELETE /api/files/comments/{id}":    true,
	// An app action a viewer may run; one that writes needs write (the handler asks).
	"POST /api/files/plugins/actions/{plugin}/{action}/run": true,
	"POST /api/files/plugins/views/{plugin}/{view}/event":   true,
	"POST /api/files/plugins/ui/{plugin}/{view}/call":       true,
	"POST /api/files/e2e/escrow/challenge":                  true,
	"POST /api/files/e2e/escrow/used":                       true,
	// The account's own credentials (each with its own ceiling). The account
	// ITSELF — profile, password, two-factor — is NOT here: changing it needs
	// `write` (a read token must not set a new password or switch two-factor
	// off), so the table expects write for those five.
	"POST /api/auth/s3-keys":                true,
	"POST /api/auth/s3-keys/{id}/state":     true,
	"DELETE /api/auth/s3-keys/{id}":         true,
	"POST /api/auth/ssh-keys":               true,
	"POST /api/auth/ssh-keys/{id}/state":    true,
	"DELETE /api/auth/ssh-keys/{id}":        true,
	"POST /api/auth/nfs-exports":            true,
	"POST /api/auth/nfs-exports/{id}/state": true,
	"DELETE /api/auth/nfs-exports/{id}":     true,
	"POST /api/tokens":                      true,
	"PATCH /api/tokens/{id}":                true,
	"DELETE /api/tokens/{id}":               true,
	"POST /api/auth/desktop/complete":       true,
	"PUT /api/me/prefs/":                    true,
	"POST /api/notifications/{id}/read":     true,
	"POST /api/notifications/read-all":      true,
	"PATCH /api/notifications/settings":     true,
}

var deleteRoutes = map[string]bool{
	"POST /api/files/delete":         true,
	"DELETE /api/files/drafts/{key}": true,
}

var handlerDecided = map[string]bool{
	// ?action=delete → delete, every other action → write.
	"POST /api/files/manager": true,
	// {"kind":"delete"} → delete, copy/move → write.
	"POST /api/files/ops": true,
}

// TestTokenSurfaces_EveryRouteAsksItsVerb walks the real router. Every route a
// token reaches as its owner refuses a token without `read`; every mutating one
// refuses a token without its own verb, before its handler runs.
func TestTokenSurfaces_EveryRouteAsksItsVerb(t *testing.T) {
	f := newVerbFixture(t)
	routes, ok := f.srv.Config.Handler.(chi.Routes)
	require.True(t, ok, "the router is no longer a chi.Routes")

	type entry struct{ method, route string }
	var table []entry
	require.NoError(t, chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		for _, p := range tokenSurfacePrefixes {
			if strings.HasPrefix(route, p) {
				if !publicUnderTokenPrefixes[method+" "+route] {
					table = append(table, entry{method, route})
				}
				return nil
			}
		}
		return nil
	}))
	sort.Slice(table, func(i, j int) bool {
		return table[i].route+table[i].method < table[j].route+table[j].method
	})
	// Anti-vacuity: the table has ~110 such routes; a small number means the
	// walk broke, not that the product shrank.
	require.Greater(t, len(table), 90, "walked %d token-surface routes", len(table))
	t.Logf("walked %d token-surface routes", len(table))

	known := map[string]bool{}
	for _, m := range []map[string]bool{readLevelMutations, deleteRoutes, handlerDecided} {
		for k := range m {
			known[k] = true
		}
	}

	noRead := testutil.NewAPIToken(t, f.store, f.memberID, "write,delete,mcp")
	noWrite := testutil.NewAPIToken(t, f.store, f.memberID, "read,delete,mcp")
	noDelete := testutil.NewAPIToken(t, f.store, f.memberID, "read,write,mcp")
	readOnly := testutil.NewAPIToken(t, f.store, f.memberID, "read")

	// {id} → a row that does not exist, so a request that DOES reach a handler
	// changes nothing.
	concrete := func(route string) string {
		p := verbRouteParam.ReplaceAllString(route, "999999")
		return strings.TrimSuffix(p, "/*")
	}
	const probe = `{"__token_verb_probe":true}`
	missing := func(body string) bool { return strings.Contains(body, "token missing scope") }

	seen := map[string]bool{}
	for _, e := range table {
		key := e.method + " " + e.route
		seen[key] = true
		url := concrete(e.route)

		code, body := f.call(t, nil, noRead, e.method, url, probe)
		if !(code == http.StatusForbidden && strings.Contains(body, "token missing scope: read")) {
			t.Errorf("%s: a token without `read` got %d %q", key, code, trim(body))
		}

		switch e.method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			continue
		}
		switch {
		case handlerDecided[key]:
			// Covered by the request-shaped tests above.
		case readLevelMutations[key]:
			if code, body := f.call(t, nil, readOnly, e.method, url, probe); missing(body) {
				t.Errorf("%s: listed as read-level, but a `read` token got %d %q", key, code, trim(body))
			}
		case deleteRoutes[key]:
			code, body := f.call(t, nil, noDelete, e.method, url, probe)
			if !(code == http.StatusForbidden && strings.Contains(body, "token missing scope: delete")) {
				t.Errorf("%s: a token without `delete` got %d %q", key, code, trim(body))
			}
		default:
			code, body := f.call(t, nil, noWrite, e.method, url, probe)
			if !(code == http.StatusForbidden && strings.Contains(body, "token missing scope: write")) {
				t.Errorf("%s: a token without `write` got %d %q — a new mutating route must ask for `write` "+
					"(or be listed in readLevelMutations / deleteRoutes with a reason)", key, code, trim(body))
			}
		}
	}
	for k := range known {
		if !seen[k] {
			t.Errorf("%s is listed in this test but no longer routed — drop it from the list", k)
		}
	}
}

var verbRouteParam = regexp.MustCompile(`\{[^}]*\}`)

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

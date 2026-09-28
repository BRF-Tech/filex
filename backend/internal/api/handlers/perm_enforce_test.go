package handlers_test

// Per-user permissions (internal/perm) at the explorer's HTTP doors.
//
// Each test takes a plain member of tenant alpha — who, with no override and
// no rule, may do everything a user always could — takes ONE permission away
// and requires that exactly the doors for that action refuse, with the
// structured `permission_denied` body the file manager reads to say why,
// while the neighbouring actions keep working.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// permFix is the tenant fixture with a member token and a seeded file.
type permFix struct {
	*mtFix
	adminTok, memberTok string
}

func newPermFix(t *testing.T) *permFix {
	t.Helper()
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	pf := &permFix{
		mtFix:     f,
		adminTok:  issueToken(t, f.Store, admin.ID, fullScopes, nil),
		memberTok: issueToken(t, f.Store, f.UserA, fullScopes, nil),
	}
	fxUpload(t, f.URL, pf.adminTok, "alpha://", "report.txt", "quarterly numbers")
	t.Cleanup(func() {
		// Rules are cached process-wide for perm.SnapshotTTL; a rule this
		// test wrote must not leak into the next one.
		perm.Invalidate()
	})
	return pf
}

// override sets the member's own overrides, as the Users page will.
func (pf *permFix) override(t *testing.T, effects map[string]string) {
	t.Helper()
	require.NoError(t, pf.Store.SetUserPermissionOverrides(context.Background(), pf.UserA, effects, nil))
	perm.Invalidate()
}

// requireDenied asserts a structured permission refusal for p.
func requireDenied(t *testing.T, what string, status int, body string, p perm.Perm, kind perm.SourceKind) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusForbidden, status, "%s: %s", what, body)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got), "%s: body is not JSON: %s", what, body)
	assert.Equal(t, "permission_denied", got["error"], what)
	assert.Equal(t, string(p), got["permission"], what)
	src, _ := got["source"].(map[string]any)
	assert.Equal(t, string(kind), src["kind"], what)
	assert.NotEmpty(t, got["message"], "%s: no sentence for the person", what)
	return got
}

func deleteItem(t *testing.T, pf *permFix, tok, rel string) (int, string) {
	return fxMutate(t, pf.URL, tok, "delete", map[string]any{
		"path": "alpha://", "items": []map[string]string{{"path": "alpha://" + rel}},
	})
}

func TestPerm_NoOverrideChangesNothing(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxMutate(t, pf.URL, pf.memberTok, "newfolder", map[string]any{"path": "alpha://", "name": "Mine"})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, pf.URL, pf.memberTok, "alpha://Mine", "a.txt", "a")
	status, body = deleteItem(t, pf, pf.memberTok, "Mine/a.txt")
	require.Equal(t, http.StatusOK, status, body)
}

func TestPerm_DeleteDenied(t *testing.T) {
	pf := newPermFix(t)
	pf.override(t, map[string]string{"files.delete": model.PermDeny})

	status, body := deleteItem(t, pf, pf.memberTok, "report.txt")
	requireDenied(t, "explorer delete", status, body, perm.FilesDelete, perm.SourceOverride)

	status, body = fxPost(t, pf.URL+"/api/files/ops", pf.memberTok, map[string]any{"kind": "delete", "storage_id": pf.StA.ID, "sources": []string{"report.txt"}})
	requireDenied(t, "queued delete", status, body, perm.FilesDelete, perm.SourceOverride)

	status, body = fxPost(t, pf.URL+"/api/files/delete", pf.memberTok, map[string]any{"source": []string{"alpha://report.txt"}})
	requireDenied(t, "per-verb delete", status, body, perm.FilesDelete, perm.SourceOverride)

	assert.FileExists(t, filepath.Join(pf.RootA, "report.txt"))

	// Neighbours still work.
	status, body = fxMutate(t, pf.URL, pf.memberTok, "newfolder", map[string]any{"path": "alpha://", "name": "Still"})
	require.Equal(t, http.StatusOK, status, "creating is not deleting: %s", body)

	// And the administrator is bound by nothing.
	status, body = deleteItem(t, pf, pf.adminTok, "report.txt")
	require.Equal(t, http.StatusOK, status, body)
}

func TestPerm_CreateDenied(t *testing.T) {
	pf := newPermFix(t)
	pf.override(t, map[string]string{"files.create": model.PermDeny})

	status, body := fxMutate(t, pf.URL, pf.memberTok, "newfolder", map[string]any{"path": "alpha://", "name": "Nope"})
	requireDenied(t, "new folder", status, body, perm.FilesCreate, perm.SourceOverride)
	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "new.txt", "x")
	requireDenied(t, "upload of a new file", status, body, perm.FilesCreate, perm.SourceOverride)
	assert.NoFileExists(t, filepath.Join(pf.RootA, "new.txt"))

	// Replacing an existing file is modify, not create — still allowed.
	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "report.txt", "revised numbers")
	require.Equal(t, http.StatusOK, status, "overwriting needs files.modify, not files.create: %s", body)
}

func TestPerm_ModifyDeniedStopsOverwriteOnly(t *testing.T) {
	pf := newPermFix(t)
	pf.override(t, map[string]string{"files.modify": model.PermDeny})

	status, body := fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "report.txt", "tampered")
	requireDenied(t, "upload over an existing file", status, body, perm.FilesModify, perm.SourceOverride)
	got, err := os.ReadFile(filepath.Join(pf.RootA, "report.txt"))
	require.NoError(t, err)
	assert.Equal(t, "quarterly numbers", string(got))

	status, body = fxPost(t, pf.URL+"/api/files/save-text", pf.memberTok, map[string]any{"path": "alpha://report.txt", "content": "edited"})
	requireDenied(t, "text editor save", status, body, perm.FilesModify, perm.SourceOverride)

	// A brand-new file is create, which the member still has.
	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "fresh.txt", "new")
	require.Equal(t, http.StatusOK, status, body)
}

func TestPerm_MoveDeniedLeavesRename(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Archive"})
	require.Equal(t, http.StatusOK, status, body)
	pf.override(t, map[string]string{"files.move": model.PermDeny})

	status, body = fxMutate(t, pf.URL, pf.memberTok, "move", map[string]any{"path": "alpha://Archive", "items": []map[string]string{{"path": "alpha://report.txt"}}})
	requireDenied(t, "move", status, body, perm.FilesMove, perm.SourceOverride)
	status, body = fxPost(t, pf.URL+"/api/files/move", pf.memberTok, map[string]any{"source": []string{"alpha://report.txt"}, "target": "alpha://Archive/"})
	requireDenied(t, "queued move", status, body, perm.FilesMove, perm.SourceOverride)
	assert.FileExists(t, filepath.Join(pf.RootA, "report.txt"))

	// A new name in the same folder is not a move.
	status, body = fxMutate(t, pf.URL, pf.memberTok, "rename", map[string]any{"path": "alpha://", "item": "alpha://report.txt", "name": "r.txt"})
	require.Equal(t, http.StatusOK, status, body)
	assert.FileExists(t, filepath.Join(pf.RootA, "r.txt"))
}

func TestPerm_RenameDeniedLeavesMove(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Archive"})
	require.Equal(t, http.StatusOK, status, body)
	pf.override(t, map[string]string{"files.rename": model.PermDeny})

	status, body = fxMutate(t, pf.URL, pf.memberTok, "rename", map[string]any{"path": "alpha://", "item": "alpha://report.txt", "name": "r.txt"})
	requireDenied(t, "rename", status, body, perm.FilesRename, perm.SourceOverride)
	assert.FileExists(t, filepath.Join(pf.RootA, "report.txt"))

	// Into another folder under the same name is a move, which they still have.
	status, body = fxMutate(t, pf.URL, pf.memberTok, "move", map[string]any{"path": "alpha://Archive", "items": []map[string]string{{"path": "alpha://report.txt"}}})
	require.Equal(t, http.StatusOK, status, body)
	assert.FileExists(t, filepath.Join(pf.RootA, "Archive", "report.txt"))

	// Copy leaves the source in place: it needs create at the destination.
	status, body = fxPost(t, pf.URL+"/api/files/copy", pf.memberTok, map[string]any{"source": []string{"alpha://Archive/report.txt"}, "target": "alpha://"})
	assert.Less(t, status, 300, "copy is not a move: %s", body)
}

func TestPerm_DownloadDeniedLeavesPreview(t *testing.T) {
	pf := newPermFix(t)
	pf.override(t, map[string]string{"files.download": model.PermDeny})

	q := url.Values{"storage": {"alpha"}, "path": {"alpha://report.txt"}}
	status, body := fxReq(t, "GET", pf.URL+"/api/files/manager?q=download&"+q.Encode(), pf.memberTok, nil, "")
	requireDenied(t, "download", status, body, perm.FilesDownload, perm.SourceOverride)

	status, body = fxPost(t, pf.URL+"/api/files/archive/download", pf.memberTok, map[string]any{"paths": []string{"alpha://report.txt"}})
	requireDenied(t, "zip download", status, body, perm.FilesDownload, perm.SourceOverride)

	status, body = fxReq(t, "GET", pf.URL+"/api/files/manager?q=preview&"+q.Encode(), pf.memberTok, nil, "")
	require.Equal(t, http.StatusOK, status, "a preview is not a download: %s", body)
	assert.Equal(t, "quarterly numbers", body)
}

func TestPerm_RoleNamesItself(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	// A role nobody holds changes nothing.
	_, err := pf.Store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Finance", Enabled: true, Permissions: perm.Standard.Without(perm.FilesPurge).Strings()})
	require.NoError(t, err)
	pf.memberRole(t, &model.PermissionRule{Name: "Contractors", Effects: map[string]string{"files.delete": model.PermDeny}})

	status, body := deleteItem(t, pf, pf.memberTok, "report.txt")
	got := requireDenied(t, "delete under a denying rule", status, body, perm.FilesDelete, perm.SourceRule)
	src := got["source"].(map[string]any)
	assert.Equal(t, "Contractors", src["rule_name"])
	assert.Contains(t, got["message"], "Contractors", "the sentence names the rule")

	// The member's own override wins over the rule.
	pf.override(t, map[string]string{"files.delete": model.PermAllow})
	status, body = deleteItem(t, pf, pf.memberTok, "report.txt")
	require.Equal(t, http.StatusOK, status, body)
}

func TestPerm_AIToolsHonourPermissions(t *testing.T) {
	pf := newPermFix(t)
	pf.override(t, map[string]string{"files.delete": model.PermDeny, "files.download": model.PermDeny})

	for _, c := range []struct {
		what, url string
		body      map[string]any
		p         perm.Perm
	}{
		{"AI delete", "/api/ai/delete", map[string]any{"path": "alpha://report.txt"}, perm.FilesDelete},
	} {
		resp := aiReq(t, http.DefaultClient, "POST", pf.URL+c.url, pf.memberTok, c.body)
		var got map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, "%s: %v", c.what, got)
		assert.Contains(t, got["error"], string(c.p), "%s: the agent is told which permission it lacks", c.what)
	}
	assert.FileExists(t, filepath.Join(pf.RootA, "report.txt"))
}

// ── 2c: the doors that are not about a path ────────────────────────────────

func TestPerm_SharingNeedsItsPermission(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://report.txt"})
	require.Less(t, status, 300, "precondition: a member may share: %s", body)

	pf.override(t, map[string]string{"share.links": model.PermDeny})
	status, body = fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://report.txt"})
	requireDenied(t, "public link", status, body, perm.ShareLinks, perm.SourceOverride)

	// A drop link is its own permission: denying download links leaves it.
	status, body = fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Inbox"})
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://Inbox", "kind": "drop"})
	require.Less(t, status, 300, "a drop link needs share.upload_links, not share.links: %s", body)

	pf.override(t, map[string]string{"share.upload_links": model.PermDeny})
	status, body = fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://Inbox", "kind": "drop"})
	requireDenied(t, "drop link", status, body, perm.ShareUploadLinks, perm.SourceOverride)
}

func TestPerm_RouteLevelGates(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	node, err := pf.Store.GetNodeByPath(ctx, pf.StA.ID, pathkey.Hash(pf.StA.ID, "/report.txt"))
	require.NoError(t, err)

	cases := []struct {
		p      perm.Perm
		method string
		path   string
		body   any
	}{
		{perm.CommentsWrite, "POST", "/api/files/comments", map[string]any{"node_id": node.ID, "body": "looks good"}},
		{perm.AccessAPI, "POST", "/api/tokens", map[string]any{"label": "ci", "scopes": "read"}},
		{perm.AccessS3, "POST", "/api/auth/s3-keys", map[string]any{"label": "backup"}},
		{perm.AccountEdit, "PATCH", "/api/auth/profile", map[string]any{"display_name": "Someone Else"}},
		{perm.AIUse, "GET", "/api/ai/root", nil},
		{perm.FilesTag, "POST", "/api/files/manager/tags", map[string]any{"node_id": node.ID, "personal": []string{"q3"}}},
	}
	// Minting credentials is a personal-caller door (RequirePersonalCaller
	// refuses any token first), so those two are asked from a signed-in
	// session, the way the account page asks them.
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	for _, c := range cases {
		t.Run(string(c.p), func(t *testing.T) {
			pf.override(t, map[string]string{string(c.p): model.PermDeny})
			var status int
			var body string
			if c.p == perm.AccessAPI || c.p == perm.AccessS3 {
				status, body = sessionJSON(t, session, c.method, pf.URL+c.path, c.body)
			} else {
				status, body = fxJSON(t, c.method, pf.URL+c.path, pf.memberTok, c.body)
			}
			requireDenied(t, string(c.p), status, body, c.p, perm.SourceOverride)
		})
	}
}

// loginSession signs in with the local driver and returns a client holding
// the session cookie.
func loginSession(t *testing.T, base, email, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := &http.Client{Jar: jar}
	status, body := sessionJSON(t, c, "POST", base+"/api/auth/login", map[string]any{"email": email, "password": password})
	require.Equal(t, http.StatusOK, status, "login: %s", body)
	return c
}

func sessionJSON(t *testing.T, c *http.Client, method, u string, payload any) (int, string) {
	t.Helper()
	var rd io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// fxJSON sends any method with an optional JSON body.
func fxJSON(t *testing.T, method, u, tok string, payload any) (int, string) {
	t.Helper()
	if payload == nil {
		return fxReq(t, method, u, tok, nil, "")
	}
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	return fxReq(t, method, u, tok, bytes.NewReader(b), "application/json")
}

// ── 2d: the delegated admin area ───────────────────────────────────────────

func TestPerm_DelegatedAdminUsers(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	admin, err := pf.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)

	// Without admin.users, the users list is closed — with a reason.
	status, body := sessionJSON(t, session, "GET", pf.URL+"/api/admin/users", nil)
	requireDenied(t, "users list, no permission", status, body, perm.AdminUsers, perm.SourceBase)

	pf.override(t, map[string]string{"admin.users": model.PermAllow})
	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/admin/users", nil)
	require.Equal(t, http.StatusOK, status, "users list with admin.users: %s", body)

	// Creating an ordinary account is the job; creating an administrator is not.
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/users", map[string]any{"email": "new@alpha.test", "password": "Str0ng!Pass", "role": "user"})
	require.Less(t, status, 300, "create a user: %s", body)
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/users", map[string]any{"email": "boss@alpha.test", "password": "Str0ng!Pass", "role": "admin"})
	require.Equal(t, http.StatusForbidden, status, "create an admin: %s", body)

	// ⚠ The takeover: resetting an administrator's password hands back the
	// new one.
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/users/"+idStr(admin.ID)+"/reset-password", nil)
	require.Equal(t, http.StatusForbidden, status, "reset an admin's password: %s", body)
	assert.NotContains(t, body, "password\":\"", "no password in a refusal")

	status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(admin.ID), map[string]any{"display_name": "pwned"})
	require.Equal(t, http.StatusForbidden, status, "edit an admin: %s", body)
	status, body = sessionJSON(t, session, "DELETE", pf.URL+"/api/admin/users/"+idStr(admin.ID), nil)
	require.Equal(t, http.StatusForbidden, status, "delete an admin: %s", body)
	status, body = sessionJSON(t, session, "PATCH", pf.URL+"/api/admin/users/"+idStr(pf.UserA), map[string]any{"role": "admin"})
	require.Equal(t, http.StatusForbidden, status, "promote oneself: %s", body)
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/users/"+idStr(admin.ID)+"/quota", map[string]any{"quota_bytes": 1})
	require.Equal(t, http.StatusForbidden, status, "an admin's quota: %s", body)

	reread, err := pf.Store.GetUser(ctx, pf.UserA)
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, reread.Role, "still not an administrator")

	// The rest of the admin area stays closed to a delegated administrator.
	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/admin/storages", nil)
	require.Equal(t, http.StatusForbidden, status, "storages are admin.full: %s", body)
	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/admin/audit", nil)
	requireDenied(t, "audit needs its own permission", status, body, perm.AdminAudit, perm.SourceBase)

	// Delegation is for a signed-in session: a token of the same account is refused.
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/users", pf.memberTok, nil)
	require.Equal(t, http.StatusForbidden, status, "delegated admin by token: %s", body)

	// And a full administrator is untouched by all of this.
	status, body = fxJSON(t, "GET", pf.URL+"/api/admin/storages", pf.adminTok, nil)
	require.Equal(t, http.StatusOK, status, body)
}

func TestPerm_DelegatedMonitorIsReadOnly(t *testing.T) {
	pf := newPermFix(t)
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)
	pf.override(t, map[string]string{"admin.monitor": model.PermAllow})

	status, body := sessionJSON(t, session, "GET", pf.URL+"/api/admin/dashboard", nil)
	require.Equal(t, http.StatusOK, status, "dashboard: %s", body)
	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/admin/sync-runs", nil)
	require.Equal(t, http.StatusOK, status, "sync runs: %s", body)
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/admin/queue/1/retry", nil)
	require.Equal(t, http.StatusForbidden, status, "retrying a job is operating, not monitoring: %s", body)
	status, body = sessionJSON(t, session, "GET", pf.URL+"/metrics", nil)
	require.Equal(t, http.StatusForbidden, status, "/metrics stays admin-only: %s", body)
}

func idStr(n int64) string { return strconv.FormatInt(n, 10) }

// ── 3: using an existing API token ─────────────────────────────────────────

func TestPerm_TokenUseFollowsAccessAPIAndDesktop(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	list := func(tok string) int {
		status, _ := fxReq(t, "GET", pf.URL+"/api/files/manager?storage="+idStr(pf.StA.ID), tok, nil, "")
		return status
	}
	require.Equal(t, http.StatusOK, list(pf.memberTok), "precondition")

	// A desktop pairing's token: same account, marked by its source.
	desktopTok := issueToken(t, pf.Store, pf.UserA, fullScopes, nil)
	res, err := pf.SQL.ExecContext(ctx, `UPDATE api_tokens SET source='desktop' WHERE token_hash=?`, apitoken.HashToken(desktopTok))
	require.NoError(t, err)
	n, _ := res.RowsAffected()
	require.EqualValues(t, 1, n)

	pf.override(t, map[string]string{"access.api": model.PermDeny})
	assert.Equal(t, http.StatusUnauthorized, list(pf.memberTok), "an API token of an account without access.api still works")
	assert.Equal(t, http.StatusOK, list(desktopTok), "access.api must not cut off the desktop app")
	resp := aiReq(t, http.DefaultClient, "GET", pf.URL+"/api/ai/root", pf.memberTok, nil)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "/api/ai took a token its owner may no longer use")

	pf.override(t, map[string]string{"access.desktop": model.PermDeny})
	assert.Equal(t, http.StatusOK, list(pf.memberTok), "access.desktop must not cut off API tokens")
	assert.Equal(t, http.StatusUnauthorized, list(desktopTok), "a desktop token of an account without access.desktop still works")
}

// ── 4c: rule settings ──────────────────────────────────────────────────────

// memberRole creates an enabled role and gives it to the member — the one
// role they hold.
func (pf *permFix) memberRole(t *testing.T, r *model.PermissionRule) *model.PermissionRule {
	t.Helper()
	r.Enabled = true
	// Written as changes to the User role: its own list is the Standard
	// preset with the account-wide changes applied.
	if r.Permissions == nil {
		set := perm.Standard
		if r.Conditions.Empty() {
			for k, eff := range r.Effects {
				if eff == model.PermAllow {
					set = set.With(perm.Perm(k))
				} else {
					set = set.Without(perm.Perm(k))
				}
			}
			r.Effects = nil
		}
		r.Permissions = set.Strings()
	}
	created, err := pf.Store.CreatePermissionRule(context.Background(), r)
	require.NoError(t, err)
	require.NoError(t, pf.Store.SetUserCustomRole(context.Background(), pf.UserA, created.ID))
	perm.Invalidate()
	return created
}

// ruleForMember gives the member a role carrying only these settings.
func (pf *permFix) ruleForMember(t *testing.T, settings model.PermRuleSettings) {
	t.Helper()
	pf.memberRole(t, &model.PermissionRule{Name: "Policy", Settings: settings})
}

func TestPerm_LinkPolicyCapsAndLocks(t *testing.T) {
	pf := newPermFix(t)
	days := 7
	pf.ruleForMember(t, model.PermRuleSettings{ShareLinkMaxDays: &days, ShareLinkPasswordRequired: true})

	status, body := fxPost(t, pf.URL+"/api/files/share", pf.memberTok, map[string]any{"path": "alpha://report.txt"})
	require.Less(t, status, 300, body)
	sh := decode(t, body)["share"].(map[string]any)
	assert.Equal(t, true, sh["has_pin"], "the rule requires a password")
	assert.NotEmpty(t, sh["password_pin"], "the generated PIN goes back to the creator")
	assert.Equal(t, true, sh["expiry_clamped"], "a link asked for with no expiry got the rule's")
	exp, err := time.Parse(time.RFC3339, sh["expires_at"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(7*24*time.Hour), exp, time.Minute)

	// The administrator is bound by no rule.
	status, body = fxPost(t, pf.URL+"/api/files/share", pf.adminTok, map[string]any{"path": "alpha://report.txt"})
	require.Less(t, status, 300, body)
	assert.Equal(t, false, decode(t, body)["share"].(map[string]any)["has_pin"])
}

func TestPerm_BlockedFileTypes(t *testing.T) {
	pf := newPermFix(t)
	pf.ruleForMember(t, model.PermRuleSettings{BlockedExtensions: []string{"exe", "tar.gz"}})

	for _, name := range []string{"setup.EXE", "backup.tar.gz"} {
		status, body := fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", name, "x")
		require.Equal(t, http.StatusForbidden, status, "%s: %s", name, body)
		got := decode(t, body)
		assert.Equal(t, "blocked_file_type", got["error"], name)
		assert.NotEmpty(t, got["message"])
		assert.NoFileExists(t, filepath.Join(pf.RootA, name))
	}
	status, body := fxMutate(t, pf.URL, pf.memberTok, "rename", map[string]any{"path": "alpha://", "item": "alpha://report.txt", "name": "report.exe"})
	require.Equal(t, http.StatusForbidden, status, "renaming INTO a blocked type: %s", body)
	assert.Equal(t, "blocked_file_type", decode(t, body)["error"])

	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "notes.txt", "fine")
	require.Equal(t, http.StatusOK, status, "other types are untouched: %s", body)
}

func TestPerm_MaxUploadSize(t *testing.T) {
	pf := newPermFix(t)
	max := int64(8)
	pf.ruleForMember(t, model.PermRuleSettings{MaxUploadBytes: &max})

	status, body := fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "big.txt", "0123456789")
	require.Equal(t, http.StatusRequestEntityTooLarge, status, body)
	assert.Equal(t, "FILE_TOO_LARGE", decode(t, body)["code"])
	assert.NoFileExists(t, filepath.Join(pf.RootA, "big.txt"))

	status, body = fxPost(t, pf.URL+"/api/files/save-text", pf.memberTok, map[string]any{"path": "alpha://note.txt", "content": "far too long for eight bytes"})
	require.Equal(t, http.StatusRequestEntityTooLarge, status, "the text editor: %s", body)

	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://", "small.txt", "tiny")
	require.Equal(t, http.StatusOK, status, body)
}

func TestPerm_Require2FAHoldsTheSessionAtEnrolment(t *testing.T) {
	pf := newPermFix(t)
	pf.ruleForMember(t, model.PermRuleSettings{Require2FA: true})
	session := loginSession(t, pf.URL, "member@alpha.test", mtUserPass)

	status, body := sessionJSON(t, session, "GET", pf.URL+"/api/files/manager?storage="+idStr(pf.StA.ID), nil)
	require.Equal(t, http.StatusForbidden, status, "a session that must enrol first: %s", body)
	assert.Contains(t, body, "2fa_required")

	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/auth/me", nil)
	require.Equal(t, http.StatusOK, status, "who am I stays open: %s", body)
	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/auth/me/permissions", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, true, decode(t, body)["settings"].(map[string]any)["require_2fa"], "the web app can tell why")
	status, body = sessionJSON(t, session, "POST", pf.URL+"/api/auth/totp/enroll", nil)
	require.Less(t, status, 300, "enrolment stays open: %s", body)

	// Enrolled, the session is whole again.
	require.NoError(t, pf.Store.SetTotpPendingSecret(context.Background(), pf.UserA, "JBSWY3DPEHPK3PXP", nil))
	require.NoError(t, pf.Store.ActivateTotp(context.Background(), pf.UserA))
	status, body = sessionJSON(t, session, "GET", pf.URL+"/api/files/manager?storage="+idStr(pf.StA.ID), nil)
	require.Equal(t, http.StatusOK, status, "an enrolled account: %s", body)
}

// ── 4d: rules limited to storages and paths ────────────────────────────────

func TestPerm_PathConditionedRule(t *testing.T) {
	pf := newPermFix(t)
	status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": "Locked"})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, pf.URL, pf.adminTok, "alpha://Locked", "signed.pdf", "signed")

	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "Locked is read-only", "enabled": true,
		"permissions": perm.Standard.Strings(),
		"effects":     map[string]string{"files.delete": "deny", "files.modify": "deny", "files.download": "deny"},
		"conditions":  map[string]any{"storage_ids": []int64{pf.StA.ID}, "paths": []string{"Locked"}},
	})
	require.Equal(t, http.StatusCreated, status, body)
	status, body = fxJSON(t, "PUT", pf.URL+"/api/admin/users/"+idStr(pf.UserA)+"/roles", pf.adminTok,
		map[string]any{"role_id": decode(t, body)["id"]})
	require.Equal(t, http.StatusOK, status, body)

	status, body = deleteItem(t, pf, pf.memberTok, "Locked/signed.pdf")
	got := requireDenied(t, "delete under Locked", status, body, perm.FilesDelete, perm.SourceRule)
	assert.Equal(t, "Locked is read-only", got["source"].(map[string]any)["rule_name"], "the refusal names the path rule")
	assert.FileExists(t, filepath.Join(pf.RootA, "Locked", "signed.pdf"))

	status, body = fxUploadStatus(t, pf.URL, pf.memberTok, "alpha://Locked", "signed.pdf", "tampered")
	requireDenied(t, "overwrite under Locked", status, body, perm.FilesModify, perm.SourceRule)
	status, body = fxPost(t, pf.URL+"/api/files/archive/download", pf.memberTok, map[string]any{"paths": []string{"alpha://Locked/signed.pdf"}})
	requireDenied(t, "zip download under Locked", status, body, perm.FilesDownload, perm.SourceRule)

	// Everywhere else the account is untouched.
	status, body = deleteItem(t, pf, pf.memberTok, "report.txt")
	require.Equal(t, http.StatusOK, status, "outside the path: %s", body)
	status, body = fxJSON(t, "GET", pf.URL+"/api/auth/me/permissions", pf.memberTok, nil)
	require.Equal(t, http.StatusOK, status)
	me := decode(t, body)
	assert.Contains(t, me["allowed"], "files.delete", "account-wide, a path rule changes nothing")
	assert.Len(t, me["conditional_rules"], 1)

	// A path rule may only change file actions.
	status, body = fxJSON(t, "POST", pf.URL+"/api/admin/roles", pf.adminTok, map[string]any{
		"name": "bad", "enabled": true,
		"effects":    map[string]string{"access.sftp": "deny"},
		"conditions": map[string]any{"paths": []string{"Locked"}},
	})
	require.Equal(t, http.StatusBadRequest, status, body)
}

// A role that allows Delete only in Scratch: /me names Delete as varying by
// folder, and ?action=allowed answers it per path, in the order asked.
func TestPerm_AllowedAnswersPerPath(t *testing.T) {
	pf := newPermFix(t)
	pf.memberRole(t, &model.PermissionRule{Name: "NoDelete",
		Permissions: []string{"files.download", "files.create", "access.api"},
		Conditions:  model.PermRuleConditions{Paths: []string{"Scratch/**"}},
		Effects:     map[string]string{"files.delete": model.PermAllow}})

	status, body := fxJSON(t, "GET", pf.URL+"/api/auth/me", pf.memberTok, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, []any{"files.delete"}, decode(t, body)["permissions_by_folder"])

	status, body = fxMutate(t, pf.URL, pf.memberTok, "allowed", map[string]any{
		"permissions": []string{"files.delete", "files.download", "no.such"},
		"items": []map[string]string{
			{"path": "alpha://Scratch/draft.txt"}, {"path": "alpha://report.txt"}, {"path": "nowhere://x"},
		},
	})
	require.Equal(t, http.StatusOK, status, body)
	var got struct{ Allowed [][]string }
	require.NoError(t, json.Unmarshal([]byte(body), &got), body)
	assert.Equal(t, [][]string{{"files.delete", "files.download"}, {"files.download"}, {}}, got.Allowed)
}

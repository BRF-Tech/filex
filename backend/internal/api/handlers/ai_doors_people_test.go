package handlers_test

// The bell, stars, comments and item permissions from the AI surface (task
// #119, item 4): notifications_list / notification_read, file_star,
// file_comments / file_comment_add / file_comment_delete, file_permissions /
// file_permission_users / file_permission_set / file_permission_revoke, and
// their REST twins under /api/ai. Measured before 0.50: none of these tools
// existed (the coverage audit's E13). Each test asserts what the call DID -
// rows in the bell, a star in the explorer's list, a grant that opens a
// folder - and on the old code the tool is a JSON-RPC error.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// notice puts one notice in a person's bell; node names the file it is about
// ("" = none).
func (f *doorFix) notice(t *testing.T, userID int64, title, node string) int64 {
	t.Helper()
	in := &model.NotificationInput{Event: "admin_test", Severity: "info", Title: title, UserID: &userID}
	if node != "" {
		meta, err := json.Marshal(map[string]any{"node": map[string]any{"storage_id": f.Main.ID, "path": node, "name": node[strings.LastIndex(node, "/")+1:]}})
		require.NoError(t, err)
		in.MetaJSON = meta
	}
	id, err := f.Store.InsertNotification(context.Background(), in)
	require.NoError(t, err)
	return id
}

// TestAIDoors_TheBellIsTheCallersAndItsRoots - notifications_list is the
// caller's own bell, narrowed to a `root:` token's folder as /api/notifications
// is; notification_read marks one read and writes no audit row.
func TestAIDoors_TheBellIsTheCallersAndItsRoots(t *testing.T) {
	f := newDoorFix(t)
	genel := f.notice(t, f.MemberID, "Genel duyuru", "")
	f.notice(t, f.MemberID, "Kutudaki dosya", "/kutu/ic.txt")
	f.notice(t, f.MemberID, "Disaridaki dosya", "/disari/gizli.txt")
	f.notice(t, f.Member2, "Baskasinin bildirimi", "")

	tl := f.tool(t, f.Tok, "notifications_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	bell := string(tl.Structured)
	for _, want := range []string{"Genel duyuru", "Kutudaki dosya", "Disaridaki dosya"} {
		assert.Contains(t, bell, want)
	}
	assert.NotContains(t, bell, "Baskasinin bildirimi", "another person's notice is never in the bell")

	confined := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,mcp,root:main://kutu")
	tl = f.tool(t, confined, "notifications_list", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	bell = string(tl.Structured)
	assert.Contains(t, bell, "Genel duyuru", "a notice that names no file stays readable")
	assert.Contains(t, bell, "Kutudaki dosya")
	assert.NotContains(t, bell, "Disaridaki dosya", "a confined token reads only the notices about its folder")

	before := len(mcpAuditRows(t, f.Store))
	tl = f.tool(t, f.Tok, "notification_read", map[string]any{"id": genel})
	require.False(t, tl.IsError, tl.Text)
	tl = f.tool(t, f.Tok, "notifications_list", map[string]any{"unread": true})
	require.False(t, tl.IsError, tl.Text)
	assert.NotContains(t, string(tl.Structured), "Genel duyuru", "marked read")
	assert.Contains(t, string(tl.Structured), "Kutudaki dosya", "only the one asked for")
	// The twin answers what the bell's own route answers: 204, no body.
	code, out := f.rest(t, f.Tok, http.MethodPost, "/api/ai/notifications/read", map[string]any{"all": true})
	require.Equal(t, http.StatusNoContent, code, "%v", out)
	tl = f.tool(t, f.Tok, "notifications_list", map[string]any{"unread": true})
	require.False(t, tl.IsError, tl.Text)
	assert.NotContains(t, string(tl.Structured), "Kutudaki dosya", "all: true marked the rest read too")
	assert.Empty(t, newRows(t, f.Store, before), "marking one's own notices read is not audited, on either door")

	// Nothing to mark is the caller's mistake.
	code, _ = f.rest(t, f.Tok, http.MethodPost, "/api/ai/notifications/read", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestAIDoors_StarAndComments - file_star stars a file in the explorer's own
// list; a comment is added, read and deleted by its author only, and each
// write is the audit row its REST twin leaves.
//
// Commenting asks the token's `comments:rw` (task #157), which the fixture's
// tokens do not name: the comment calls run on tokens that do
// (token_comments_test.go holds the refusal).
func TestAIDoors_StarAndComments(t *testing.T) {
	f := newDoorFix(t)
	c1 := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,write,delete,mcp,comments:rw")
	c2 := testutil.NewAPIToken(t, f.Store, f.Member2, "read,write,delete,mcp,comments:rw")
	f.put(t, f.Tok, "main://docs/a.txt", "x")
	f.put(t, f.Tok, "main://docs/b.txt", "y")

	tl := f.tool(t, f.Tok, "file_star", map[string]any{"path": "main://docs/a.txt"})
	require.False(t, tl.IsError, tl.Text)
	resp := aiReq(t, http.DefaultClient, http.MethodGet, f.URL+"/api/files/manager/star/list", f.Tok, nil)
	var starred map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&starred))
	resp.Body.Close()
	raw, _ := json.Marshal(starred)
	assert.Contains(t, string(raw), "a.txt", "the explorer's Starred lists it")
	assert.NotContains(t, string(raw), "b.txt")
	tl = f.tool(t, f.Tok, "file_star", map[string]any{"path": "main://docs/a.txt", "star": false})
	require.False(t, tl.IsError, tl.Text)

	before := len(mcpAuditRows(t, f.Store))
	tl = f.tool(t, c1, "file_comment_add", map[string]any{"path": "main://docs/a.txt", "text": "ilk yorum"})
	require.False(t, tl.IsError, tl.Text)
	rows := newRows(t, f.Store, before)
	require.Len(t, rows, 1, "%+v", rows)
	assert.Equal(t, "ai.file.comment_add", rows[0].Action)
	assert.Equal(t, "mcp", rows[0].Metadata["via"])

	before = len(mcpAuditRows(t, f.Store))
	code, out := f.rest(t, c1, http.MethodPost, "/api/ai/comments", map[string]any{"path": "main://docs/b.txt", "text": "rest yorumu"})
	require.Less(t, code, 300, "%v", out)
	rows = newRows(t, f.Store, before)
	require.Len(t, rows, 1)
	assert.Equal(t, "ai.file.comment_add", rows[0].Action, "the REST twin's row is the tool's")
	assert.Equal(t, "api", rows[0].Metadata["via"])

	tl = f.tool(t, c2, "file_comments", map[string]any{"path": "main://docs/a.txt"})
	require.False(t, tl.IsError, tl.Text)
	_, res := doorResult(t, tl)
	list, _ := res["comments"].([]any)
	require.Len(t, list, 1, "%v", res)
	cid := list[0].(map[string]any)["id"].(float64)
	assert.Equal(t, "ilk yorum", list[0].(map[string]any)["body"])

	tl = f.tool(t, c2, "file_comment_delete", map[string]any{"id": cid})
	require.True(t, tl.IsError, "only its author deletes a comment: %s", tl.Raw)
	tl = f.tool(t, c1, "file_comment_delete", map[string]any{"id": cid})
	require.False(t, tl.IsError, tl.Text)
	tl = f.tool(t, c1, "file_comments", map[string]any{"path": "main://docs/a.txt"})
	_, res = doorResult(t, tl)
	list, _ = res["comments"].([]any)
	assert.Empty(t, list, "deleted")
}

// TestAIDoors_PermissionsAreTheOwners - on a storage with access control the
// owner grants and takes access through the tools, and the grant really opens
// the folder; a member who is not the owner can do neither.
func TestAIDoors_PermissionsAreTheOwners(t *testing.T) {
	f := newDoorFix(t)
	admin := testutil.NewAPIToken(t, f.Store, f.AdminID, "read,write,mcp")
	f.put(t, admin, "rbac://proje/plan.txt", "plan")

	tl := f.tool(t, f.Tok, "file_list", map[string]any{"path": "rbac://proje"})
	require.True(t, tl.IsError, "no grant yet: %s", tl.Raw)

	tl = f.tool(t, admin, "file_permission_users", map[string]any{"q": "door-member@"})
	require.False(t, tl.IsError, tl.Text)
	assert.Contains(t, string(tl.Structured), "door-member@test.local")

	tl = f.tool(t, f.Tok, "file_permission_set", map[string]any{"path": "rbac://proje", "user_id": f.Member2, "level": "viewer"})
	require.True(t, tl.IsError, "a member who is not the owner grants nothing: %s", tl.Raw)

	before := len(mcpAuditRows(t, f.Store))
	tl = f.tool(t, admin, "file_permission_set", map[string]any{"path": "rbac://proje", "user_id": f.MemberID, "level": "viewer"})
	require.False(t, tl.IsError, tl.Text)
	rows := newRows(t, f.Store, before)
	require.Len(t, rows, 1)
	assert.Equal(t, "ai.file.grant_set", rows[0].Action)

	tl = f.tool(t, f.Tok, "file_list", map[string]any{"path": "rbac://proje"})
	require.False(t, tl.IsError, "the grant opened the folder: %s", tl.Text)
	assert.Contains(t, string(tl.Structured), "plan.txt")

	tl = f.tool(t, admin, "file_permissions", map[string]any{"path": "rbac://proje"})
	require.False(t, tl.IsError, tl.Text)
	_, res := doorResult(t, tl)
	direct, _ := res["direct"].([]any)
	var gid float64
	for _, g := range direct {
		m := g.(map[string]any)
		if m["user_id"] == float64(f.MemberID) {
			gid, _ = m["id"].(float64)
		}
	}
	require.NotZero(t, gid, "the grant is listed on the folder: %v", res)

	tl = f.tool(t, admin, "file_permission_revoke", map[string]any{"id": gid})
	require.False(t, tl.IsError, tl.Text)
	tl = f.tool(t, f.Tok, "file_list", map[string]any{"path": "rbac://proje"})
	assert.True(t, tl.IsError, "revoked: the folder is closed again: %s", tl.Raw)
}

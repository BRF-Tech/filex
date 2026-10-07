package handlers_test

// Task #157 - comments are an API key's own permission. Adding and deleting a
// comment ask the key's `comments` permission at `rw` (`comments:rw` in its
// list) on every door - the explorer's /api/files/comments, /api/ai/comments
// and the MCP file_comment_* tools - and the verb `write` does not stand in
// for it. A key that does not name the permission holds it at `read`: every
// key from before, and a new one minted without choosing (package tokenperm,
// docs/RBAC.md → Permissions with a level).
//
// Measured before the change: /api/files/comments let a `read` token add and
// delete (it was read-level), /api/ai and MCP asked `write` - the same key
// could comment through one door and not through its twin.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

const gizliPath = "rbac://docs/gizli.txt"

// liveComments is how many comments the fixture's secret file has.
func (f *verbFixture) liveComments(t *testing.T) int {
	t.Helper()
	list, err := f.store.ListNodeComments(context.Background(), f.secretID)
	require.NoError(t, err)
	n := 0
	for _, c := range list {
		if c.DeletedAt == nil {
			n++
		}
	}
	return n
}

func (f *verbFixture) mcpCall(t *testing.T, tok, payload string) string {
	t.Helper()
	code, body := mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", tok, payload)
	require.Equal(t, http.StatusOK, code, body)
	return body
}

func commentAddCall(path string) string {
	return `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"file_comment_add","arguments":{"path":"` + path + `","text":"mcp yorumu"}}}`
}

// commentID reads the id out of POST /api/files/comments' answer.
func commentID(t *testing.T, body string) int64 {
	t.Helper()
	var out struct {
		Comment struct {
			ID int64 `json:"id"`
		} `json:"comment"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out), body)
	require.NotZero(t, out.Comment.ID, body)
	return out.Comment.ID
}

// TestComments_WriteDoesNotStandInForTheCommentsPermission - a key with every
// verb and no comments level - the shape of every key minted before 0.53 -
// reads comments on all three doors and adds one on none of them.
func TestComments_WriteDoesNotStandInForTheCommentsPermission(t *testing.T) {
	f := newVerbFixture(t)
	tok := testutil.NewAPIToken(t, f.store, f.editorID, "read,write,delete,mcp")
	nodeBody := fmt.Sprintf(`{"node_id":%d,"body":"yazamam"}`, f.secretID)

	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/comments", nodeBody)
	refusedFor(t, "comments:write", code, body, "/api/files/comments")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/ai/comments", `{"path":"`+gizliPath+`","text":"yazamam"}`)
	refusedFor(t, "comments:write", code, body, "/api/ai/comments")

	list := f.mcpCall(t, tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	assert.Contains(t, list, `"file_comments"`, "reading comments needs `read` only")
	assert.NotContains(t, list, `"file_comment_add"`, "not offered without comments:rw")
	assert.NotContains(t, list, `"file_comment_delete"`, "not offered without comments:rw")
	body = f.mcpCall(t, tok, commentAddCall(gizliPath))
	assert.NotContains(t, body, `"isError":false`, "MCP: file_comment_add must not run; body: %s", body)

	assert.Zero(t, f.liveComments(t), "no door wrote a comment")

	// Reading stays open on every door.
	code, body = f.call(t, nil, tok, http.MethodGet, fmt.Sprintf("/api/files/comments?node_id=%d", f.secretID), "")
	assert.Equal(t, http.StatusOK, code, "read on /api/files: %s", body)
	code, body = f.call(t, nil, tok, http.MethodGet, "/api/ai/comments?path="+gizliPath, "")
	assert.Equal(t, http.StatusOK, code, "read on /api/ai: %s", body)
	tl := doorCall(t, f.srv.URL, tok, "file_comments", map[string]any{"path": gizliPath})
	assert.False(t, tl.IsError, "read over MCP: %s", tl.Raw)
}

// TestComments_RWCommentsOnEveryDoorWithoutWrite - `comments:rw` adds and
// deletes on all three doors, with `read` and no `write`; and it is not
// `write`: the same key creates no folder.
func TestComments_RWCommentsOnEveryDoorWithoutWrite(t *testing.T) {
	f := newVerbFixture(t)
	tok := testutil.NewAPIToken(t, f.store, f.editorID, "read,mcp,comments:rw")

	code, body := f.call(t, nil, tok, http.MethodPost, "/api/files/comments", fmt.Sprintf(`{"node_id":%d,"body":"rest"}`, f.secretID))
	require.Equal(t, http.StatusOK, code, "/api/files/comments: %s", body)
	first := commentID(t, body)

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/ai/comments", `{"path":"`+gizliPath+`","text":"ai"}`)
	require.Less(t, code, 300, "/api/ai/comments: %s", body)

	tl := doorCall(t, f.srv.URL, tok, "file_comment_add", map[string]any{"path": gizliPath, "text": "mcp"})
	require.False(t, tl.IsError, "MCP file_comment_add: %s", tl.Raw)
	require.Equal(t, 3, f.liveComments(t), "one comment from each door")

	list, err := f.store.ListNodeComments(context.Background(), f.secretID)
	require.NoError(t, err)
	var others []int64
	for _, c := range list {
		if c.ID != first {
			others = append(others, c.ID)
		}
	}
	require.Len(t, others, 2, "%+v", list)

	code, body = f.call(t, nil, tok, http.MethodDelete, fmt.Sprintf("/api/files/comments/%d", first), "")
	assert.Equal(t, http.StatusOK, code, "delete on /api/files: %s", body)
	code, body = f.call(t, nil, tok, http.MethodPost, fmt.Sprintf("/api/ai/comments/%d/delete", others[0]), "")
	assert.Less(t, code, 300, "delete on /api/ai: %s", body)
	tl = doorCall(t, f.srv.URL, tok, "file_comment_delete", map[string]any{"id": others[1]})
	assert.False(t, tl.IsError, "delete over MCP: %s", tl.Raw)
	assert.Zero(t, f.liveComments(t), "each door deleted its comment")

	code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/manager?action=newfolder", newFolderBody("yorumcu"))
	refusedFor(t, "write", code, body, "comments:rw is not write")
	assert.False(t, f.exists("docs/yorumcu"))
}

// TestComments_AKeyFromBeforeHoldsRead - no migration rewrites the stored
// lists: a row as 0.52 left it (the full list migration 00054 wrote, a
// desktop pairing's, a read-only one) holds comments at `read`, says so in
// the key list, reads comments and adds none.
func TestComments_AKeyFromBeforeHoldsRead(t *testing.T) {
	f := newVerbFixture(t)
	s := f.session(t, verbEditorEmail)
	for _, scopes := range []string{"read,write,delete,mcp,admin", "read,write,delete", "read"} {
		tok := testutil.NewAPIToken(t, f.store, f.editorID, scopes)

		code, body := f.call(t, nil, tok, http.MethodGet, fmt.Sprintf("/api/files/comments?node_id=%d", f.secretID), "")
		assert.Equal(t, http.StatusOK, code, "%s reads comments: %s", scopes, body)
		code, body = f.call(t, nil, tok, http.MethodPost, "/api/files/comments", fmt.Sprintf(`{"node_id":%d,"body":"eski"}`, f.secretID))
		refusedFor(t, "comments:write", code, body, scopes+": add a comment")
	}
	assert.Zero(t, f.liveComments(t))

	code, body := f.call(t, s, "", http.MethodGet, "/api/tokens", "")
	require.Equal(t, http.StatusOK, code, body)
	var out struct {
		Tokens []struct {
			Scopes      string            `json:"scopes"`
			Permissions map[string]string `json:"permissions"`
		} `json:"tokens"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out), body)
	require.Len(t, out.Tokens, 3, body)
	for _, row := range out.Tokens {
		assert.Equal(t, "read", row.Permissions["comments"], "%s: the list says comments at read", row.Scopes)
	}

	// The browser session of the same account comments as before.
	code, body = f.call(t, s, "", http.MethodPost, "/api/files/comments", fmt.Sprintf(`{"node_id":%d,"body":"oturum"}`, f.secretID))
	assert.Equal(t, http.StatusOK, code, "a session is judged by the account: %s", body)
}

// TestComments_TheKeyScreensSetTheLevel - a key is minted with
// `comments:rw` or without it (read), and an existing key's level is changed
// with PATCH {permissions} on /api/tokens and /api/admin/ai-tokens - the verbs
// stay as they were. A key never raises a key above itself: not a new one,
// not another of its owner's, not its own.
func TestComments_TheKeyScreensSetTheLevel(t *testing.T) {
	f := newVerbFixture(t)
	s := f.session(t, verbEditorEmail)
	nodeBody := fmt.Sprintf(`{"node_id":%d,"body":"duzenlendi"}`, f.secretID)

	type minted struct {
		Token string `json:"token"`
		Row   struct {
			ID          int64             `json:"id"`
			Scopes      string            `json:"scopes"`
			Permissions map[string]string `json:"permissions"`
		} `json:"row"`
	}
	mint := func(c *http.Client, tok, scopes string) (int, minted, string) {
		code, body := f.call(t, c, tok, http.MethodPost, "/api/tokens", `{"label":"yorum","scopes":"`+scopes+`"}`)
		var m minted
		if code == http.StatusCreated {
			require.NoError(t, json.Unmarshal([]byte(body), &m), body)
		}
		return code, m, body
	}

	code, rw, body := mint(s, "", "read,comments:rw")
	require.Equal(t, http.StatusCreated, code, body)
	assert.Equal(t, "read,comments:rw", rw.Row.Scopes)
	assert.Equal(t, "rw", rw.Row.Permissions["comments"])

	code, ro, body := mint(s, "", "read")
	require.Equal(t, http.StatusCreated, code, body)
	assert.Equal(t, "read", ro.Row.Scopes, "the default level is not written")
	assert.Equal(t, "read", ro.Row.Permissions["comments"])

	code, body = f.call(t, nil, rw.Token, http.MethodPost, "/api/files/comments", nodeBody)
	assert.Equal(t, http.StatusOK, code, "minted with comments:rw: %s", body)
	code, body = f.call(t, nil, ro.Token, http.MethodPost, "/api/files/comments", nodeBody)
	refusedFor(t, "comments:write", code, body, "minted without")

	// A key cannot raise itself, nor mint wider than itself (token_ceiling).
	code, body = f.call(t, nil, ro.Token, http.MethodPatch, fmt.Sprintf("/api/tokens/%d", ro.Row.ID), `{"permissions":{"comments":"rw"}}`)
	assert.Equal(t, http.StatusForbidden, code, "a read key raising itself: %s", body)
	assert.Contains(t, body, "token_ceiling")
	code, _, body = mint(nil, ro.Token, "read,comments:rw")
	assert.Equal(t, http.StatusForbidden, code, "a read key minting a comments:rw key: %s", body)
	assert.Contains(t, body, "token_ceiling")

	// The person raises it (session), and the key comments from then on.
	code, body = f.call(t, s, "", http.MethodPatch, fmt.Sprintf("/api/tokens/%d", ro.Row.ID), `{"permissions":{"comments":"rw"}}`)
	require.Equal(t, http.StatusOK, code, body)
	code, body = f.call(t, nil, ro.Token, http.MethodPost, "/api/files/comments", nodeBody)
	assert.Equal(t, http.StatusOK, code, "raised to rw: %s", body)
	row, err := f.store.GetAPITokenByID(context.Background(), ro.Row.ID)
	require.NoError(t, err)
	assert.Equal(t, "read,comments:rw", row.Scopes, "the verbs stay as they were")

	// …and takes it back.
	code, body = f.call(t, s, "", http.MethodPatch, fmt.Sprintf("/api/tokens/%d", ro.Row.ID), `{"permissions":{"comments":"read"}}`)
	require.Equal(t, http.StatusOK, code, body)
	code, body = f.call(t, nil, ro.Token, http.MethodPost, "/api/files/comments", nodeBody)
	refusedFor(t, "comments:write", code, body, "back to read")

	for _, bad := range []string{`{"permissions":{"comments":"admin"}}`, `{"permissions":{"nosuch":"rw"}}`} {
		code, body = f.call(t, s, "", http.MethodPatch, fmt.Sprintf("/api/tokens/%d", ro.Row.ID), bad)
		assert.Equal(t, http.StatusBadRequest, code, "%s: %s", bad, body)
	}

	// The administrator's screen sets it too.
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	admin := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, admin, "admin@test.local", "TestAdminPass!1")
	code, body = f.call(t, admin, "", http.MethodPatch, fmt.Sprintf("/api/admin/ai-tokens/%d", ro.Row.ID), `{"permissions":{"comments":"rw"}}`)
	require.Equal(t, http.StatusOK, code, body)
	code, body = f.call(t, nil, ro.Token, http.MethodPost, "/api/files/comments", nodeBody)
	assert.Equal(t, http.StatusOK, code, "raised by the administrator: %s", body)
}

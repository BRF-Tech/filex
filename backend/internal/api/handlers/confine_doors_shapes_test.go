package handlers_test

// GHSA-8gvc-6w52-6c7j, the other /api/files doors (2026-10-04).
//
// confine.Middleware confines a `root:` token's paths by rewriting `?path=`
// and the path keys of a body labelled JSON. Every door below decodes its body
// whatever the label, and none of them asked the root itself, so the same
// body sent as text/plain (or with no Content-Type) reached files outside the
// token's folder: the queue's copy / move / delete, the text editor's save,
// the grants and invitations, the encrypted-folder doors, a comment's delete
// by id. Two more reached outside even in JSON: an upload init's `filename`
// and `storage_id`, and a POST /ops path the checks read without its
// `x://` prefix while the queue read it whole. Each test is red while the
// request is carried out, and shows the same shape still works in the root.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// cdEventually waits for a queued job to show its effect.
func cdEventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(msg)
}

func cdRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err, rel)
	return string(b)
}

// The per-verb queue doors (the explorer's paste and delete).
func TestConfineDoors_TheQueueVerbsStayInTheRoot(t *testing.T) {
	f, tok := confinedFix(t)
	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", "application/json",
		[]byte(`{"path":"main://kutu","name":"hedef"}`))
	require.Equal(t, http.StatusOK, code, raw)

	for _, ct := range []string{"text/plain", ""} {
		for _, s := range []struct{ label, route, body string }{
			{"delete outside", "/api/files/delete", `{"source":["main://disari/gizli.txt"]}`},
			{"move in from outside", "/api/files/move", `{"source":["main://disari/gizli.txt"],"target":"main://kutu/hedef"}`},
			{"copy in from outside", "/api/files/copy", `{"source":["main://disari/gizli.txt"],"target":"main://kutu/hedef"}`},
			{"copy out", "/api/files/copy", `{"source":["main://kutu/ic.txt"],"target":"main://disari"}`},
			{"move out", "/api/files/move", `{"source":["main://kutu/ic.txt"],"target":"main://disari"}`},
		} {
			code, raw := confRaw(t, f.URL, tok, http.MethodPost, s.route, ct, []byte(s.body))
			assert.Equal(t, http.StatusForbidden, code, "%s (%q): %s", s.label, ct, raw)
		}
	}
	settle()
	assert.True(t, confExists(f.RootMain, "disari/gizli.txt"), "the file outside the root stays")
	assert.False(t, confExists(f.RootMain, "kutu/hedef/gizli.txt"), "nothing is brought in from outside")
	assert.False(t, confExists(f.RootMain, "disari/ic.txt"), "nothing is put outside")
	assert.True(t, confExists(f.RootMain, "kutu/ic.txt"), "the file inside stays")

	// Inside the root the same text/plain body is queued and carried out.
	code, raw = confRaw(t, f.URL, tok, http.MethodPost, "/api/files/copy", "text/plain",
		[]byte(`{"source":["main://kutu/ic.txt"],"target":"main://kutu/hedef"}`))
	require.Equal(t, http.StatusAccepted, code, raw)
	cdEventually(t, func() bool { return confExists(f.RootMain, "kutu/hedef/ic.txt") }, "a copy inside the root must still be carried out")
}

// POST /ops judged a path without its `x://` prefix and queued it with it: a
// driver reads `disari/alt://kutu/` as the folder `disari/alt:` and below it.
// For a `root:` token that is a write outside its folder (in a body the
// middleware does not read); for anybody, a write where they hold no grant.
func TestConfineDoors_AnOpIsCarriedOutWhereItWasJudged(t *testing.T) {
	f, tok := confinedFix(t)
	ctx := context.Background()

	body := fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/ic.txt"],"dest":"disari/alt://kutu/"}`, f.Main.ID)
	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/ops", "text/plain", []byte(body))
	// Refused before it is read as a path at all (0.52.0): to a root token the
	// `dest` names another storage, and it gets the answer the same body gets
	// as JSON (confine_ops_one_answer_test.go). A caller with no root is
	// answered 400 BAD_PATH below.
	assert.Equal(t, http.StatusForbidden, code, "a root token, text/plain: %s", raw)

	// An unconfined member holding a grant on rbac://kutu alone.
	f.put(t, f.AdminTk, "rbac://kutu/a.txt", "rbac icerik")
	_, err := f.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: f.Rbac.ID, PathPrefix: "kutu", IsDir: true, UserID: f.MemberID, Level: model.GrantEditor})
	require.NoError(t, err)
	body = fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/a.txt"],"dest":"disari/alt://kutu/"}`, f.Rbac.ID)
	code, raw = confRaw(t, f.URL, f.Tok, http.MethodPost, "/api/files/ops", "application/json", []byte(body))
	assert.Equal(t, http.StatusBadRequest, code, "a member, JSON: %s", raw)
	// The plain spelling of that destination is refused by the grants.
	body = fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/a.txt"],"dest":"disari/"}`, f.Rbac.ID)
	code, raw = confRaw(t, f.URL, f.Tok, http.MethodPost, "/api/files/ops", "application/json", []byte(body))
	assert.Equal(t, http.StatusForbidden, code, raw)

	settle()
	assert.False(t, confExists(f.RootMain, "disari/alt:/kutu/ic.txt"), "nothing lands outside the root token's folder")
	assert.False(t, confExists(f.RootRbac, "disari/alt:/kutu/a.txt"), "nothing lands where the member holds no grant")

	// The storage-relative spelling still works, in the root and in a grant.
	code, raw = confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", "application/json",
		[]byte(`{"path":"main://kutu","name":"hedef"}`))
	require.Equal(t, http.StatusOK, code, raw)
	body = fmt.Sprintf(`{"kind":"copy","storage_id":%d,"sources":["kutu/ic.txt"],"dest":"kutu/hedef/"}`, f.Main.ID)
	code, raw = confRaw(t, f.URL, tok, http.MethodPost, "/api/files/ops", "text/plain", []byte(body))
	require.Equal(t, http.StatusAccepted, code, raw)
	cdEventually(t, func() bool { return confExists(f.RootMain, "kutu/hedef/ic.txt") }, "a copy inside the root must still be carried out")
}

// The text editor's save.
func TestConfineDoors_ATextSaveStaysInTheRoot(t *testing.T) {
	f, tok := confinedFix(t)

	for _, ct := range []string{"text/plain", ""} {
		for _, s := range []struct{ label, body string }{
			{"over a file outside", `{"path":"main://disari/gizli.txt","content":"ezildi"}`},
			{"a new file outside", `{"path":"main://disari/yeni.txt","content":"yeni"}`},
		} {
			code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/save-text", ct, []byte(s.body))
			assert.Equal(t, http.StatusForbidden, code, "%s (%q): %s", s.label, ct, raw)
		}
	}
	assert.Equal(t, "gizli", cdRead(t, f.RootMain, "disari/gizli.txt"), "the file outside keeps its content")
	assert.False(t, confExists(f.RootMain, "disari/yeni.txt"), "no file is made outside")

	code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/save-text", "text/plain",
		[]byte(`{"path":"main://kutu/ic.txt","content":"guncel"}`))
	require.Equal(t, http.StatusOK, code, raw)
	assert.Equal(t, "guncel", cdRead(t, f.RootMain, "kutu/ic.txt"), "a save inside the root still works")
}

// A comment is deleted by its own id, which the middleware never sees.
//
// Commenting asks the token's `comments:rw` (task #157): both tokens here name
// it, so what is measured is the root, not the permission.
func TestConfineDoors_ACommentOutsideTheRootIsNotDeleted(t *testing.T) {
	f, _ := confinedFix(t)
	ctx := context.Background()
	author := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,write,delete,mcp,comments:rw")
	tok := testutil.NewAPIToken(t, f.Store, f.MemberID, "read,write,delete,comments:rw,root:main://kutu")

	comment := func(rel string) int64 {
		t.Helper()
		n, err := f.Store.GetNodeByPath(ctx, f.Main.ID, pathkey.Hash(f.Main.ID, rel))
		require.NoError(t, err, rel)
		require.NotNil(t, n, rel)
		// Written by the same member, unconfined: the author may delete it.
		code, raw := confRaw(t, f.URL, author, http.MethodPost, "/api/files/comments", "application/json",
			[]byte(fmt.Sprintf(`{"node_id":%d,"body":"not"}`, n.ID)))
		require.Equal(t, http.StatusOK, code, raw)
		var out struct {
			Comment struct {
				ID int64 `json:"id"`
			} `json:"comment"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &out), raw)
		require.NotZero(t, out.Comment.ID, raw)
		return out.Comment.ID
	}
	outside, inside := comment("disari/gizli.txt"), comment("kutu/ic.txt")

	code, raw := confRaw(t, f.URL, tok, http.MethodDelete, "/api/files/comments/"+fmt.Sprint(outside), "", nil)
	assert.Equal(t, http.StatusNotFound, code, raw)
	c, err := f.Store.GetNodeComment(ctx, outside)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Nil(t, c.DeletedAt, "the comment outside the root stays")

	code, raw = confRaw(t, f.URL, tok, http.MethodDelete, "/api/files/comments/"+fmt.Sprint(inside), "", nil)
	assert.Equal(t, http.StatusOK, code, "a comment inside the root is still deleted: %s", raw)
}

// Grants, invitations and share mails resolve their path in one place.
func TestConfineDoors_GrantsStayInTheRoot(t *testing.T) {
	pf := newPermFix(t)
	ctx := context.Background()
	for _, name := range []string{"Ekip", "Gizli"} {
		status, body := fxMutate(t, pf.URL, pf.adminTok, "newfolder", map[string]any{"path": "alpha://", "name": name})
		require.Equal(t, http.StatusOK, status, body)
	}
	pf.StA.RBACEnabled = true
	require.NoError(t, pf.Store.UpdateStorage(ctx, pf.StA))
	// The member owns both folders, so only the token's root stands between
	// it and Gizli.
	for _, p := range []string{"Ekip", "Gizli"} {
		_, err := pf.Store.CreateFileGrant(ctx, &model.FileGrant{StorageID: pf.StA.ID, PathPrefix: p, IsDir: true, UserID: pf.UserA, Level: model.GrantOwner})
		require.NoError(t, err)
	}
	colleague := seedUserIn(t, pf.Store, mustSupertenant(t, pf), "colleague@alpha.test")
	tok := issueToken(t, pf.Store, pf.UserA, "read,write,delete,root:alpha://Ekip", nil)
	granted := func(p string) bool {
		gs, err := pf.Store.ListFileGrantsByStorageUser(ctx, pf.StA.ID, colleague)
		require.NoError(t, err)
		for _, g := range gs {
			if g.PathPrefix == p {
				return true
			}
		}
		return false
	}

	for _, ct := range []string{"text/plain", ""} {
		for _, s := range []struct{ label, route, body string }{
			{"a grant", "/api/files/permissions", fmt.Sprintf(`{"path":"alpha://Gizli","user_id":%d,"level":"viewer"}`, colleague)},
			{"an invitation", "/api/files/permissions/invite", `{"path":"alpha://Gizli","email":"colleague@alpha.test","level":"viewer"}`},
		} {
			code, raw := confRaw(t, pf.URL, tok, http.MethodPost, s.route, ct, []byte(s.body))
			assert.Equal(t, http.StatusForbidden, code, "%s (%q): %s", s.label, ct, raw)
		}
	}
	assert.False(t, granted("Gizli"), "no grant is made outside the root")

	// A share mail names a link, not a path (share_mail.go): the member's own
	// link on Gizli is outside this token's root, and answers as a link that
	// does not exist — in either body shape.
	gizli, err := pf.Store.GetNodeByPath(ctx, pf.StA.ID, pathkey.Hash(pf.StA.ID, "/Gizli"))
	if err != nil || gizli == nil {
		gizli, err = pf.Store.CreateNode(ctx, &model.Node{
			StorageID: pf.StA.ID, Name: "Gizli", Path: "/Gizli", PathHash: pathkey.Hash(pf.StA.ID, "/Gizli"),
			Type: model.NodeTypeDirectory,
		})
		require.NoError(t, err)
	}
	member := pf.UserA
	outside, err := share.NewService(pf.Store).Create(ctx, share.CreateOpts{NodeID: gizli.ID, CreatedBy: &member})
	require.NoError(t, err)
	for _, ct := range []string{"text/plain", ""} {
		code, raw := confRaw(t, pf.URL, tok, http.MethodPost, "/api/files/permissions/share-mail", ct,
			[]byte(fmt.Sprintf(`{"share":%q,"email":"colleague@alpha.test"}`, outside.Token)))
		assert.Equal(t, http.StatusNotFound, code, "a share mail (%q): %s", ct, raw)
	}

	code, raw := confRaw(t, pf.URL, tok, http.MethodPost, "/api/files/permissions", "text/plain",
		[]byte(fmt.Sprintf(`{"path":"alpha://Ekip","user_id":%d,"level":"viewer"}`, colleague)))
	require.Equal(t, http.StatusOK, code, raw)
	assert.True(t, granted("Ekip"), "a grant inside the root still works")
}

// The encrypted-folder doors resolve their folder in one place; cleanup
// deletes an encrypted folder's versions and trash entries for good.
func TestConfineDoors_EncryptedFolderDoorsStayInTheRoot(t *testing.T) {
	f := newStagedFixtureWith(t, withVersions)
	for _, dir := range []string{"Disari", "Kutu"} {
		f.stagedAt(t, dir, "a.txt", []byte("one"), false)
		f.stagedAt(t, dir, "a.txt", []byte("two"), false)
		f.stagedAt(t, dir, ".filex-e2e.json", []byte(kfConvDone), false)
		require.Len(t, versionsOf(t, f, dir+"/a.txt"), 1)
	}
	tok := testutil.NewAPIToken(t, f.store, f.userID, "read,write,delete,root:main://Kutu")

	for _, ct := range []string{"text/plain", ""} {
		for _, s := range []struct{ label, route, body string }{
			{"cleanup", "/api/files/e2e/cleanup", `{"path":"main://Disari","versions":true,"trash":true}`},
			{"a password change", "/api/files/e2e/password-changed", `{"path":"main://Disari","via":"password"}`},
		} {
			code, raw := confRaw(t, f.srv.URL, tok, http.MethodPost, s.route, ct, []byte(s.body))
			assert.Equal(t, http.StatusForbidden, code, "%s (%q): %s", s.label, ct, raw)
		}
	}
	assert.Len(t, versionsOf(t, f, "Disari/a.txt"), 1, "the versions outside the root are kept")

	code, raw := confRaw(t, f.srv.URL, tok, http.MethodPost, "/api/files/e2e/password-changed", "text/plain",
		[]byte(`{"path":"main://Kutu","via":"password"}`))
	assert.Equal(t, http.StatusOK, code, raw)
	code, raw = confRaw(t, f.srv.URL, tok, http.MethodPost, "/api/files/e2e/cleanup", "text/plain",
		[]byte(`{"path":"main://Kutu","versions":true}`))
	require.Equal(t, http.StatusOK, code, raw)
	assert.Empty(t, versionsOf(t, f, "Kutu/a.txt"), "inside the root cleanup still works")
}

// One account behind many `root:` tokens (one per project, the documented
// embed): each token follows the operations of its own folder only.
func TestConfineDoors_TheQueueShowsOnlyTheRootsOperations(t *testing.T) {
	f, tok := confinedFix(t)

	queue := func(token, body string) int64 {
		t.Helper()
		code, raw := confRaw(t, f.URL, token, http.MethodPost, "/api/files/copy", "application/json", []byte(body))
		require.Equal(t, http.StatusAccepted, code, raw)
		var out struct {
			Op struct {
				ID int64 `json:"id"`
			} `json:"op"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &out), raw)
		require.NotZero(t, out.Op.ID, raw)
		return out.Op.ID
	}
	// The same member, unconfined (another project's token), and the root token.
	outside := queue(f.Tok, `{"source":["main://disari/gizli.txt"],"target":"main://disari"}`)
	inside := queue(tok, `{"source":["main://kutu/ic.txt"],"target":"main://kutu"}`)
	settle()

	code, raw := confRaw(t, f.URL, tok, http.MethodGet, "/api/files/ops", "", nil)
	require.Equal(t, http.StatusOK, code, raw)
	var list struct {
		Ops []struct {
			ID int64 `json:"id"`
		} `json:"ops"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &list), raw)
	ids := map[int64]bool{}
	for _, o := range list.Ops {
		ids[o.ID] = true
	}
	assert.False(t, ids[outside], "the listing must not carry the other folder's operation: %s", raw)
	assert.True(t, ids[inside], "the listing carries the root's own operation: %s", raw)
	assert.NotContains(t, raw, "disari", "no path outside the root is listed")

	code, raw = confRaw(t, f.URL, tok, http.MethodGet, "/api/files/ops/"+fmt.Sprint(outside), "", nil)
	assert.Equal(t, http.StatusNotFound, code, raw)
	code, raw = confRaw(t, f.URL, tok, http.MethodPost, "/api/files/ops/"+fmt.Sprint(outside)+"/cancel", "", nil)
	assert.Equal(t, http.StatusNotFound, code, raw)
	code, raw = confRaw(t, f.URL, tok, http.MethodGet, "/api/files/ops/"+fmt.Sprint(inside), "", nil)
	assert.Equal(t, http.StatusOK, code, raw)
}

// cdWatch opens the live socket with a token (no ticket), subscribes to
// rawPath and returns the connection once the subscription is answered.
func cdWatch(t *testing.T, base, token, rawPath string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	url := strings.Replace(base, "http://", "ws://", 1) + "/api/ws"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"X-Filex-Token": {token}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	req, _ := json.Marshal(map[string]string{"type": "subscribe", "path": rawPath})
	require.NoError(t, conn.Write(ctx, websocket.MessageText, req))
	for {
		rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
		_, data, rerr := conn.Read(rctx)
		rcancel()
		require.NoError(t, rerr, "no answer to the subscription")
		var frame map[string]any
		if json.Unmarshal(data, &frame) == nil && (frame["type"] == "presence" || frame["type"] == "error") {
			return conn
		}
	}
}

// cdChanged reports whether a change frame arrives on conn within the window.
func cdChanged(conn *websocket.Conn, window time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return false
		}
		var frame map[string]any
		if json.Unmarshal(data, &frame) == nil && frame["type"] == "change" {
			return true
		}
	}
}

// The live socket, opened with the token itself rather than a ticket.
func TestConfineDoors_TheLiveFeedStaysInTheRoot(t *testing.T) {
	f, tok := confinedFix(t)

	outside := cdWatch(t, f.URL, tok, "main://disari")
	inside := cdWatch(t, f.URL, tok, "main://kutu")

	// A colleague changes both folders.
	for _, dir := range []string{"disari", "kutu"} {
		code, raw := confRaw(t, f.URL, f.Tok2, http.MethodPost, "/api/files/manager?action=newfolder", "application/json",
			[]byte(`{"path":"main://`+dir+`","name":"yeni"}`))
		require.Equal(t, http.StatusOK, code, raw)
	}
	assert.False(t, cdChanged(outside, 2*time.Second), "a change outside the root must not reach the token")
	assert.True(t, cdChanged(inside, 5*time.Second), "a change inside the root still reaches it")
}

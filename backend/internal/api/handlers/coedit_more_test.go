package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// ── Editing together, filex 0.56's second round (task #189) ─────────────
//
//   - a file of an encrypted folder is edited together: only by an explorer
//     that holds the folder key (`e2e`) and an app the folder's door lets in,
//     with the session's key sealed by the members' pages (…/key) - the
//     server keeps it as it is and is never handed a key;
//   - a person who may only read the file follows along: joined, refused
//     every write, audited as a watcher;
//   - a save in a session is held to what the session holds, not to the
//     page's `expect`, so one member's save does not refuse the next one's,
//     and an outside version still does;
//   - unsent changes of an earlier opening: the question comes from the
//     server, in the reader's language.
//
// All red before this round: an encrypted file was refused at the join,
// there was no …/key, a watcher was audited as a member, a session's saves
// were held to `expect`, and the join answered no words.

const coeditE2EManifest = `{"manifest_version":1,"name":"sketch","version":"1.0.0","label":{"en":"Sketch"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},"encrypted_folders":{"open":true},"co_edit":{"open":true},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Sketch"},"applies":{"ext":["sketch"]}}]}`

func (f *appFixture) installCoEditE2EApp(t *testing.T) {
	t.Helper()
	_, _, err := f.reg.Install(t.Context(), &wasmplugin.InstallInput{
		Manifest: []byte(coeditE2EManifest), UI: bytes.NewReader(uiBundleZip(t)),
		Granted: []string{"files:read", "files:write", "ui", "ui-viewer:.sketch", "files:e2e-plaintext", "files:co-edit"},
	})
	require.NoError(t, err)
}

func memberOf(t *testing.T, out map[string]any) (sid, client, token string) {
	t.Helper()
	me, _ := out["me"].(map[string]any)
	require.NotNil(t, me, out)
	return out["session"].(string), me["client"].(string), out["token"].(string)
}

func TestCoEdit_AnEncryptedFolderIsEditedTogetherWithTheMembersOwnKey(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditE2EApp(t)
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", sketchCiphertext)
	target := "main://kasa/doc.sketch"

	code, out := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": target}))
	assert.Equal(t, http.StatusForbidden, code, "without the explorer's folder key: %v", out)
	assert.Equal(t, "encrypted", out["error"])

	code, out = f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": target, "e2e": true}))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "folder", out["key_mode"])
	assert.NotContains(t, out, "key", "the server makes no key it could open")
	assert.Equal(t, "", out["sealed_key"], "none yet: the starter's page seals one")
	assert.Equal(t, true, out["created"])
	assert.NotEmpty(t, out["doc"], "the scope the members seal the key for")
	sid, client, token := memberOf(t, out)

	code, again := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": target, "e2e": true}))
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "coedit_not_ready", again["error"], "no key and no base yet")

	code, k := f.co(t, http.MethodPost, sid+"/key", jsonBody(map[string]any{"client": client, "token": token, "sealed": "sealed-with-the-folder-key"}))
	require.Equal(t, http.StatusOK, code, k)
	code, k = f.co(t, http.MethodPost, sid+"/key", jsonBody(map[string]any{"client": client, "token": token, "sealed": "another"}))
	assert.Equal(t, http.StatusBadRequest, code, "once: %v", k)

	q := url.Values{"client": {client}, "token": {token}}
	code, _ = f.co(t, http.MethodPut, sid+"/blob/base?"+q.Encode(), strings.NewReader("sealed-base"))
	require.Equal(t, http.StatusCreated, code)

	code, b := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": target, "e2e": true}))
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, "sealed-with-the-folder-key", b["sealed_key"], "a joiner is handed the sealed key, never a raw one")
	assert.NotContains(t, b, "key")

	rows := f.e2eAudit(t, "app_plugin.coedit_join")
	require.NotEmpty(t, rows)
	assert.Equal(t, true, rows[0]["e2e"])

	// The administrator's switch closes this door as it closes the others.
	require.NoError(t, f.store.UpsertSetting(t.Context(), wasmplugin.E2EPlaintextSetting.Key, "false"))
	code, out = f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": target, "e2e": true}))
	assert.Equal(t, http.StatusForbidden, code, out)
}

func TestCoEdit_ALevel2FileIsJoinedWithoutAKindTheServerCannotRead(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditE2EApp(t)
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.markNamesEncrypted(t, "kasa")
	f.writeFile(t, "kasa/"+level2Name, sketchCiphertext)
	f.writeFile(t, "kasa/clear.txt", sketchCiphertext)

	// The stored name is ciphertext: the browser chose the app by the
	// decrypted name, as for the read and save doors (app_ui_e2e.go).
	code, out := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://kasa/" + level2Name, "e2e": true}))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "folder", out["key_mode"])

	// A name the server can read there is held to its kind.
	code, out = f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://kasa/clear.txt", "e2e": true}))
	assert.NotEqual(t, http.StatusOK, code, out)
}

func TestCoEdit_APersonWhoMayOnlyReadFollowsAlong(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	f.writeFile(t, "alone.sketch", "v1")
	// Somebody who may write started the session and stored its base.
	_, editor, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, editor, "base"))
	st, err := f.store.GetStorage(t.Context(), f.st.ID)
	require.NoError(t, err)
	st.ReadOnly = true
	require.NoError(t, f.store.UpdateStorage(t.Context(), st))

	// A watcher never starts a session: its starter chooses the base every
	// member builds on.
	code, out := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://alone.sketch"}))
	assert.Equal(t, http.StatusConflict, code, out)
	assert.Equal(t, "coedit_nothing_to_watch", out["error"])

	code, out = f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://doc.sketch"}))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, editor.Session, out["session"], "the editor's session")
	assert.Equal(t, false, out["created"])
	me := out["me"].(map[string]any)
	assert.Equal(t, false, me["can_edit"], "a read-only storage: a watcher")
	sid, client, token := memberOf(t, out)

	code, a := f.co(t, http.MethodPost, sid+"/append", jsonBody(map[string]any{
		"client": client, "token": token, "kind": "lock", "base": 1, "ctr": 1, "ct": []byte("sealed"),
	}))
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "coedit_read_only", a["error"])

	assert.Len(t, f.e2eAudit(t, "app_plugin.coedit_watch"), 1, "audited as watching")
	joins := f.e2eAudit(t, "app_plugin.coedit_join")
	if assert.Len(t, joins, 1, "the editor's join only, not the watcher's") {
		assert.Equal(t, true, joins[0]["created"], "the editor who started the session")
	}
}

// A member's page leaves as it goes away with navigator.sendBeacon (core
// useFileApi coeditCall): the session cookie and nothing else - no
// Accept-Language, no JSON content type, the browser's own fetch metadata -
// and the member's body as text/plain. The leave is taken all the same.
func TestCoEdit_ALeaveSentAsABeaconIsTaken(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, editor, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, editor, "base"))
	code, other, out := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusOK, code, out)
	s, err := f.coedit.Session(t.Context(), editor.Session)
	require.NoError(t, err)
	require.Equal(t, 2, s.Members)

	body, _ := json.Marshal(map[string]string{"client": other.Client, "token": other.Token})
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/ui/sketch/editor/coedit/"+other.Session+"/leave", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", f.srv.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, string(raw))
	assert.JSONEq(t, `{"left":true}`, string(raw))

	s, err = f.coedit.Session(t.Context(), editor.Session)
	require.NoError(t, err)
	assert.Equal(t, 1, s.Members, "the member is out of the session at once, not when the relay times it out")
}

// An API key without `write` joins as a watcher, as the document server's
// editor opens in view for it: the person may edit, the key may not.
func TestCoEdit_AKeyWithoutWriteFollowsAlong(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, editor, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, editor, "base"))

	admin, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)
	call := func(token, rest string, body any) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/ui/sketch/editor/coedit/"+rest, jsonBody(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Filex-Token", token)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	code, out := call(testutil.NewAPIToken(t, f.store, admin.ID, "read"), "join", map[string]string{"path": "main://doc.sketch"})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, editor.Session, out["session"])
	me := out["me"].(map[string]any)
	assert.Equal(t, false, me["can_edit"], "a `read` key: a watcher")
	assert.Len(t, f.e2eAudit(t, "app_plugin.coedit_watch"), 1, "audited as watching")

	code, out = call(testutil.NewAPIToken(t, f.store, admin.ID, "read,write"), "join", map[string]string{"path": "main://doc.sketch"})
	require.Equal(t, http.StatusOK, code, out)
	me = out["me"].(map[string]any)
	assert.Equal(t, true, me["can_edit"], "a `write` key edits")
}

func TestCoEdit_ASaveInASessionIsHeldToTheSessionNotToThePagesExpect(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, a, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))
	session := func(through string) url.Values {
		return url.Values{"path": {"main://doc.sketch"}, "coedit": {a.Session}, "client": {a.Client}, "token": {a.Token}, "through": {through}}
	}

	// A stale `expect` (another member's save came between) does not refuse
	// a save the session holds.
	q := session("1")
	q.Set("expect", "1:1")
	code, body := f.uiSave(t, "sketch", "editor", q.Encode(), "v2")
	require.Equal(t, http.StatusOK, code, body)
	code, body = f.uiSave(t, "sketch", "editor", session("1").Encode(), "v3")
	require.Equal(t, http.StatusOK, code, "the session's own last save is what it holds: %s", body)

	// A version written outside the session is never overwritten unseen.
	f.writeFile(t, "doc.sketch", "written elsewhere")
	code, body = f.uiSave(t, "sketch", "editor", session("1").Encode(), "v4")
	assert.Equal(t, http.StatusPreconditionFailed, code, body)
	assert.Equal(t, "written elsewhere", f.readFile(t, "doc.sketch"))
}

func TestCoEdit_TheUnsentQuestionComesFromTheServer(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	code, out := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://doc.sketch", "unsent": map[string]any{"count": 3}}))
	require.Equal(t, http.StatusOK, code, out)
	words, _ := out["unsent_words"].(map[string]any)
	require.NotNil(t, words, out)
	assert.Contains(t, words["behind"], "3 changes")
	assert.Contains(t, words["behind"], "doc.sketch")
	assert.Contains(t, words["ended"], "has ended")
	assert.NotEmpty(t, words["apply"])
	assert.NotEmpty(t, words["discard"])

	// The starter stores the base; the next openings join, and each says
	// what it kept.
	sid, client, token := memberOf(t, out)
	q := url.Values{"client": {client}, "token": {token}}
	code, _ = f.co(t, http.MethodPut, sid+"/blob/base?"+q.Encode(), strings.NewReader("base"))
	require.Equal(t, http.StatusCreated, code)

	code, one := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://doc.sketch", "unsent": map[string]any{"count": 1}}))
	require.Equal(t, http.StatusOK, code, one)
	w1 := one["unsent_words"].(map[string]any)
	assert.Contains(t, w1["behind"], "1 change you made", "one change: the singular, carrying the count")

	code, none := f.co(t, http.MethodPost, "join", jsonBody(map[string]any{"path": "main://doc.sketch"}))
	require.Equal(t, http.StatusOK, code, none)
	assert.NotContains(t, none, "unsent_words", "no unsent changes: no question")
}

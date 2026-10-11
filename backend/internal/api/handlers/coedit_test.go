package handlers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/coedit"
)

// ── Editing together (task #189): the relay behind coedit.* ─────────────

// coeditManifest is the interface-only app whose views edit together
// (`co_edit`, derived permission files:co-edit).
var coeditManifest = strings.Replace(uiOnlyManifest, `"permissions":["files:read","files:write"]`,
	`"permissions":["files:read","files:write"],"co_edit":{"open":true}`, 1)

// installCoEditApp installs the sketch app with files:co-edit, through the
// install the panel uses (every permission it asks for granted).
func (f *appFixture) installCoEditApp(t *testing.T) {
	t.Helper()
	bundle := uiBundleZip(t)
	grant := []string{"files:co-edit", "files:read", "files:write", "ui", "ui-viewer:.sketch"}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write([]byte(coeditManifest))
	uf, _ := w.CreateFormFile("ui", "ui.zip")
	_, _ = uf.Write(bundle)
	g, _ := json.Marshal(map[string]any{"permissions": grant})
	_ = w.WriteField("grant", string(g))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Less(t, res.StatusCode, 300, string(raw))
}

func (f *appFixture) co(t *testing.T, method, rest string, body io.Reader) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+"/api/files/plugins/ui/sketch/editor/coedit/"+rest, body)
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if len(out) == 0 {
		out["raw"] = string(raw)
	}
	return res.StatusCode, out
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

type joined struct {
	Session string
	Client  string
	Token   string
	Ticket  string
	Created bool
	Index   int
	CanEdit bool
	Key     []byte
}

func (f *appFixture) join(t *testing.T, path string) (int, joined, map[string]any) {
	t.Helper()
	code, out := f.co(t, http.MethodPost, "join", jsonBody(map[string]string{"path": path}))
	if code != http.StatusOK {
		return code, joined{}, out
	}
	me := out["me"].(map[string]any)
	socket := out["socket"].(map[string]any)
	key, _ := base64.StdEncoding.DecodeString(out["key"].(string))
	return code, joined{
		Session: out["session"].(string), Client: me["client"].(string), Token: out["token"].(string),
		Ticket: socket["ticket"].(string), Created: out["created"].(bool), Index: int(me["index_user"].(float64)),
		CanEdit: me["can_edit"].(bool), Key: key,
	}, out
}

func (f *appFixture) putBase(t *testing.T, j joined, body string) int {
	t.Helper()
	q := url.Values{"client": {j.Client}, "token": {j.Token}}
	code, _ := f.co(t, http.MethodPut, j.Session+"/blob/base?"+q.Encode(), strings.NewReader(body))
	return code
}

// The server says who joins and what they may do; the first opener starts
// the session and stores the base before anybody else may join.
func TestCoEdit_TheFirstOpenerStartsTheSessionAndOthersWaitForTheBase(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")

	code, a, out := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusOK, code, out)
	assert.True(t, a.Created)
	assert.Equal(t, 1, a.Index)
	assert.True(t, a.CanEdit, "the admin may change the file and the app holds files:write")
	assert.Len(t, a.Key, coedit.KeyBytes, "an ordinary folder: the server's session key, for the filex page")
	assert.NotEmpty(t, a.Token)
	assert.Contains(t, out["socket"].(map[string]any)["url"], "/api/coedit/ws")
	assert.NotContains(t, out, "user_id", "never an account id")

	code, _, out = f.join(t, "main://doc.sketch")
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "coedit_not_ready", out["error"])

	assert.Equal(t, http.StatusCreated, f.putBase(t, a, "sealed-base"))
	code, b, out := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusOK, code, out)
	assert.False(t, b.Created)
	assert.Equal(t, a.Session, b.Session)
	assert.Equal(t, 2, b.Index)
	assert.Equal(t, a.Key, b.Key)
}

func TestCoEdit_AnAppWithoutTheGrantIsRefused(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	code, _, out := f.join(t, "main://doc.sketch")
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "not_granted", out["error"])
}

func TestCoEdit_OnlyAFileTheViewOpensAndThePersonMayRead(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "notes.txt", "x")
	f.writeFile(t, "secret.sketch.fxe", "ciphertext")
	code, _, out := f.join(t, "main://notes.txt")
	assert.NotEqual(t, http.StatusOK, code, out)
	code, _, out = f.join(t, "main://secret.sketch.fxe")
	assert.Equal(t, http.StatusForbidden, code, out)
	assert.Equal(t, "encrypted", out["error"], "a file the server holds only as ciphertext waits for files:e2e-plaintext")
	code, _, _ = f.join(t, "main://missing.sketch")
	assert.Equal(t, http.StatusNotFound, code)

	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/ui/sketch/editor/coedit/join", jsonBody(map[string]string{"path": "main://doc.sketch"}))
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode, "no session: nobody joins")
}

// A request that names a member must be that member's: its account AND its
// token. The order, the lease and the counter are the relay's.
func TestCoEdit_AppendsAreTheMembersOwnAndLandInOneOrder(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, a, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))

	add := func(token string, kind string, base, ctr uint64) (int, map[string]any) {
		return f.co(t, http.MethodPost, a.Session+"/append", jsonBody(map[string]any{
			"client": a.Client, "token": token, "kind": kind, "base": base, "ctr": ctr, "ct": []byte("sealed"),
		}))
	}
	code, out := add("not-the-token", "lock", 1, 1)
	assert.Equal(t, http.StatusGone, code, out)
	assert.Equal(t, "coedit_gone", out["error"])

	code, out = add(a.Token, "changes", 1, 1)
	assert.Equal(t, http.StatusConflict, code, out)
	assert.Equal(t, "coedit_no_lease", out["error"])

	code, out = add(a.Token, "lock", 1, 1)
	require.Equal(t, http.StatusOK, code, out)
	assert.EqualValues(t, 2, out["seq"])

	code, out = add(a.Token, "lock", 1, 2)
	assert.Equal(t, http.StatusConflict, code, out)
	assert.Equal(t, "coedit_conflict", out["error"])
	assert.EqualValues(t, 2, out["head"])

	code, out = f.co(t, http.MethodPost, a.Session+"/lease", jsonBody(map[string]any{"client": a.Client, "token": a.Token, "op": "acquire", "changes_seen": 0}))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, true, out["granted"])
	code, out = add(a.Token, "changes", 2, 2)
	require.Equal(t, http.StatusOK, code, out)

	q := url.Values{"client": {a.Client}, "token": {a.Token}}
	code, out = f.co(t, http.MethodGet, a.Session+"/blob/base?"+q.Encode(), nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "base", out["raw"])
}

// A save made in a session records which part of the log it holds - the
// file's signature taken by the server after the write.
func TestCoEdit_ASaveInASessionIsRecordedInItsLog(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, a, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))

	q := url.Values{"path": {"main://doc.sketch"}, "coedit": {a.Session}, "client": {a.Client}, "token": {a.Token}, "through": {"1"}}
	code, body := f.uiSave(t, "sketch", "editor", q.Encode(), "v2")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "v2", f.readFile(t, "doc.sketch"))

	es, err := f.coedit.Entries(context.Background(), a.Session, 0, 100)
	require.NoError(t, err)
	last := es[len(es)-1]
	assert.Equal(t, coedit.KindSaved, last.Kind)
	assert.EqualValues(t, 1, last.Through)

	// Somebody else's token records nothing (the save itself stands).
	q.Set("token", "wrong")
	q.Set("through", "2")
	code, body = f.uiSave(t, "sketch", "editor", q.Encode(), "v3")
	require.Equal(t, http.StatusOK, code, body)
	es2, err := f.coedit.Entries(context.Background(), a.Session, 0, 100)
	require.NoError(t, err)
	assert.Len(t, es2, len(es), "no second saved entry")
}

// The socket hands a member every entry in order, then each new one, and
// says when the membership ended.
func TestCoEdit_TheSocketHandsEveryEntryInOrderAndSaysWhenItEnds(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, a, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/api/coedit/ws?ticket=" + a.Ticket
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"subscribe","from":0}`)))

	read := func() map[string]any {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(data, &m))
		return m
	}
	first := read()
	require.Equal(t, "entry", first["type"], first)
	assert.EqualValues(t, 1, first["entry"].(map[string]any)["seq"])
	assert.Equal(t, "join", first["entry"].(map[string]any)["kind"])

	code, out := f.co(t, http.MethodPost, a.Session+"/append", jsonBody(map[string]any{
		"client": a.Client, "token": a.Token, "kind": "lock", "base": 1, "ctr": 1, "ct": []byte("sealed"),
	}))
	require.Equal(t, http.StatusOK, code, out)
	next := read()
	require.Equal(t, "entry", next["type"], next)
	assert.EqualValues(t, 2, next["entry"].(map[string]any)["seq"])

	code, out = f.co(t, http.MethodPost, a.Session+"/leave", jsonBody(map[string]any{"client": a.Client, "token": a.Token}))
	require.Equal(t, http.StatusOK, code, out)
	for {
		m := read()
		if m["type"] == "ended" {
			assert.Equal(t, "left", m["reason"])
			break
		}
		require.Equal(t, "entry", m["type"], m)
	}

	// A ticket is spent once.
	_, _, err = websocket.Dial(ctx, wsURL, nil)
	assert.Error(t, err)
}

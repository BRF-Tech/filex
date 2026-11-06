package handlers_test

// Switching an account off stops every API token it holds, at every door a
// token opens, with the answer the session gets (GHSA-58wc-42xh-89v8).
//
// The token-only doors (/api/ai and its MCP and admin surfaces, /api/sharex)
// authenticated with a middleware of their own that never asked whether the
// owner was enabled, while the hybrid door beside it (/api/files, /api/admin)
// did: a disabled account's token went on reading and writing its files,
// answering MCP, minting public links, and - an administrator's token - running
// the admin surface, which can switch the account back on. Measured on the old
// code: every request below answered 200 after the account was disabled, and
// the administrator's token re-enabled its own account.
//
// The two sockets a single-use ticket opens (/api/ws, /api/coedit/ws) read
// the ticket's account back and did not ask either: a ticket minted a moment
// before the switch still opened its socket after it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// ownerTokenCall sends one request with an API token and returns the status, the
// decoded JSON body (empty when it is not JSON) and the raw body.
func ownerTokenCall(t *testing.T, base, tok, method, path, ct string, body []byte) (int, map[string]any, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, bytes.NewReader(body))
	require.NoError(t, err)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if strings.HasSuffix(path, "/mcp") {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if tok != "" {
		req.Header.Set("X-Filex-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

// tokenDoor is one request a token can make. body builds the request body and
// its Content-Type together (a multipart boundary is new on every build).
type tokenDoor struct {
	name         string
	method, path string
	body         func(t *testing.T) ([]byte, string)
}

func doorJSON(v any) func(*testing.T) ([]byte, string) {
	return func(*testing.T) ([]byte, string) { b, _ := json.Marshal(v); return b, "application/json" }
}

// theTokenOnlyDoors are the routes behind auth.APITokenMiddleware the report
// names, read and write: discovery, a listing, a download, an upload, a public
// link, MCP and the ShareX upload (which writes and links).
func theTokenOnlyDoors(write string) []tokenDoor {
	return []tokenDoor{
		{name: "ai root", method: http.MethodGet, path: "/api/ai/root"},
		{name: "ai list", method: http.MethodGet, path: "/api/ai/files?path=main://"},
		{name: "ai download", method: http.MethodGet, path: "/api/ai/download?path=main://marker.txt"},
		{name: "ai upload", method: http.MethodPost, path: "/api/ai/upload",
			body: doorJSON(map[string]any{"path": "main://" + write, "content": "written by the token"})},
		{name: "ai share", method: http.MethodPost, path: "/api/ai/share",
			body: doorJSON(map[string]any{"path": "main://marker.txt"})},
		{name: "mcp", method: http.MethodPost, path: "/api/ai/mcp",
			body: doorJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}})},
		{name: "sharex upload", method: http.MethodPost, path: "/api/sharex/upload", body: func(t *testing.T) ([]byte, string) {
			b, ct := multipartBody(t, "file", "shot.png", []byte("captured"), map[string]string{"folder": "sharex"})
			return b.Bytes(), ct
		}},
	}
}

func (d tokenDoor) call(t *testing.T, base, tok string) (int, map[string]any, string) {
	t.Helper()
	var body []byte
	ct := ""
	if d.body != nil {
		body, ct = d.body(t)
	}
	return ownerTokenCall(t, base, tok, d.method, d.path, ct, body)
}

// jsonOf is a JSON request body alone, for a call made outside a door list.
func jsonOf(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// assertRefusedAsDisabled holds a refusal to the session chain's own words
// (auth.Middleware): 403, "this account is disabled", reason account_disabled.
// Not fatal: a red run names every door that let the token through.
func assertRefusedAsDisabled(t *testing.T, door string, code int, out map[string]any, raw string) {
	t.Helper()
	if !assert.Equal(t, http.StatusForbidden, code, "%s: a disabled account's token was let through; body: %.200s", door, raw) {
		return
	}
	assert.Equal(t, "this account is disabled", out["error"], "%s: the session's words; body: %s", door, raw)
	assert.Equal(t, "account_disabled", out["reason"], "%s: the code the sign-in page translates; body: %s", door, raw)
}

func setEnabled(t *testing.T, f *stagedFixture, userID int64, on bool) {
	t.Helper()
	code, out := doJSON(t, f.client, http.MethodPatch,
		fmt.Sprintf("%s/api/admin/users/%d", f.srv.URL, userID), map[string]any{"enabled": on})
	require.Equal(t, http.StatusOK, code, "%v", out)
	u, err := f.store.GetUser(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, on, u.Enabled)
}

func sharesBy(t *testing.T, f *stagedFixture, userID int64) int64 {
	t.Helper()
	_, n, err := f.store.ListAllShares(context.Background(), &userID, false, 100, 0)
	require.NoError(t, err)
	return n
}

// The report's case and its controls: an ordinary account's token, every verb
// it was minted with, works while the account is on; switched off, each door
// refuses it as the session is refused and nothing is read, written or linked;
// switched back on, the same token works again (disable pauses, it does not
// revoke).
func TestDisabledOwner_TheTokenOnlyDoorsRefuseItsTokens(t *testing.T) {
	f := newSurfaceFixture(t)
	const email, pw = "leaver@test.local", "LeaverPass!1"
	u := seedSharedUser(t, f.store, email, pw)
	tok := testutil.NewAPIToken(t, f.store, u.ID, "read,write,delete,mcp")

	// The account's own file, written through its token while it is on.
	code, out, raw := ownerTokenCall(t, f.srv.URL, tok, http.MethodPost, "/api/ai/upload", "application/json",
		jsonOf(t, map[string]any{"path": "main://marker.txt", "content": "marker"}))
	require.Equal(t, http.StatusOK, code, "%v %s", out, raw)

	for _, d := range theTokenOnlyDoors("while-on.txt") {
		code, _, raw := d.call(t, f.srv.URL, tok)
		require.Truef(t, code >= 200 && code < 300, "%s: an enabled account's token must work (%d); body: %s", d.name, code, raw)
	}
	sharesOn := sharesBy(t, f, u.ID)
	require.GreaterOrEqual(t, sharesOn, int64(2), "the ai share and the ShareX upload each made a link")
	sharexOn, _ := os.ReadDir(filepath.Join(f.rootDir, "sharex"))

	setEnabled(t, f, u.ID, false)

	for _, d := range theTokenOnlyDoors("while-off.txt") {
		code, out, raw := d.call(t, f.srv.URL, tok)
		assertRefusedAsDisabled(t, d.name, code, out, raw)
	}
	assert.NoFileExists(t, filepath.Join(f.rootDir, "while-off.txt"), "the disabled account's token wrote a file")
	assert.Equal(t, sharesOn, sharesBy(t, f, u.ID), "the disabled account's token minted a public link")
	sharexOff, _ := os.ReadDir(filepath.Join(f.rootDir, "sharex"))
	assert.Len(t, sharexOff, len(sharexOn), "the disabled account's token stored a ShareX capture")

	// Controls the report ran: the hybrid door refused the same token before
	// this fix, and a request with no token is a 401, not a 403.
	code, out, raw = ownerTokenCall(t, f.srv.URL, tok, http.MethodGet, "/api/files/manager?action=index&path=main://", "", nil)
	assertRefusedAsDisabled(t, "files manager (hybrid door)", code, out, raw)
	code, _, raw = ownerTokenCall(t, f.srv.URL, "", http.MethodGet, "/api/ai/root", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code, "no token at all: %s", raw)

	setEnabled(t, f, u.ID, true)
	for _, d := range theTokenOnlyDoors("on-again.txt") {
		code, _, raw := d.call(t, f.srv.URL, tok)
		require.Truef(t, code >= 200 && code < 300, "%s: re-enabled, the same token works again (%d); body: %s", d.name, code, raw)
	}
	assert.FileExists(t, filepath.Join(f.rootDir, "on-again.txt"))
}

// An administrator's admin token is the worst case of the same hole: while
// its account was switched off it still ran /api/ai/admin and the admin_* MCP
// tools, and could switch its own account back on.
func TestDisabledOwner_AnAdministratorsTokenCannotSwitchItselfBackOn(t *testing.T) {
	f := newSurfaceFixture(t)
	second, _ := testutil.SeedAdminUser(t, f.store)
	tok := testutil.NewAPIToken(t, f.store, second, "read,mcp,admin")

	code, _, raw := ownerTokenCall(t, f.srv.URL, tok, http.MethodGet, "/api/ai/admin/users", "", nil)
	require.Equal(t, http.StatusOK, code, "an enabled administrator's admin token works: %s", raw)

	setEnabled(t, f, second, false)

	code, out, raw := ownerTokenCall(t, f.srv.URL, tok, http.MethodGet, "/api/ai/admin/users", "", nil)
	assertRefusedAsDisabled(t, "ai admin users", code, out, raw)
	code, out, raw = ownerTokenCall(t, f.srv.URL, tok, http.MethodPatch, fmt.Sprintf("/api/ai/admin/users/%d", second),
		"application/json", jsonOf(t, map[string]any{"enabled": true}))
	assertRefusedAsDisabled(t, "ai admin: enable itself", code, out, raw)
	code, out, raw = ownerTokenCall(t, f.srv.URL, tok, http.MethodPost, "/api/ai/mcp", "application/json",
		jsonOf(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name": "admin_users_update", "arguments": map[string]any{"id": second, "body": map[string]any{"enabled": true}},
		}}))
	assertRefusedAsDisabled(t, "mcp admin_users_update: enable itself", code, out, raw)

	u, err := f.store.GetUser(context.Background(), second)
	require.NoError(t, err)
	assert.False(t, u.Enabled, "the disabled administrator's token switched its own account back on")
}

// The session, the hybrid token door and the token-only door turn a disabled
// account away with one answer: the same status, words and code, so the
// sign-in page, the desktop app and an agent read the same thing.
func TestDisabledOwner_EveryDoorAnswersInOneVoice(t *testing.T) {
	f := newSurfaceFixture(t)
	const email, pw = "one-voice@test.local", "OneVoicePass!1"
	u := seedSharedUser(t, f.store, email, pw)
	tok := testutil.NewAPIToken(t, f.store, u.ID, "read")
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	session := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, session, email, pw)

	setEnabled(t, f, u.ID, false)

	answers := map[string]string{}
	resp, err := session.Get(f.srv.URL + "/api/files/quota/me")
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "session: %s", b)
	answers["session"] = strings.TrimSpace(string(b))

	for name, path := range map[string]string{
		"hybrid token door": "/api/files/quota/me",
		"token-only door":   "/api/ai/root",
	} {
		code, out, raw := ownerTokenCall(t, f.srv.URL, tok, http.MethodGet, path, "", nil)
		assertRefusedAsDisabled(t, name, code, out, raw)
		answers[name] = strings.TrimSpace(raw)
	}
	for name, a := range answers {
		assert.Equal(t, answers["session"], a, "%s answers a disabled account in other words than the session", name)
	}
}

// dialTicket opens a ticketed socket the way the embedded explorer does: no
// cookie, no token, only the ticket in the address.
func dialTicket(t *testing.T, base, path, ticket string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	url := "ws" + strings.TrimPrefix(base, "http") + path + "?ticket=" + ticket
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: &http.Client{}})
}

// The live-updates socket: a ticket the account minted while it was on does
// not open once it is off, and says so as the session would.
func TestDisabledOwner_ARealtimeTicketItMintedDoesNotOpen(t *testing.T) {
	f := newSurfaceFixture(t)
	const email, pw = "socket-leaver@test.local", "SocketLeaver!1"
	u := seedSharedUser(t, f.store, email, pw)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	session := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, session, email, pw)
	mint := func() string {
		t.Helper()
		code, out := doJSON(t, session, http.MethodPost, f.srv.URL+"/api/files/ws-ticket", nil)
		require.Equal(t, http.StatusOK, code, "%v", out)
		tk, _ := out["ticket"].(string)
		require.NotEmpty(t, tk, "%v", out)
		return tk
	}
	whileOn, spentLater := mint(), mint()

	conn, _, err := dialTicket(t, f.srv.URL, "/api/ws", whileOn)
	require.NoError(t, err, "an enabled account's ticket opens the socket")
	_ = conn.CloseNow()

	setEnabled(t, f, u.ID, false)
	conn, resp, err := dialTicket(t, f.srv.URL, "/api/ws", spentLater)
	if conn != nil {
		_ = conn.CloseNow()
	}
	require.Error(t, err, "a disabled account's ticket opened the live-updates socket")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// The editing-together socket: the same, for a member's ticket.
func TestDisabledOwner_ACoEditTicketItMintedDoesNotOpen(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCoEditApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	_, a, _ := f.join(t, "main://doc.sketch")
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))
	code, out := f.co(t, http.MethodPost, a.Session+"/ticket", jsonBody(map[string]any{"client": a.Client, "token": a.Token}))
	require.Equal(t, http.StatusOK, code, "%v", out)
	spentLater, _ := out["ticket"].(string)
	require.NotEmpty(t, spentLater, "%v", out)

	conn, _, err := dialTicket(t, f.srv.URL, "/api/coedit/ws", a.Ticket)
	require.NoError(t, err, "an enabled member's ticket opens the socket")
	_ = conn.CloseNow()

	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	require.NoError(t, f.store.SetUserEnabled(context.Background(), admin.ID, false))
	conn, resp, err := dialTicket(t, f.srv.URL, "/api/coedit/ws", spentLater)
	if conn != nil {
		_ = conn.CloseNow()
	}
	require.Error(t, err, "a disabled member's ticket opened the editing-together socket")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

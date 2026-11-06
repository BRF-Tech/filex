package handlers_test

// What switching an account off (or suspending its tenant) must reach beyond
// the doors a request knocks on (GHSA-58wc-42xh-89v8, the owner's decisions
// of 2026-11-06):
//
//   - only a person signed in to the panel switches an ADMINISTRATOR's account
//     on or off: a key that could would undo the containment (a disabled
//     administrator's own key re-enabled its account) or lock out the people
//     who would revoke it;
//   - a realtime socket opened before the switch is closed, not left carrying
//     the account's change frames and presence until it drops by itself;
//   - a suspended tenant's sessions, keys and sockets stop the way a disabled
//     account's do. Suspension was a sign-in gate only: measured on the code
//     before this, a suspended tenant's signed-in member, its key on /api/ai,
//     /api/files and MCP, and a socket ticket it held all went on working.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An administrator's account is switched on or off by a person signed in to
// the panel, never by a key: not another administrator's, not the key's own,
// on /api/admin, /api/ai/admin or as the admin_users_update MCP tool, and the
// refusal is the session-only gate's own (session_required). An ordinary
// account stays a key's to switch, and an unchanged `enabled` is no switch.
func TestAdminAccounts_AKeyCannotSwitchAnAdministratorOnOrOff(t *testing.T) {
	f := newTokenGateFixture(t)
	ctx := context.Background()
	key := testutil.NewAPIToken(t, f.store, f.adminID, "read,write,delete,mcp,admin")
	code, body := f.adminCall(t, "", http.MethodPost, "/api/admin/users",
		`{"email":"switched-admin@test.local","password":"SwitchedAdm!n1","role":"admin"}`)
	require.Equal(t, http.StatusOK, code, "the session creates an administrator; body: %s", body)
	second, err := f.store.GetUserByEmail(ctx, "switched-admin@test.local")
	require.NoError(t, err)
	secondID := strconv.FormatInt(second.ID, 10)
	self := strconv.FormatInt(f.adminID, 10)
	member := strconv.FormatInt(f.userID, 10)
	enabled := func(id int64) bool {
		u, err := f.store.GetUser(ctx, id)
		require.NoError(t, err)
		return u.Enabled
	}

	for _, base := range []string{"/api/admin", "/api/ai/admin"} {
		t.Run(base, func(t *testing.T) {
			code, body := f.adminCall(t, key, http.MethodPatch, base+"/users/"+secondID, `{"enabled":false}`)
			refusedWithoutSession(t, code, body, "switch another administrator off")
			assert.True(t, enabled(second.ID), "a key switched an administrator off")

			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+self, `{"enabled":false}`)
			refusedWithoutSession(t, code, body, "switch its own administrator account off")
			assert.True(t, enabled(f.adminID), "a key switched its own administrator account off")

			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+secondID, `{"enabled":true,"display_name":"Ikinci"}`)
			assert.Equal(t, http.StatusOK, code, "an unchanged enabled is not a switch; body: %s", body)

			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+member, `{"enabled":false}`)
			require.Equal(t, http.StatusOK, code, "a key switches an ordinary account off; body: %s", body)
			assert.False(t, enabled(f.userID))
			code, body = f.adminCall(t, key, http.MethodPatch, base+"/users/"+member, `{"enabled":true}`)
			require.Equal(t, http.StatusOK, code, "a key switches an ordinary account back on; body: %s", body)
			assert.True(t, enabled(f.userID))
		})
	}

	// Switched off by a person, the administrator stays off whatever a key says.
	code, body = f.adminCall(t, "", http.MethodPatch, "/api/admin/users/"+secondID, `{"enabled":false}`)
	require.Equal(t, http.StatusOK, code, "the session switches an administrator off; body: %s", body)
	code, body = f.adminCall(t, key, http.MethodPatch, "/api/ai/admin/users/"+secondID, `{"enabled":true}`)
	refusedWithoutSession(t, code, body, "switch an administrator back on")
	code, body = mcpPost(t, &http.Client{}, f.srv.URL+"/api/ai/mcp", key,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_users_update","arguments":{"id":`+secondID+`,"body":{"enabled":true}}}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "session_required", "admin_users_update: switch an administrator back on; body: %s", body)
	assert.False(t, enabled(second.ID), "a key switched an administrator back on")

	code, body = f.adminCall(t, "", http.MethodPatch, "/api/admin/users/"+secondID, `{"enabled":true}`)
	require.Equal(t, http.StatusOK, code, "the session switches an administrator back on; body: %s", body)
	assert.True(t, enabled(second.ID))
}

// A tenant is suspended or resumed by a person signed in to the panel, never
// by a key: the platform administrator's admin key is refused session_required
// on /api/admin/providers, /api/ai/admin/providers and admin_tenants_update,
// and nothing of the request is written. Every other field of the tenant stays
// a key's to change, and an unchanged `enabled` is no switch.
func TestAdminAccounts_AKeyCannotSuspendOrResumeATenant(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	super, err := f.Store.GetUserByEmail(ctx, "super@platform.test")
	require.NoError(t, err)
	key := testutil.NewAPIToken(t, f.Store, super.ID, "read,write,delete,mcp,admin")
	id := strconv.FormatInt(f.ProvA, 10)
	tenant := func() (bool, string) {
		p, err := f.Store.GetProvider(ctx, f.ProvA)
		require.NoError(t, err)
		return p.Enabled, p.Name
	}
	keyPatch := func(path, body string) (int, map[string]any, string) {
		return ownerTokenCall(t, f.URL, key, http.MethodPatch, path, "application/json", []byte(body))
	}

	for i, base := range []string{"/api/admin/providers/", "/api/ai/admin/providers/"} {
		code, _, raw := keyPatch(base+id, `{"enabled":false,"name":"Alpha Held"}`)
		assert.Equal(t, http.StatusForbidden, code, "%s: a key suspended a tenant; body: %s", base, raw)
		assert.Contains(t, raw, "session_required", "%s", base)
		on, name := tenant()
		assert.True(t, on, "%s: a key suspended a tenant", base)
		assert.NotEqual(t, "Alpha Held", name, "%s: a refused update wrote a field", base)

		code, _, raw = keyPatch(base+id, `{"enabled":true,"name":"Alpha `+strconv.Itoa(i+1)+`"}`)
		assert.Equal(t, http.StatusOK, code, "%s: an unchanged enabled is no switch, and the name is a key's; body: %s", base, raw)
	}
	code, body := mcpPost(t, &http.Client{}, f.URL+"/api/ai/mcp", key,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_tenants_update","arguments":{"id":`+id+`,"enabled":false}}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "session_required", "admin_tenants_update: suspend; body: %s", body)
	on, _ := tenant()
	assert.True(t, on, "admin_tenants_update: a key suspended a tenant")

	// Suspended by a person, the tenant stays suspended whatever a key says.
	code, out := doJSON(t, f.Super, http.MethodPatch, f.URL+"/api/admin/providers/"+id, map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, code, "the session suspends a tenant: %v", out)
	code2, _, raw := keyPatch("/api/ai/admin/providers/"+id, `{"enabled":true}`)
	assert.Equal(t, http.StatusForbidden, code2, "a key resumed a tenant; body: %s", raw)
	assert.Contains(t, raw, "session_required")
	on, _ = tenant()
	assert.False(t, on, "a key resumed a tenant")
	code, out = doJSON(t, f.Super, http.MethodPatch, f.URL+"/api/admin/providers/"+id, map[string]any{"enabled": true})
	require.Equal(t, http.StatusOK, code, "the session resumes a tenant: %v", out)
	on, _ = tenant()
	assert.True(t, on)
}

// A tenant is deleted together with its people by a person signed in to the
// panel, never by a key: ?force=1 on /api/admin/providers, on
// /api/ai/admin/providers and admin_tenants_delete with force answer a key
// session_required and delete nothing - not the tenant, not an account, not
// a storage link. A key still creates a tenant and deletes an empty one; the
// session deletes the full one.
func TestAdminAccounts_AKeyCannotDeleteATenantWithItsPeople(t *testing.T) {
	f := newMTFix(t, true)
	ctx := context.Background()
	super, err := f.Store.GetUserByEmail(ctx, "super@platform.test")
	require.NoError(t, err)
	key := testutil.NewAPIToken(t, f.Store, super.ID, "read,write,delete,mcp,admin")
	id := strconv.FormatInt(f.ProvB, 10)
	intact := func(door string) {
		t.Helper()
		p, err := f.Store.GetProvider(ctx, f.ProvB)
		require.NoError(t, err, "%s: the tenant was deleted", door)
		require.NotNil(t, p, "%s: the tenant was deleted", door)
		u, err := f.Store.GetUser(ctx, f.UserB)
		assert.NoError(t, err, "%s: an account of the tenant was deleted", door)
		assert.NotNil(t, u, "%s: an account of the tenant was deleted", door)
		ids, err := f.Store.ListProviderStorageIDs(ctx, f.ProvB)
		require.NoError(t, err)
		assert.Contains(t, ids, f.StB.ID, "%s: the tenant's storage was unlinked", door)
	}

	for _, base := range []string{"/api/admin/providers/", "/api/ai/admin/providers/"} {
		code, _, raw := ownerTokenCall(t, f.URL, key, http.MethodDelete, base+id+"?force=1", "", nil)
		assert.Equal(t, http.StatusForbidden, code, "%s: a key deleted a tenant with its people; body: %s", base, raw)
		assert.Contains(t, raw, "session_required", "%s", base)
		intact(base)
		code, _, raw = ownerTokenCall(t, f.URL, key, http.MethodDelete, base+id, "", nil)
		assert.Equal(t, http.StatusConflict, code, "%s: without force a tenant with people is refused as before; body: %s", base, raw)
	}
	code, body := mcpPost(t, &http.Client{}, f.URL+"/api/ai/mcp", key,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_tenants_delete","arguments":{"id":`+id+`,"force":true}}}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "session_required", "admin_tenants_delete with force; body: %s", body)
	intact("admin_tenants_delete")

	// A key creates a tenant and deletes it while it is empty.
	code2, out, raw := ownerTokenCall(t, f.URL, key, http.MethodPost, "/api/admin/providers", "application/json",
		[]byte(`{"slug":"empty-one","name":"Empty"}`))
	require.Equal(t, http.StatusCreated, code2, "a key creates a tenant; body: %s", raw)
	emptyID := strconv.FormatInt(int64(out["id"].(float64)), 10)
	code2, _, raw = ownerTokenCall(t, f.URL, key, http.MethodDelete, "/api/ai/admin/providers/"+emptyID+"?force=1", "", nil)
	assert.Equal(t, http.StatusOK, code2, "a key deletes an empty tenant, force or not; body: %s", raw)

	// The session deletes the full one, its people with it.
	code3, gone := doJSON(t, f.Super, http.MethodDelete, f.URL+"/api/admin/providers/"+id+"?force=1", nil)
	require.Equal(t, http.StatusOK, code3, "the session deletes a tenant with its people: %v", gone)
	_, err = f.Store.GetUser(ctx, f.UserB)
	assert.Error(t, err, "the tenant's account went with it")
}

// openSocket dials /api/ws with client (its cookie jar, if any), header (a
// token) and ticket ("" for none), and fails the test when it does not open.
func openSocket(t *testing.T, base string, client *http.Client, header http.Header, ticket string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(base, "http") + "/api/ws"
	if ticket != "" {
		url += "?ticket=" + ticket
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	require.NoError(t, err, "the socket opens")
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// stillOpen pings an open socket and expects its pong.
func stillOpen(t *testing.T, door string, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`)), "%s: the socket takes a ping", door)
	for {
		_, data, err := conn.Read(ctx)
		require.NoError(t, err, "%s: an account that may act keeps its socket", door)
		if strings.Contains(string(data), `"pong"`) {
			return
		}
	}
}

// closedFor reads until the server closes the socket and asserts the close
// says why: 1008 with the reason code.
func closedFor(t *testing.T, door string, conn *websocket.Conn, reason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if !assert.True(t, errors.As(err, &ce), "%s: the server did not close the socket (%v)", door, err) {
			return
		}
		assert.Equal(t, websocket.StatusPolicyViolation, ce.Code, "%s", door)
		assert.Equal(t, reason, ce.Reason, "%s", door)
		return
	}
}

// Every door a realtime socket opens by - the session's cookie, an API token
// in the header, a ticket - stays open while the account may act and is closed
// once it is switched off.
func TestDisabledOwner_AnOpenRealtimeSocketIsClosed(t *testing.T) {
	restore := handlers.SetWSRecheckEvery(50 * time.Millisecond)
	defer restore()
	f := newSurfaceFixture(t)
	const email, pw = "live-leaver@test.local", "LiveLeaver!1"
	u := seedSharedUser(t, f.store, email, pw)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	session := &http.Client{Jar: jar}
	testutil.LoginAs(t, f.srv, session, email, pw)
	tok := testutil.NewAPIToken(t, f.store, u.ID, "read")
	code, out := doJSON(t, session, http.MethodPost, f.srv.URL+"/api/files/ws-ticket", nil)
	require.Equal(t, http.StatusOK, code, "%v", out)
	ticket, _ := out["ticket"].(string)

	sockets := map[string]*websocket.Conn{
		"cookie": openSocket(t, f.srv.URL, session, nil, ""),
		"token":  openSocket(t, f.srv.URL, &http.Client{}, http.Header{"X-Filex-Token": {tok}}, ""),
		"ticket": openSocket(t, f.srv.URL, &http.Client{}, nil, ticket),
	}
	time.Sleep(300 * time.Millisecond) // several re-checks of an account that may act
	for door, conn := range sockets {
		stillOpen(t, door, conn)
	}

	setEnabled(t, f, u.ID, false)
	for door, conn := range sockets {
		closedFor(t, door, conn, "account_disabled")
	}
}

// assertRefusedAsSuspended holds a refusal to the sign-in's own answer to a
// suspended tenant (auth.RefuseTenantLocked).
func assertRefusedAsSuspended(t *testing.T, door string, code int, out map[string]any, raw string) {
	t.Helper()
	if !assert.Equal(t, http.StatusForbidden, code, "%s: a suspended tenant's credential was let through; body: %.200s", door, raw) {
		return
	}
	assert.Equal(t, "tenant_suspended", out["reason"], "%s: body: %s", door, raw)
	assert.Equal(t, "sign-in is temporarily limited to the platform operator", out["error"], "%s: the sign-in's words; body: %s", door, raw)
}

// A suspended tenant's member is held off with everything it already holds:
// its signed-in session, its API key on the hybrid and the token-only doors
// and MCP, a socket ticket it took, an open socket. Each answers as the sign-in
// does (403 tenant_suspended), and resuming the tenant gives every one of them
// back.
func TestSuspendedTenant_ItsSessionsKeysAndSocketsStop(t *testing.T) {
	restore := handlers.SetWSRecheckEvery(50 * time.Millisecond)
	defer restore()
	f := newMTFix(t, true)
	tok := testutil.NewAPIToken(t, f.Store, f.UserA, "read,mcp")
	ticket := wsTicket(t, f, f.A)
	live := openSocket(t, f.URL, f.A, nil, "")
	suspend := func(on bool) {
		t.Helper()
		code, out := doJSON(t, f.Super, http.MethodPatch, f.URL+"/api/admin/providers/"+strconv.FormatInt(f.ProvA, 10),
			map[string]any{"enabled": !on})
		require.Equal(t, http.StatusOK, code, "%v", out)
	}
	session := func(t *testing.T) (int, map[string]any, string) {
		t.Helper()
		resp, err := f.A.Get(f.URL + "/api/files/quota/me")
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out, string(raw)
	}
	doors := map[string]func(t *testing.T) (int, map[string]any, string){
		"session": session,
		"key, hybrid door": func(t *testing.T) (int, map[string]any, string) {
			return ownerTokenCall(t, f.URL, tok, http.MethodGet, "/api/files/quota/me", "", nil)
		},
		"key, token-only door": func(t *testing.T) (int, map[string]any, string) {
			return ownerTokenCall(t, f.URL, tok, http.MethodGet, "/api/ai/root", "", nil)
		},
		"key, MCP": func(t *testing.T) (int, map[string]any, string) {
			return ownerTokenCall(t, f.URL, tok, http.MethodPost, "/api/ai/mcp", "application/json",
				[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		},
	}
	for name, door := range doors {
		code, _, raw := door(t)
		require.Equal(t, http.StatusOK, code, "%s works while the tenant is active: %s", name, raw)
	}

	suspend(true)

	for name, door := range doors {
		code, out, raw := door(t)
		assertRefusedAsSuspended(t, name, code, out, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.URL, "http")+"/api/ws?ticket="+ticket,
		&websocket.DialOptions{HTTPClient: &http.Client{}})
	if conn != nil {
		_ = conn.CloseNow()
	}
	assert.Error(t, err, "a suspended tenant's ticket opened the socket")
	if assert.NotNil(t, resp) {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
	closedFor(t, "open socket", live, "tenant_suspended")

	suspend(false)
	for name, door := range doors {
		code, _, raw := door(t)
		assert.Equal(t, http.StatusOK, code, "%s works again once the tenant is resumed: %s", name, raw)
	}
}

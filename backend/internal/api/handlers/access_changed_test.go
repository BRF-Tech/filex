package handlers_test

// #196 - an explorer keeps the answers its right-click menu depends on (which
// per-folder permissions are held at a path, may this person start encrypting
// there) so a menu opens on them at once (packages/core lib/menuAnswers). The
// copy stays honest because the server says when it may have gone stale: an
// `access.changed` frame on the live socket, to the person a grant names, or
// to everybody connected when a role, a rule or a policy changed. The frame
// names no path, no person and no reason (internal/realtime/access.go).
//
// Measured through the real router and a real socket, so the wiring in
// api.BuildRouter (handlers.SetAccessEmitter, perm.SetInvalidateHook) is what
// is under test, not a stand-in.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// accessSocket opens the live socket through the cookie door of c's session.
// Subscribed to nothing: the frame reaches a socket whatever folder it shows.
func accessSocket(t *testing.T, baseURL string, c *http.Client) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := strings.Replace(baseURL, "http://", "ws://", 1) + "/api/ws"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// accessListener collects the access.changed frames a socket receives. One
// reader goroutine for the socket's life: ⚠ a Read whose context ends closes a
// coder/websocket connection, so a test cannot poll with timed reads.
type accessListener struct{ frames chan map[string]any }

func listenAccess(conn *websocket.Conn) *accessListener {
	l := &accessListener{frames: make(chan map[string]any, 16)}
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil && m["type"] == "access.changed" {
				select {
				case l.frames <- m:
				default:
				}
			}
		}
	}()
	return l
}

// next answers the next access.changed frame within `within`, or nil.
func (l *accessListener) next(within time.Duration) map[string]any {
	select {
	case m := <-l.frames:
		return m
	case <-time.After(within):
		return nil
	}
}

// settle lets a burst the setup itself caused (accounts made, signed in) go
// out - the hub gathers for 250 ms - and forgets it.
func (l *accessListener) settle() {
	time.Sleep(500 * time.Millisecond)
	for {
		select {
		case <-l.frames:
		default:
			return
		}
	}
}

// accessFixture: an administrator, an RBAC storage and two people, each with
// an open socket.
type accessFixture struct {
	url        string
	admin      *http.Client
	uid, vid   int64
	uWS, vWS   *accessListener
	storageKey string
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	srv, admin, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, admin, email, pw)
	name := fmt.Sprintf("acc%d", time.Now().UnixNano()%1_000_000)
	st, raw := doReq(t, admin, http.MethodPost, srv.URL+"/api/admin/storages", model.Storage{
		Name:          name,
		Driver:        "local",
		MountPath:     "/data",
		ConfigJSON:    json.RawMessage(fmt.Sprintf(`{"root":%q}`, t.TempDir())),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
		RBACEnabled:   true,
	})
	require.Equal(t, http.StatusOK, st, "create storage: %s", raw)
	uid := createUser(t, srv.URL, admin, "acc-u@test.local", "UserPass1!", model.RoleUser)
	vid := createUser(t, srv.URL, admin, "acc-v@test.local", "UserPass1!", model.RoleUser)
	uc := freshClient(t)
	testutil.LoginAs(t, srv, uc, "acc-u@test.local", "UserPass1!")
	vc := freshClient(t)
	testutil.LoginAs(t, srv, vc, "acc-v@test.local", "UserPass1!")
	f := &accessFixture{
		url: srv.URL, admin: admin, uid: uid, vid: vid,
		uWS: listenAccess(accessSocket(t, srv.URL, uc)), vWS: listenAccess(accessSocket(t, srv.URL, vc)),
		storageKey: name,
	}
	f.uWS.settle()
	f.vWS.settle()
	return f
}

func TestAccessChanged_AGrantReachesItsPersonAndNobodyElse(t *testing.T) {
	f := newAccessFixture(t)

	st, raw := doReq(t, f.admin, http.MethodPost, f.url+"/api/files/permissions",
		map[string]any{"path": f.storageKey + "://alfa", "user_id": f.uid, "level": "editor"})
	require.Equal(t, http.StatusOK, st, "grant: %s", raw)

	m := f.uWS.next(3 * time.Second)
	require.NotNil(t, m, "the person the grant names hears that their access changed")
	assert.Equal(t, map[string]any{"type": "access.changed"}, m, "and nothing else: no path, no person, no reason")
	assert.Nil(t, f.vWS.next(700*time.Millisecond), "somebody the grant does not name hears nothing")

	// Taking it back says so as well.
	var grant struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &grant))
	require.NotZero(t, grant.ID)
	st, raw = doReq(t, f.admin, http.MethodDelete, fmt.Sprintf("%s/api/files/permissions/%d", f.url, grant.ID), nil)
	require.Equal(t, http.StatusOK, st, "revoke: %s", raw)
	assert.NotNil(t, f.uWS.next(3*time.Second), "a revoked grant is a change too")
	assert.Nil(t, f.vWS.next(700*time.Millisecond))
}

func TestAccessChanged_ARoleChangeTellsThatPersonOnly(t *testing.T) {
	f := newAccessFixture(t)

	// A person's role changes: the write invalidates the cached permissions
	// for that account (perm.InvalidateFor), whose hook is the hub.
	st, raw := doReq(t, f.admin, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d", f.url, f.vid),
		map[string]any{"role": model.RoleViewer})
	require.Equal(t, http.StatusOK, st, "role change: %s", raw)

	m := f.vWS.next(3 * time.Second)
	require.NotNil(t, m, "the person whose role changed hears it")
	assert.Equal(t, map[string]any{"type": "access.changed"}, m)
	assert.Nil(t, f.uWS.next(700*time.Millisecond), "somebody else's role is not their news")
}

func TestAccessChanged_APlatformChangeTellsEverybodyAndSaysSo(t *testing.T) {
	f := newAccessFixture(t)

	// The install's encryption policy (a single-tenant install: the
	// platform's): everybody may be concerned.
	st, raw := doReq(t, f.admin, http.MethodPatch, f.url+"/api/admin/e2e", map[string]any{"policy": "approval"})
	require.Equal(t, http.StatusOK, st, "policy: %s", raw)

	for who, ws := range map[string]*accessListener{"u": f.uWS, "v": f.vWS} {
		m := ws.next(3 * time.Second)
		require.NotNil(t, m, "%s hears it", who)
		assert.Equal(t, map[string]any{"type": "access.changed", "scope": "all"}, m, who)
	}
}

// On a multi-tenant install a tenant's change reaches the sockets of that
// tenant's accounts and no others: not another tenant's, and not the
// platform's own accounts - the frame names nothing, but its timing alone
// would tell them that something changed there. A change at the platform level
// reaches everybody.
func TestAccessChanged_ATenantsChangeStaysInTheTenant(t *testing.T) {
	srv, superC, store := multiTenantServer(t)
	superEmail, superPw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, superC, superEmail, superPw)
	acme, acmeEmail, acmePw := seedTenant(t, store, "acme", "admin@acme.test", false)
	_, globexEmail, globexPw := seedTenant(t, store, "globex", "admin@globex.test", false)
	acmeC := freshClient(t)
	testutil.LoginAs(t, srv, acmeC, acmeEmail, acmePw)
	globexC := freshClient(t)
	testutil.LoginAs(t, srv, globexC, globexEmail, globexPw)

	acmeWS := listenAccess(accessSocket(t, srv.URL, acmeC))
	globexWS := listenAccess(accessSocket(t, srv.URL, globexC))
	superWS := listenAccess(accessSocket(t, srv.URL, superC))
	for _, l := range []*accessListener{acmeWS, globexWS, superWS} {
		l.settle()
	}

	// acme's administrator changes acme's encryption policy.
	st, raw := doReq(t, acmeC, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": "approval"})
	require.Equal(t, http.StatusOK, st, "acme policy: %s", raw)
	m := acmeWS.next(3 * time.Second)
	require.NotNil(t, m, "acme's own people hear it")
	assert.Equal(t, map[string]any{"type": "access.changed", "scope": "all"}, m)
	assert.Nil(t, globexWS.next(700*time.Millisecond), "another tenant hears nothing")
	assert.Nil(t, superWS.next(700*time.Millisecond), "nor do the platform's own accounts")

	// The operator switches acme's ceiling: still acme's news only.
	st, raw = doReq(t, superC, http.MethodPatch, fmt.Sprintf("%s/api/admin/e2e/tenants/%d", srv.URL, acme),
		map[string]any{"e2e_allowed": false})
	require.Equal(t, http.StatusOK, st, "ceiling: %s", raw)
	assert.NotNil(t, acmeWS.next(3*time.Second), "acme hears its ceiling changed")
	assert.Nil(t, globexWS.next(700*time.Millisecond))
	assert.Nil(t, superWS.next(700*time.Millisecond))

	// The platform's own policy is a platform-level change: every tenant hears it.
	st, raw = doReq(t, superC, http.MethodPatch, srv.URL+"/api/admin/e2e", map[string]any{"policy": "approval"})
	require.Equal(t, http.StatusOK, st, "platform policy: %s", raw)
	for who, ws := range map[string]*accessListener{"acme": acmeWS, "globex": globexWS, "platform": superWS} {
		m := ws.next(3 * time.Second)
		require.NotNil(t, m, "%s hears a platform-level change", who)
		assert.Equal(t, "all", m["scope"], who)
	}
}

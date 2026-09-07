package handlers_test

// The live-collaboration socket across the tenant boundary.
//
// `/api/ws` has two doors. The COOKIE door runs through the router group, so
// `auth.TenantResolver` has already attached a scope by the time the handler
// sees the request; `resolveSubscribe` then enumerates ListEnabledStorages,
// tenantstore confines it, and a foreign adapter simply does not resolve.
//
// The TICKET door authenticates AFTER all middleware has run — the whole point
// of a ticket is that the embedded, cross-origin client carries neither cookie
// nor bearer. `Handle` restores the ticket's USER into the connection context
// (`auth.WithUser`) so RBAC has somebody to ask about, but it never restored
// the SCOPE. So `resolveSubscribe` ran on an unscoped context, got every
// storage on the instance, and a ticketed subscriber could join any tenant's
// room: live change frames for that folder, plus a presence roster carrying
// other tenants' display names, e-mail local-parts and avatars.
//
// The asymmetry is the tell: the same subscribe, refused on one door and
// granted on the other, by the same server, for the same user.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// wsTicket mints a ticket for the client's session.
func wsTicket(t *testing.T, f *mtFix, c *http.Client) string {
	t.Helper()
	status, body := doJSON(t, c, http.MethodPost, f.URL+"/api/files/ws-ticket", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	tok, _ := body["ticket"].(string)
	require.NotEmpty(t, tok)
	return tok
}

// wsSubscribeReply opens a socket, subscribes to rawPath, and returns the first
// frame that answers it. `ticket` empty uses the cookie door instead.
//
// ⚠⚠ The ticketed variants MUST dial with a cookie-less client, and this is not
// hygiene — it is the difference between measuring the hole and measuring
// nothing. A ticket exists precisely because the embedded, cross-origin client
// has no cookie; if the dial carries one, `auth.MiddlewareWithToken` resolves
// the user, `auth.TenantResolver` attaches the scope, and the connection is
// confined by the middleware the ticket path is supposed to have bypassed. The
// first version of this suite dialled with the session client and reported all
// five cases SAFE on the unfixed build. See wsDialClient.
func wsSubscribeReply(t *testing.T, f *mtFix, c *http.Client, ticket, rawPath string) map[string]any {
	t.Helper()
	url := strings.Replace(f.URL, "http://", "ws://", 1) + "/api/ws"
	if ticket != "" {
		url += "?ticket=" + ticket
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c})
	require.NoError(t, err)
	defer conn.CloseNow()

	req, _ := json.Marshal(map[string]string{"type": "subscribe", "path": rawPath})
	require.NoError(t, conn.Write(ctx, websocket.MessageText, req))

	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	defer readCancel()
	for {
		_, data, rerr := conn.Read(readCtx)
		if rerr != nil {
			return nil // nothing came back within the window
		}
		var frame map[string]any
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame["type"] {
		case "presence", "error":
			return frame
		}
	}
}

// wsConn is an open socket plus the helpers to drive it, for the cases that
// need TWO connections alive at once (the presence-roster leak: somebody has to
// already be in the room for the roster to name them).
type wsConn struct {
	conn *websocket.Conn
	ctx  context.Context
}

func wsOpen(t *testing.T, f *mtFix, c *http.Client, ticket string) *wsConn {
	t.Helper()
	url := strings.Replace(f.URL, "http://", "ws://", 1) + "/api/ws"
	if ticket != "" {
		url += "?ticket=" + ticket
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: c})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow(); cancel() })
	return &wsConn{conn: conn, ctx: ctx}
}

func (w *wsConn) subscribe(t *testing.T, rawPath string) map[string]any {
	t.Helper()
	req, _ := json.Marshal(map[string]string{"type": "subscribe", "path": rawPath})
	require.NoError(t, w.conn.Write(w.ctx, websocket.MessageText, req))
	readCtx, cancel := context.WithTimeout(w.ctx, 3*time.Second)
	defer cancel()
	for {
		_, data, rerr := w.conn.Read(readCtx)
		if rerr != nil {
			return nil
		}
		var frame map[string]any
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame["type"] {
		case "presence", "error":
			return frame
		}
	}
}

// TestWSTicket_DoesNotExposeAnotherTenantsPresenceRoster — the frame the red
// proof above returned carried an EMPTY roster only because nobody else was in
// the room. With a bravo member already watching the folder, the same crossing
// hands the alpha client that person's display name.
//
// RED PROOF (unfixed code):
//
//	{"type":"presence","path":"bravo://","users":[{"id":4,"name":"member"}]}
//
// where user 4 is `member@bravo.test` — the other customer's account, named to
// somebody who has no business knowing it exists. (The label is the display
// name, falling back to the e-mail local-part; avatars ride along the same
// frame.)
func TestWSTicket_DoesNotExposeAnotherTenantsPresenceRoster(t *testing.T) {
	f := newMTFix(t, true)

	// A bravo member is watching their own folder.
	bravo := wsOpen(t, f, f.B, "")
	require.Equal(t, "presence", bravo.subscribe(t, "bravo://")["type"])

	alpha := wsOpen(t, f, wsDialClient(), wsTicket(t, f, f.A))
	frame := alpha.subscribe(t, "bravo://")
	require.NotNil(t, frame)
	require.Equal(t, "error", frame["type"], "%v", frame)
	raw, _ := json.Marshal(frame)
	require.NotContains(t, string(raw), "member",
		"no name from the other tenant's roster may reach this connection: %s", raw)
}

// wsDialClient is a client with NO cookie jar — the embedded webcomponent's
// situation, and the only one in which a ticket means anything.
func wsDialClient() *http.Client { return &http.Client{} }

// TestWSTicket_CannotSubscribeToAnotherTenantsFolder
//
// RED PROOF (unfixed code): an alpha member minted a ticket, opened
// `ws://…/api/ws?ticket=…`, sent
//
//	{"type":"subscribe","path":"bravo://"}
//
// and the server answered
//
//	{"type":"presence","path":"bravo://","users":[{"id":3,"name":"member"}]}
//
// — a successful join of tenant bravo's room. From that point the connection
// receives every change frame for that folder, and the roster names whoever
// else is in it.
func TestWSTicket_CannotSubscribeToAnotherTenantsFolder(t *testing.T) {
	f := newMTFix(t, true)
	frame := wsSubscribeReply(t, f, wsDialClient(), wsTicket(t, f, f.A), "bravo://")
	require.NotNil(t, frame, "the connection must answer, not hang")
	require.Equal(t, "error", frame["type"],
		"a ticketed client must not join another tenant's room: %v", frame)
	require.Equal(t, "not_found", frame["error"],
		"and the refusal must look like a folder that is not there: %v", frame)
}

// TestWSTicket_OwnTenantStillSubscribes — the ticket door is what the embedded
// webcomponent uses; breaking it would take live collaboration away from every
// embedder. Same call, own storage.
func TestWSTicket_OwnTenantStillSubscribes(t *testing.T) {
	f := newMTFix(t, true)
	frame := wsSubscribeReply(t, f, wsDialClient(), wsTicket(t, f, f.A), "alpha://")
	require.NotNil(t, frame)
	require.Equal(t, "presence", frame["type"], "%v", frame)
}

// TestWSTicket_SupertenantStillSubscribesAnywhere — the platform operator
// watches tenants' folders during support work.
func TestWSTicket_SupertenantStillSubscribesAnywhere(t *testing.T) {
	f := newMTFix(t, true)
	frame := wsSubscribeReply(t, f, wsDialClient(), wsTicket(t, f, f.Super), "bravo://")
	require.NotNil(t, frame)
	require.Equal(t, "presence", frame["type"], "%v", frame)
}

// TestWSCookie_TenantBoundaryUnchanged — the cookie door was NEVER the hole,
// and this pins that down so a future change cannot quietly move the boundary
// onto the ticket path only. It also documents where the asymmetry came from:
// this door has a scope because the middleware put one there.
func TestWSCookie_TenantBoundaryUnchanged(t *testing.T) {
	f := newMTFix(t, true)

	own := wsSubscribeReply(t, f, f.A, "", "alpha://")
	require.NotNil(t, own)
	require.Equal(t, "presence", own["type"], "%v", own)

	foreign := wsSubscribeReply(t, f, f.A, "", "bravo://")
	require.NotNil(t, foreign)
	require.Equal(t, "error", foreign["type"], "%v", foreign)
}

// TestWSTenantIsolation_SingleTenantInstallUnaffected — mode off: a ticketed
// client still reaches every storage, exactly as before. Passes on `main`.
func TestWSTenantIsolation_SingleTenantInstallUnaffected(t *testing.T) {
	f := newMTFix(t, false)

	ticketed := wsSubscribeReply(t, f, wsDialClient(), wsTicket(t, f, f.A), "bravo://")
	require.NotNil(t, ticketed)
	require.Equal(t, "presence", ticketed["type"],
		"one instance, one boundary: every storage is subscribable: %v", ticketed)

	cookie := wsSubscribeReply(t, f, f.A, "", "bravo://")
	require.NotNil(t, cookie)
	require.Equal(t, "presence", cookie["type"], "%v", cookie)
}

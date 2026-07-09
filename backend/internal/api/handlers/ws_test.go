package handlers_test

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// newWSFixture builds an httptest server that upgrades /api/ws with a given
// authenticated user injected into the context (standing in for the auth
// middleware the real route sits behind). Returns the ws:// URL and the hub so
// tests can drive change events from the "mutation" side.
func newWSFixture(t *testing.T, user *model.User) (string, *realtime.Hub, db.Store) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	_, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"/tmp/ws-test"}`),
	})
	require.NoError(t, err)

	hub := realtime.NewHub()
	wsh := handlers.NewWS(store, nil, hub, nil, "") // nil ACL → allow (RBAC covered elsewhere)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsh.Handle(w, r.WithContext(auth.WithUser(r.Context(), user)))
	}))
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws", hub, store
}

func wsSend(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, raw))
}

// wsReadType reads frames until one of the wanted type arrives (skipping other
// types), or fails on timeout.
func wsReadType(t *testing.T, conn *websocket.Conn, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		typ, data, err := conn.Read(ctx)
		cancel()
		require.NoError(t, err)
		if typ != websocket.MessageText {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal(data, &m))
		if m["type"] == want {
			return m
		}
	}
	t.Fatalf("timed out waiting for %q frame", want)
	return nil
}

// TestWSSubscribePresenceAndChange is the end-to-end vertical slice: a real
// WebSocket client subscribes to a folder, receives a presence frame listing
// itself, then receives a change frame when a mutation emits into that folder.
func TestWSSubscribePresenceAndChange(t *testing.T) {
	url, hub, store := newWSFixture(t, &model.User{ID: 1, DisplayName: "Ayşe", Email: "ayse@brf.sh"})

	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	wsSend(t, conn, map[string]any{"type": "subscribe", "path": "main://reports"})

	// Presence answers "who ELSE is here" — a lone subscriber gets an empty
	// roster (self is excluded server-side).
	pres := wsReadType(t, conn, "presence")
	require.Equal(t, "main://reports", pres["path"])
	users, _ := pres["users"].([]any)
	require.Len(t, users, 0)

	// Another user joins via the hub → the live socket sees THEM.
	storagesForJoin, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	other := realtime.NewClient(9, "Cem", 16)
	hub.Subscribe(other, storagesForJoin[0].ID, "reports", "main://reports")
	joined := wsReadType(t, conn, "presence")
	jusers, _ := joined["users"].([]any)
	require.Len(t, jusers, 1)
	require.Equal(t, "Cem", jusers[0].(map[string]any)["name"])

	// Resolve the storage id the same way the handler does, then emit a change
	// as a mutation handler would.
	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, storages)
	hub.EmitChange(storages[0].ID, "reports", realtime.ChangeEvent{Action: "create", Name: "q3.pdf"})

	chg := wsReadType(t, conn, "change")
	require.Equal(t, "create", chg["action"])
	require.Equal(t, "q3.pdf", chg["name"])
	require.Equal(t, "main://reports", chg["path"])
}

// TestWSFocusPresence: a live socket sees another room member's focus reflected
// in a presence broadcast.
func TestWSFocusPresence(t *testing.T) {
	url, hub, store := newWSFixture(t, &model.User{ID: 2, DisplayName: "Burak"})
	ctx := context.Background()
	st, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, st)

	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")
	wsSend(t, conn, map[string]any{"type": "subscribe", "path": "main://x"})
	_ = wsReadType(t, conn, "presence") // initial roster (Burak alone)

	// A different user joins the same room via the hub and focuses a file.
	other := realtime.NewClient(9, "Cem", 16)
	hub.Subscribe(other, st[0].ID, "x", "main://x")
	hub.SetFocus(other, "rapor.pdf")

	// The live socket should observe a presence frame that includes Cem/rapor.pdf.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		pres := wsReadType(t, conn, "presence")
		users, _ := pres["users"].([]any)
		for _, u := range users {
			um := u.(map[string]any)
			if um["name"] == "Cem" && um["file"] == "rapor.pdf" {
				return // success
			}
		}
	}
	t.Fatal("did not observe Cem focused on rapor.pdf in presence")
}

// TestWSSubscribeUnknownAdapter: a bad adapter yields an error frame, not a
// crash, and the connection stays usable.
func TestWSSubscribeUnknownAdapter(t *testing.T) {
	url, _, _ := newWSFixture(t, &model.User{ID: 1, DisplayName: "Ayşe"})
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	wsSend(t, conn, map[string]any{"type": "subscribe", "path": "nope://whatever"})
	errFrame := wsReadType(t, conn, "error")
	require.Equal(t, "not_found", errFrame["error"])

	// Connection still alive: ping → pong.
	wsSend(t, conn, map[string]any{"type": "ping"})
	_ = wsReadType(t, conn, "pong")
}

// newWSTicketFixture builds a ws server with a TicketStore but NO cookie user —
// identity comes purely from a ticket, exactly like an embedded consumer that
// fetched its ticket through the host proxy. Returns the mint store so a test can
// forge a confined ticket (the work.brf.sh / fishapp X-Filex-Root scenario).
func newWSTicketFixture(t *testing.T) (string, *realtime.Hub, db.Store, *realtime.TicketStore) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	_, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"/tmp/ws-test"}`),
	})
	require.NoError(t, err)

	tickets := realtime.NewTicketStore()
	hub := realtime.NewHub()
	wsh := handlers.NewWS(store, nil, hub, tickets, "https://fm.brf.sh")
	srv := httptest.NewServer(http.HandlerFunc(wsh.Handle)) // no user injected
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws", hub, store, tickets
}

// TestWSConfinedTicketSubscribe reproduces the embedded (work.brf.sh) flow: the
// explorer is confine-unaware and subscribes with a confine-RELATIVE path
// ("main://" for its root), while the ticket confines it to "projeler/5". The
// handler must (a) NOT forbid it, (b) join the ABSOLUTE room so a mutation into
// projeler/5 reaches it, and (c) echo the client's OWN relative path in frames.
func TestWSConfinedTicketSubscribe(t *testing.T) {
	url, hub, store, tickets := newWSTicketFixture(t)
	tok, err := tickets.Mint(realtime.Ticket{
		UserID: 1, Name: "Embedded", ConfineAdapter: "main", ConfineRel: "projeler/5",
	}, time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url+"?ticket="+tok, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Subscribe to the explorer's view of "root" — a bare confine-relative path.
	wsSend(t, conn, map[string]any{"type": "subscribe", "path": "main://"})

	pres := wsReadType(t, conn, "presence")
	require.Equal(t, "main://", pres["path"], "frame must echo the client's own relative path")
	users, _ := pres["users"].([]any)
	require.Len(t, users, 0, "lone subscriber sees an empty roster (self excluded)")

	// A mutation into the ABSOLUTE folder (what the proxy resolves) must reach the
	// confined viewer — proving the subscribe joined the absolute room.
	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	hub.EmitChange(storages[0].ID, "projeler/5", realtime.ChangeEvent{Action: "upload", Name: "fis.pdf"})

	chg := wsReadType(t, conn, "change")
	require.Equal(t, "upload", chg["action"])
	require.Equal(t, "fis.pdf", chg["name"])
	require.Equal(t, "main://", chg["path"], "change frame must echo the relative path too")
}

// TestWSConfinedTicketAbsoluteSubscribe reproduces the REAL webcomponent
// behavior under confinement: the backend returns storage-absolute dirnames, so
// the explorer subscribes with the ABSOLUTE path ("main://projeler/5"). That
// must join the SAME room the mutation handlers emit into — NOT a doubled
// "projeler/5/projeler/5" room (the v0.1.78 regression the dummy-app test
// caught: presence looked fine between embedded viewers but changes never
// arrived and native viewers were invisible).
func TestWSConfinedTicketAbsoluteSubscribe(t *testing.T) {
	url, hub, store, tickets := newWSTicketFixture(t)
	tok, err := tickets.Mint(realtime.Ticket{
		UserID: 1, Name: "Embedded", ConfineAdapter: "main", ConfineRel: "projeler/5",
	}, time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url+"?ticket="+tok, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	wsSend(t, conn, map[string]any{"type": "subscribe", "path": "main://projeler/5"})
	pres := wsReadType(t, conn, "presence")
	require.Equal(t, "main://projeler/5", pres["path"], "frame echoes the client's own absolute path")

	// The native (absolute) room must be the one this client joined.
	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	hub.EmitChange(storages[0].ID, "projeler/5", realtime.ChangeEvent{Action: "upload", Name: "abs.pdf"})
	chg := wsReadType(t, conn, "change")
	require.Equal(t, "abs.pdf", chg["name"])

	// A native viewer of the same folder shares the room (cross-visibility).
	native := realtime.NewClient(2, "Native", 16)
	hub.Subscribe(native, storages[0].ID, "projeler/5", "main://projeler/5")
	joined := wsReadType(t, conn, "presence")
	users, _ := joined["users"].([]any)
	require.Len(t, users, 1)
	require.Equal(t, "Native", users[0].(map[string]any)["name"])
}

// TestWSConfinedTicketCannotEscape: a confined ticket may not watch a sibling
// outside its root, even via "..". The subscribe is rejected with "forbidden".
func TestWSConfinedTicketCannotEscape(t *testing.T) {
	url, _, _, tickets := newWSTicketFixture(t)
	tok, err := tickets.Mint(realtime.Ticket{
		UserID: 1, Name: "Embedded", ConfineAdapter: "main", ConfineRel: "projeler/5",
	}, time.Minute)
	require.NoError(t, err)

	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url+"?ticket="+tok, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// "../secret" resolves to projeler/secret — inside projeler but NOT under
	// projeler/5, so confinement must reject it.
	wsSend(t, conn, map[string]any{"type": "subscribe", "path": "main://../secret"})
	errFrame := wsReadType(t, conn, "error")
	require.Equal(t, "forbidden", errFrame["error"])
}

// mintVia calls the Ticket handler with the given context decorations +
// headers and returns the minted realtime.Ticket for inspection. tokenUser
// mimics what the auth middleware resolves from X-Filex-Token-User.
func mintVia(t *testing.T, user *model.User, token *model.APIToken, tokenUser string, headers map[string]string) realtime.Ticket {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	tickets := realtime.NewTicketStore()
	wsh := handlers.NewWS(store, nil, realtime.NewHub(), tickets, "https://fm.brf.sh")

	req := httptest.NewRequest(http.MethodPost, "/api/files/ws-ticket", nil)
	ctx := auth.WithUser(req.Context(), user)
	if token != nil {
		ctx = auth.WithToken(ctx, token)
	}
	if tokenUser != "" {
		ctx = auth.WithTokenUser(ctx, tokenUser)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	wsh.Ticket(rec, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp struct {
		Ticket string `json:"ticket"`
		WsURL  string `json:"ws_url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "wss://fm.brf.sh/api/ws", resp.WsURL)
	tk, ok := tickets.Consume(resp.Ticket)
	require.True(t, ok)
	return tk
}

// TestWSTicketIdentity: what name/key a minted ticket carries per auth mode.
//   - cookie session → the user's own display name; override headers IGNORED
//   - API token      → its token USERNAME (default = first allow-list entry,
//     else the label) — a shared proxy token must not show its owner
//     account's name to everyone
//   - API token + X-Filex-Presence-Name → "Kişi (username)" combined display
//     (trusted: proxies strip these headers from client requests)
func TestWSTicketIdentity(t *testing.T) {
	admin := &model.User{ID: 1, DisplayName: "admin", Email: "admin@local"}

	cookie := mintVia(t, admin, nil, "", map[string]string{
		"X-Filex-Presence-Name": "Sahte",
		"X-Filex-Presence-Key":  "spoof-1",
	})
	require.Equal(t, "admin", cookie.Name, "cookie sessions must ignore override headers")
	require.Empty(t, cookie.PresenceKey)

	tok := &model.APIToken{ID: 5, UserID: 1, Label: "work-panel"}
	viaToken := mintVia(t, admin, tok, "", nil)
	require.Equal(t, "work-panel", viaToken.Name, "no usernames configured → the label IS the default username")
	require.Equal(t, "tok-5-work-panel", viaToken.PresenceKey,
		"keyless token connections default to their own (token, username) identity")

	// A token with a username allow-list + the middleware-resolved selection.
	named := &model.APIToken{ID: 6, UserID: 1, Label: "shared", Usernames: "work,fishapp"}
	require.Equal(t, "work", mintVia(t, admin, named, "", nil).Name, "default = first allow-list entry")
	sel := mintVia(t, admin, named, "fishapp", nil)
	require.Equal(t, "fishapp", sel.Name, "the resolved X-Filex-Token-User wins")
	require.Equal(t, "tok-6-fishapp", sel.PresenceKey)

	// Proxy-stamped real person combines with the token username.
	stamped := mintVia(t, admin, named, "work", map[string]string{
		"X-Filex-Presence-Name": "  Burak  Fun ",
		"X-Filex-Presence-Key":  "work-7",
	})
	require.Equal(t, "Burak Fun (work)", stamped.Name)
	require.Equal(t, "work-7", stamped.PresenceKey)

	badKey := mintVia(t, admin, tok, "", map[string]string{
		"X-Filex-Presence-Key": "kötü anahtar!",
	})
	require.Equal(t, "tok-5-work-panel", badKey.PresenceKey,
		"malformed keys are dropped entirely and fall back to the token's own identity")

	// HTTP headers are latin-1 territory — proxies RFC 2047-encode non-ASCII
	// names (Turkish characters) and the mint must decode them.
	encoded := mintVia(t, admin, tok, "", map[string]string{
		"X-Filex-Presence-Name": mime.BEncoding.Encode("utf-8", "Gökçil Ayşe"),
		"X-Filex-Presence-Key":  "work-8",
	})
	require.Equal(t, "Gökçil Ayşe (work-panel)", encoded.Name)
}

// TestTokenUsernameResolution locks the ResolveUsername contract the auth
// middlewares enforce: empty → default, allow-listed → chosen, anything
// else → rejected (the request must 403, not silently blend into default).
func TestTokenUsernameResolution(t *testing.T) {
	tok := &model.APIToken{ID: 9, Label: "anahtar", Usernames: "work,fishapp"}

	got, ok := tok.ResolveUsername("")
	require.True(t, ok)
	require.Equal(t, "work", got)

	got, ok = tok.ResolveUsername("fishapp")
	require.True(t, ok)
	require.Equal(t, "fishapp", got)

	_, ok = tok.ResolveUsername("saldirgan")
	require.False(t, ok)

	// Legacy token (no list): only empty or the label itself resolve.
	legacy := &model.APIToken{ID: 10, Label: "eski"}
	got, ok = legacy.ResolveUsername("")
	require.True(t, ok)
	require.Equal(t, "eski", got)
	got, ok = legacy.ResolveUsername("eski")
	require.True(t, ok)
	require.Equal(t, "eski", got)
	_, ok = legacy.ResolveUsername("baska")
	require.False(t, ok)
}

// TestWSUnauthorized: no user in context → 101 upgrade is refused with 401.
func TestWSUnauthorized(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	wsh := handlers.NewWS(store, nil, realtime.NewHub(), nil, "")
	srv := httptest.NewServer(http.HandlerFunc(wsh.Handle)) // no user injected
	defer srv.Close()

	ctx := context.Background()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	_, resp, err := websocket.Dial(ctx, url, nil)
	require.Error(t, err)
	if resp != nil {
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
}

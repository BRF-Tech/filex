package handlers_test

import (
	"context"
	"encoding/json"
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

	pres := wsReadType(t, conn, "presence")
	require.Equal(t, "main://reports", pres["path"])
	users, _ := pres["users"].([]any)
	require.Len(t, users, 1)
	require.Equal(t, "Ayşe", users[0].(map[string]any)["name"])

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
	require.Len(t, users, 1)

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

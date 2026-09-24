package handlers_test

// The recursive, presence-less `watch` subscription the desktop sync engine
// uses to hear about server-side edits the moment they happen. It is a second
// door onto the same change stream, so every test here is the watch twin of a
// subscribe test that already exists: same confinement, same RBAC, same tenant
// boundary — plus the acknowledgement an old server cannot send, which is how
// the engine tells "nothing changed" from "nothing will ever be announced".

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
)

func wsWatch(t *testing.T, conn *websocket.Conn, paths ...string) map[string]any {
	t.Helper()
	wsSend(t, conn, map[string]any{"type": "watch", "paths": paths})
	return wsReadType(t, conn, "watching")
}

func anyStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(string))
	}
	return out
}

func TestWSWatchAcknowledgesAndDeliversRecursively(t *testing.T) {
	url, hub, store := newWSFixture(t, &model.User{ID: 1, DisplayName: "Burak"})
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, url, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	ack := wsWatch(t, conn, "main://reports", "nope://x", "main://reports")
	require.Equal(t, []string{"main://reports"}, anyStrings(ack["roots"]),
		"an accepted root is listed once, a refused one is not")
	errs, _ := ack["errors"].([]any)
	require.Len(t, errs, 1)
	require.Equal(t, "nope://x", errs[0].(map[string]any)["path"])
	require.Equal(t, "not_found", errs[0].(map[string]any)["error"])

	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	hub.EmitChange(storages[0].ID, "reports/2026/q3", realtime.ChangeEvent{Action: "upload", Name: "a.pdf"})

	f := wsReadType(t, conn, "tree_change")
	require.Equal(t, "main://reports", f["root"])
	require.Equal(t, []string{"2026/q3"}, anyStrings(f["dirs"]))
	require.Equal(t, "a.pdf", f["name"])
}

// ⚠ An empty watch list is the engine's "stop watching" (every pair paused)
// and must still be acknowledged, or the client reads it as an old server.
func TestWSWatchEmptySetIsAcknowledged(t *testing.T) {
	url, _, _ := newWSFixture(t, &model.User{ID: 1})
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")
	ack := wsWatch(t, conn)
	require.Empty(t, anyStrings(ack["roots"]))
}

// A watcher must not show up in the presence bar of the folders it mirrors.
func TestWSWatchIsNotPresence(t *testing.T) {
	url, hub, store := newWSFixture(t, &model.User{ID: 1, DisplayName: "Burak"})
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")
	wsWatch(t, conn, "main://reports")

	storages, _ := store.ListEnabledStorages(context.Background())
	require.Empty(t, hub.Presence(storages[0].ID, "reports"), "a watcher is nobody's co-viewer")
}

// The confinement a ticket carries binds the watch exactly as it binds a room.
func TestWSWatchConfinedTicket(t *testing.T) {
	url, hub, store, tickets := newWSTicketFixture(t)
	tok, err := tickets.Mint(realtime.Ticket{
		UserID: 1, Name: "Embedded", ConfineAdapter: "main", ConfineRel: "projeler/5",
	}, time.Minute)
	require.NoError(t, err)
	conn, _, err := websocket.Dial(context.Background(), url+"?ticket="+tok, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	ack := wsWatch(t, conn, "main://", "main://../secret")
	require.Equal(t, []string{"main://"}, anyStrings(ack["roots"]))
	require.Equal(t, "forbidden", watchRefusals(ack)["main://../secret"])

	// The relative root resolved to the ABSOLUTE confine root, and dirs come
	// back relative to what the client asked for.
	storages, _ := store.ListEnabledStorages(context.Background())
	hub.EmitChange(storages[0].ID, "projeler/5/sub", realtime.ChangeEvent{Action: "upload", Name: "x"})
	f := wsReadType(t, conn, "tree_change")
	require.Equal(t, "main://", f["root"])
	require.Equal(t, []string{"sub"}, anyStrings(f["dirs"]))

	hub.EmitChange(storages[0].ID, "projeler/6", realtime.ChangeEvent{Action: "upload", Name: "leak"})
	readCtx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, data, rerr := conn.Read(readCtx)
	require.Error(t, rerr, "nothing outside the confine may arrive, got %s", data)
}

// The tenant boundary: a ticketed alpha client cannot watch bravo's storage.
// Twin of TestWSTicket_CannotSubscribeToAnotherTenantsFolder.
func TestWSWatchTicket_CannotWatchAnotherTenantsFolder(t *testing.T) {
	f := newMTFix(t, true)
	url := strings.Replace(f.URL, "http://", "ws://", 1) + "/api/ws?ticket=" + wsTicket(t, f, f.A)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: &http.Client{}})
	require.NoError(t, err)
	defer conn.CloseNow()

	ack := wsWatch(t, conn, "bravo://", "alpha://")
	require.Equal(t, []string{"alpha://"}, anyStrings(ack["roots"]), "%v", ack)
	require.Equal(t, "not_found", watchRefusals(ack)["bravo://"],
		"the refusal must look like a folder that is not there: %v", ack)
}

// watchRefusals maps each refused root of a `watching` ack to its reason.
func watchRefusals(ack map[string]any) map[string]string {
	out := map[string]string{}
	errs, _ := ack["errors"].([]any)
	for _, e := range errs {
		m := e.(map[string]any)
		out[m["path"].(string)] = m["error"].(string)
	}
	return out
}

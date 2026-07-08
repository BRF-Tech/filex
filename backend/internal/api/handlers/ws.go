package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
)

// WS is the live-collaboration WebSocket handler. It upgrades GET /api/ws to a
// WebSocket, then relays the connected user's intents (subscribe to a folder,
// focus a file, ping) into the realtime Hub and streams the Hub's change +
// presence frames back to the browser.
//
// The route MUST be mounted in the AUTHENTICATED group so auth.UserFrom(ctx)
// resolves the caller (the browser's session cookie authenticates the upgrade;
// it is same-origin, so no bearer token is involved for the native panel).
type WS struct {
	Store db.Store
	ACL   *acl.Resolver
	Hub   *realtime.Hub
}

// NewWS constructs the WebSocket handler. A nil hub makes Handle reply 503 so
// the route can be registered unconditionally.
func NewWS(store db.Store, resolver *acl.Resolver, hub *realtime.Hub) *WS {
	return &WS{Store: store, ACL: resolver, Hub: hub}
}

// wsClientMsg is the client → server wire message. `file` is a pointer so
// `{"type":"focus","file":null}` (clear focus) is distinguishable from absent.
type wsClientMsg struct {
	Type string  `json:"type"`           // subscribe | focus | ping
	Path string  `json:"path,omitempty"` // "<adapter>://<dir>" for subscribe
	File *string `json:"file,omitempty"` // file name for focus (null clears)
}

var wsPongFrame = []byte(`{"type":"pong"}`)

// Handle upgrades the request and runs the per-connection read loop + write
// pump until the socket closes.
func (h *WS) Handle(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.Hub == nil {
		http.Error(w, "realtime unavailable", http.StatusServiceUnavailable)
		return
	}

	// Default origin verification (the request host is always authorized) is a
	// CSRF guard for the cookie-authenticated upgrade — keep it. Behind a
	// reverse proxy the Host header must be preserved for this to pass (see the
	// deploy note in the handover).
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		// Accept already wrote the HTTP error (e.g. 403 origin / 501 no hijacker).
		slog.Debug("ws accept failed", slog.String("err", err.Error()))
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 * 1024)

	// connCtx drives the connection lifecycle (read loop + write pump); baseCtx
	// carries the auth/tenant values off the request WITHOUT its cancellation,
	// so per-message DB/ACL lookups stay valid for the life of the socket.
	connCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseCtx := context.WithoutCancel(r.Context())

	client := realtime.NewClient(user.ID, wsDisplayName(user), 32)
	defer h.Hub.Unsubscribe(client)

	go h.writePump(connCtx, cancel, conn, client)

	for {
		typ, data, err := conn.Read(connCtx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		h.handleClientMessage(baseCtx, client, data)
	}
	cancel()
}

// writePump drains the client's outbound queue to the socket. It exits (and
// cancels the connection) on the first write error or when connCtx is done, so
// the read loop unblocks too.
func (h *WS) writePump(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, client *realtime.Client) {
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-client.Send:
			if !ok {
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, frame)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}

// handleClientMessage parses and dispatches one client frame.
func (h *WS) handleClientMessage(ctx context.Context, client *realtime.Client, data []byte) {
	var msg wsClientMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	switch msg.Type {
	case "subscribe":
		h.handleSubscribe(ctx, client, msg.Path)
	case "focus":
		file := ""
		if msg.File != nil {
			file = strings.TrimSpace(*msg.File)
		}
		h.Hub.SetFocus(client, file)
	case "ping":
		select {
		case client.Send <- wsPongFrame:
		default:
		}
	}
}

// handleSubscribe resolves the requested path to a (storage, dir), RBAC-checks
// the user may READ it, and joins the corresponding room. On denial it sends an
// error frame and leaves the connection open so the client can try elsewhere.
func (h *WS) handleSubscribe(ctx context.Context, client *realtime.Client, rawPath string) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	storageID, storageName, rel, cleanDir, ok := h.resolveSubscribe(cctx, rawPath)
	if !ok {
		h.sendError(client, rawPath, "not_found")
		return
	}
	// RBAC: viewing a folder's live feed requires ≥viewer on it. A nil resolver
	// (ACL unwired, e.g. tests) allows. This is the security boundary — a user
	// can only subscribe to folders they may read.
	if !aclAllowName(cctx, h.ACL, h.Store, storageName, rel, acl.LevelViewer) {
		h.sendError(client, rawPath, "forbidden")
		return
	}
	displayPath := storageName + "://" + strings.TrimPrefix(cleanDir, "/")
	h.Hub.Subscribe(client, storageID, cleanDir, displayPath)
}

// resolveSubscribe maps "<adapter>://<dir>" (or a bare dir against the first
// storage) to (storageID, storageName, trimmedRel, cleanDir). It does NOT
// require the folder to be indexed — an empty/uncached folder still has a room.
func (h *WS) resolveSubscribe(ctx context.Context, rawPath string) (storageID int64, storageName, rel, cleanDir string, ok bool) {
	adapter, rel := splitAdapterPath(rawPath)
	storages, err := h.Store.ListEnabledStorages(ctx)
	if err != nil || len(storages) == 0 {
		return 0, "", "", "", false
	}
	if adapter == "" {
		adapter = storages[0].Name
	}
	var st *model.Storage
	for _, s := range storages {
		if s.Name == adapter {
			st = s
			break
		}
	}
	if st == nil {
		return 0, "", "", "", false
	}
	if strings.Contains(rel, "..") {
		return 0, "", "", "", false
	}
	cleanDir = normalizeDBPath(rel)
	return st.ID, st.Name, rel, cleanDir, true
}

// sendError enqueues a non-fatal error frame for the client.
func (h *WS) sendError(client *realtime.Client, path, reason string) {
	frame, err := json.Marshal(map[string]string{"type": "error", "path": path, "error": reason})
	if err != nil {
		return
	}
	select {
	case client.Send <- frame:
	default:
	}
}

// wsDisplayName picks the friendliest label for presence: display name, else
// the email local-part, else a generic fallback.
func wsDisplayName(u *model.User) string {
	if u == nil {
		return "user"
	}
	if n := strings.TrimSpace(u.DisplayName); n != "" {
		return n
	}
	if u.Email != "" {
		if i := strings.IndexByte(u.Email, '@'); i > 0 {
			return u.Email[:i]
		}
		return u.Email
	}
	return "user"
}

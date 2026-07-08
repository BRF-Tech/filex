package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
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
	Store     db.Store
	ACL       *acl.Resolver
	Hub       *realtime.Hub
	Tickets   *realtime.TicketStore // ticket auth for embedded/cross-origin clients
	PublicURL string                // used to advertise the wss:// URL in ticket responses
}

// NewWS constructs the WebSocket handler. A nil hub makes Handle reply 503 so
// the route can be registered unconditionally.
func NewWS(store db.Store, resolver *acl.Resolver, hub *realtime.Hub, tickets *realtime.TicketStore, publicURL string) *WS {
	return &WS{Store: store, ACL: resolver, Hub: hub, Tickets: tickets, PublicURL: publicURL}
}

// wsTicketTTL is how long a minted ticket stays valid — long enough for the
// browser to open the socket, short enough to be near-useless if leaked.
const wsTicketTTL = 60 * time.Second

// Ticket mints a short-lived, single-use WebSocket auth ticket for the caller,
// bound to their identity + confinement. Embedded consumers fetch this through
// the host's HTTP proxy (which injects the real token) and then open
// `wss://.../api/ws?ticket=<t>` directly — the durable token never reaches the
// browser. Same-origin callers (the native panel) can use it too.
//
//	POST /api/files/ws-ticket  →  {"ticket": "...", "ws_url": "wss://host/api/ws"}
func (h *WS) Ticket(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if h.Hub == nil || h.Tickets == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "realtime unavailable"})
		return
	}
	root, hasRoot, err := confine.FromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	t := realtime.Ticket{UserID: user.ID, Name: wsDisplayName(user)}
	if hasRoot {
		t.ConfineAdapter = root.Adapter
		t.ConfineRel = root.Rel
	}
	tok, err := h.Tickets.Mint(t, wsTicketTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mint failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ticket": tok, "ws_url": h.wsURL()})
}

// wsURL derives the public wss:// URL for /api/ws from the configured PublicURL.
func (h *WS) wsURL() string {
	base := strings.TrimRight(h.PublicURL, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + "/api/ws"
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + "/api/ws"
	default:
		return base + "/api/ws"
	}
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
	if h.Hub == nil {
		http.Error(w, "realtime unavailable", http.StatusServiceUnavailable)
		return
	}

	// Identity comes from EITHER a single-use ticket (embedded/cross-origin
	// clients that can't send a cookie or Authorization header) OR the session
	// cookie (native same-origin panel).
	var (
		userID   int64
		name     string
		ticketed bool
		ticket   realtime.Ticket
	)
	if tok := r.URL.Query().Get("ticket"); tok != "" && h.Tickets != nil {
		t, ok := h.Tickets.Consume(tok)
		if !ok {
			http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
			return
		}
		ticketed, ticket = true, t
		userID, name = t.UserID, t.Name
	} else {
		user := auth.UserFrom(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		userID, name = user.ID, wsDisplayName(user)
	}

	// Ticketed connections are cross-origin by design (embedded webcomponent →
	// fm.brf.sh) and already authenticated by the one-shot ticket, so skip the
	// same-origin (CSRF) check. Cookie connections keep it — behind a reverse
	// proxy the Host header must be preserved for that to pass.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: ticketed})
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

	// A ticketed connection carries no session, so RBAC on subscribe (which
	// reads auth.UserFrom) would see no user and forbid everything. Load the
	// ticket's user into the context so subscribes are authorized as that user.
	if ticketed {
		if u, err := h.Store.GetUser(baseCtx, ticket.UserID); err == nil && u != nil {
			baseCtx = auth.WithUser(baseCtx, u)
		}
	}

	client := realtime.NewClient(userID, name, 32)
	if ticketed && ticket.ConfineAdapter != "" {
		client.Confined = true
		client.ConfineAdapter = ticket.ConfineAdapter
		client.ConfineRel = ticket.ConfineRel
	}
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

	// Embedded (ticket-confined) explorers are confine-unaware: the host proxy
	// injects X-Filex-Root, so the explorer treats the confined root as "/" and
	// subscribes with paths RELATIVE to it (e.g. "s3-test://" for its own root).
	// Translate that to the storage-absolute path the hub keys rooms by — and the
	// mutation handlers emit — so the room, the confine check and RBAC all resolve
	// the REAL folder. Frames still echo the client's own path (Hub stamps c.path)
	// so the browser's path-matching keeps lining up against what it sent.
	resolvePath := rawPath
	if client.Confined && client.ConfineRel != "" {
		_, rel := splitAdapterPath(rawPath)
		resolvePath = client.ConfineAdapter + "://" + path.Join(client.ConfineRel, rel)
	}

	storageID, storageName, rel, cleanDir, ok := h.resolveSubscribe(cctx, resolvePath)
	if !ok {
		h.sendError(client, rawPath, "not_found")
		return
	}
	// Ticket confinement: a confined (embedded) client may only watch rooms
	// within its ticket's root — a hard boundary on top of RBAC.
	if !client.AllowsPath(storageName, strings.Trim(cleanDir, "/")) {
		h.sendError(client, rawPath, "forbidden")
		return
	}
	// RBAC: viewing a folder's live feed requires ≥viewer on it. A nil resolver
	// (ACL unwired, e.g. tests) allows. This is the security boundary — a user
	// can only subscribe to folders they may read.
	if !aclAllowName(cctx, h.ACL, h.Store, storageName, rel, acl.LevelViewer) {
		h.sendError(client, rawPath, "forbidden")
		return
	}
	// Echo the client's OWN path (rawPath), not the absolute one, so its frame
	// path-matching lines up; the room itself is keyed by the absolute cleanDir.
	h.Hub.Subscribe(client, storageID, cleanDir, rawPath)
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

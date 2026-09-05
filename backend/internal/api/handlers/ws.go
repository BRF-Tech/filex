package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
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
	// Tenants resolves which origin the advertised wss:// URL is built on.
	// In multi-tenant mode a tenant's browser must be told to open ITS OWN
	// host — the operator's would fail the origin check and, on a separate
	// certificate, not connect at all.
	Tenants tenanturl.Resolver
}

// AttachTenants wires the shared per-request origin resolver (internal/tenanturl).
func (h *WS) AttachTenants(rv tenanturl.Resolver) { h.Tenants = rv }

// NewWS constructs the WebSocket handler. A nil hub makes Handle reply 503 so
// the route can be registered unconditionally.
func NewWS(store db.Store, resolver *acl.Resolver, hub *realtime.Hub, tickets *realtime.TicketStore, publicURL string) *WS {
	return &WS{
		Store: store, ACL: resolver, Hub: hub, Tickets: tickets, PublicURL: publicURL,
		Tenants: tenanturl.New(store, publicURL, false),
	}
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
	// Presence identity: a native session shows the user's own name. An API
	// token (embedded host proxies, MCP) shows its resolved token USERNAME
	// (X-Filex-Token-User → the token's allow-list, default = first entry /
	// label) — every end user behind a shared proxy token maps to the same
	// filex account, so the account name ("admin") would be misleading. A
	// trusted host proxy can additionally stamp the REAL end user via
	// X-Filex-Presence-Name/-Key; the display then combines both:
	// "Ada (work)". (Honored only on token auth; proxies strip these from
	// client requests, so end users can't spoof them.)
	name := wsDisplayName(user)
	// ⭐ The picture follows the ACCOUNT, not the client: a session, the
	// desktop app and every API key minted under the same user all resolve to
	// the same avatar, so setting it once in the profile puts the same face on
	// every filex surface that account opens.
	avatar := user.AvatarURL
	presenceKey := ""
	if tok := auth.TokenFrom(r.Context()); tok != nil {
		uname := auth.TokenUserFrom(r.Context())
		if uname == "" {
			uname = tok.DefaultUsername()
		}
		// The parenthesised half of a presence entry: WHICH consumer of the token
		// this connection is.
		qualifier := uname
		// ⚠ A token with NO username allow-list is not a shared proxy — it is one
		// person's own client (the desktop app, a CLI, a personal script), and
		// that person is the account owner, so their real name is neither
		// misleading nor unavailable. DefaultUsername() falls back to the token's
		// LABEL for such tokens, so replacing the name with it put
		// "filex desktop — Win32" in the presence bar of the owner's OWN folder.
		// Their name leads and the client goes in parentheses instead —
		// "Ada (filex desktop)", the same shape the proxy branch produces.
		if len(tok.UsernameList()) == 0 {
			qualifier = wsClientLabel(tok.Label)
			if qualifier != "" {
				name += " (" + qualifier + ")"
			}
		} else if uname != "" {
			// A shared proxy token is not a person: the entry says "work", so
			// the account owner's face would be a lie on somebody else's row.
			name = uname
			avatar = ""
		}
		if v := sanitizePresenceName(r.Header.Get("X-Filex-Presence-Name")); v != "" {
			if qualifier != "" {
				name = v + " (" + qualifier + ")"
			} else {
				name = v
			}
			// ⚠ A trusted proxy just told us this connection is a DIFFERENT
			// human from the account holder. Their own picture is the only one
			// that may be drawn here — the header below, when the host sends
			// one — never the token owner's.
			avatar = sanitizePresenceAvatar(r.Header.Get("X-Filex-Presence-Avatar"))
		}
		presenceKey = sanitizePresenceKey(r.Header.Get("X-Filex-Presence-Key"))
		if presenceKey == "" {
			// Default token connections to their own (token, username) identity:
			// without this a keyless token viewer collides with the token
			// OWNER's cookie session (same user id) — self-exclusion would hide
			// it and the merged entry's name would be nondeterministic. The
			// username is part of the key so "work" and "fishapp" viewers of one
			// token stay distinct people.
			presenceKey = "tok-" + strconv.FormatInt(tok.ID, 10)
			if uname != "" {
				presenceKey += "-" + uname
			}
		}
	}
	t := realtime.Ticket{UserID: user.ID, Name: name, Avatar: avatar, PresenceKey: presenceKey}
	if hasRoot {
		t.ConfineAdapter = root.Adapter
		t.ConfineRel = root.Rel
	}
	tok, err := h.Tickets.Mint(t, wsTicketTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mint failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ticket": tok, "ws_url": h.wsURL(r)})
}

// wsURL derives the public wss:// URL for /api/ws from the origin this request
// belongs to (the tenant's host in multi-tenant mode, PublicURL otherwise).
func (h *WS) wsURL(r *http.Request) string {
	base := h.Tenants.FromRequest(r)
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
		avatar   string
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
		userID, name, avatar = t.UserID, t.Name, t.Avatar
	} else {
		user := auth.UserFrom(r.Context())
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		userID, name, avatar = user.ID, wsDisplayName(user), user.AvatarURL
	}

	// Ticketed connections are cross-origin by design (embedded webcomponent →
	// fm.example.com) and already authenticated by the one-shot ticket, so skip the
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
	client.Avatar = avatar
	if ticketed {
		client.PresenceKey = ticket.PresenceKey
		if ticket.ConfineAdapter != "" {
			client.Confined = true
			client.ConfineAdapter = ticket.ConfineAdapter
			client.ConfineRel = ticket.ConfineRel
		}
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

	// Confined (embedded) clients may spell a folder two ways. The webcomponent
	// itself is confine-AWARE — the backend returns storage-absolute dirnames
	// under X-Filex-Root, so it subscribes with the ABSOLUTE path
	// ("s3-test://projeler/5"): use it as-is so the room matches what mutation
	// handlers emit. Hand-rolled integrators may instead send a confine-RELATIVE
	// path ("s3-test://" for their root): only a path that does NOT already fall
	// inside the confine gets ConfineRel prepended. (v0.1.78 prepended
	// unconditionally, which shoved absolute subscribes into a doubled
	// "projeler/5/projeler/5" room — presence looked fine between embedded
	// viewers, but real changes never arrived and native viewers were invisible.)
	// Frames still echo the client's own path (Hub stamps c.path).
	resolvePath := rawPath
	if client.Confined && client.ConfineRel != "" {
		if pathHasDotDot(rawPath) {
			h.sendError(client, rawPath, "forbidden")
			return
		}
		_, rel := splitAdapterPath(rawPath)
		rel = strings.Trim(path.Clean("/"+rel), "/")
		if rel != client.ConfineRel && !strings.HasPrefix(rel, client.ConfineRel+"/") {
			resolvePath = client.ConfineAdapter + "://" + path.Join(client.ConfineRel, rel)
		}
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
	if pathHasDotDot(rel) {
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

// sanitizePresenceName cleans a proxy-supplied presence display name: control
// characters stripped, whitespace collapsed, capped at 48 runes. HTTP header
// values are latin-1 territory, so proxies send non-ASCII names (Ayşe, Zoë)
// RFC 2047-encoded (`=?utf-8?B?...?=`) — decode that first; plain values pass
// through untouched.
func sanitizePresenceName(v string) string {
	if dec, err := new(mime.WordDecoder).DecodeHeader(v); err == nil {
		v = dec
	}
	v = strings.Join(strings.Fields(v), " ")
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if runes := []rune(v); len(runes) > 48 {
		v = string(runes[:48])
	}
	return strings.TrimSpace(v)
}

// sanitizePresenceAvatar cleans a proxy-supplied presence picture for an end
// user the host re-identified. Same three shapes the profile accepts, same
// cap — a header is the least trustworthy input we have, and the value is
// broadcast to everyone in the room.
func sanitizePresenceAvatar(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || validateImageRef("presence avatar", v, avatarMaxBytes) != nil {
		return ""
	}
	return v
}

// sanitizePresenceKey restricts a proxy-supplied presence key to a safe
// identifier alphabet, capped at 64 bytes. Anything else is dropped entirely
// (a malformed key must not silently merge distinct users).
func sanitizePresenceKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 64 {
		return ""
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return ""
		}
	}
	return v
}

// wsClientLabel shortens an API token's label down to the client NAME, for the
// parenthesised half of a presence entry.
//
// Labels are minted for the server's token screen, where detail is the point —
// the desktop app asks for "filex desktop — Win32" so its owner can tell two
// registrations apart. Next to a folder that detail is noise, so everything
// from the first dash separator on is dropped. Capped like every other presence
// string, because the label is user-supplied.
func wsClientLabel(label string) string {
	s := strings.Join(strings.Fields(label), " ")
	for _, sep := range []string{" — ", " – ", " - ", "—", "–"} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
			break
		}
	}
	s = strings.TrimSpace(s)
	if runes := []rune(s); len(runes) > 32 {
		s = strings.TrimSpace(string(runes[:32]))
	}
	return s
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

// Package realtime implements the WebSocket hub behind filex's live
// collaboration surface: per-folder "rooms" that broadcast (1) file-change
// events so every viewer's listing updates without a manual refresh and
// (2) presence so viewers can see who else is in the same folder and which
// file each is focused on.
//
// The package is transport-agnostic: it knows nothing about net/http or
// coder/websocket. The HTTP handler (internal/api/handlers/ws.go) owns the
// socket lifecycle and feeds parsed client intents (subscribe/focus) into the
// Hub, while the Hub pushes JSON frames back out through each Client's Send
// channel. That split keeps the Hub unit-testable with fake clients and no
// network — see hub_test.go.
package realtime

import (
	"strings"
	"sync/atomic"
)

// clientSeq hands out process-unique client ids so two connections from the
// same user (e.g. two browser tabs) are still distinguishable inside a room.
var clientSeq atomic.Int64

// Client is one live WebSocket connection's presence in the hub. It carries
// the identity used for presence plus a buffered Send channel the hub writes
// outbound JSON frames to; the handler's write pump drains it to the socket.
//
// The room/file/path fields are mutated only under Hub.mu, so a Client must
// not be shared across hubs. Send is created buffered — the hub uses a
// non-blocking send so one slow/stalled reader can never wedge a broadcast
// (a dropped frame degrades to a missed live update, which the next event or
// a manual refresh corrects).
type Client struct {
	ID     int64  // process-unique connection id
	UserID int64  // authenticated user id (0 = anonymous, unused today)
	Name   string // display name shown in presence
	Send   chan []byte

	// Confinement carried by a ticket-authenticated connection (embedded
	// contexts like work.brf.sh proxy a root-confined token). When Confined is
	// true the client may only subscribe to rooms within (ConfineAdapter,
	// ConfineRel). Cookie-authenticated connections leave Confined=false and
	// rely on RBAC alone. Set once before any hub interaction.
	Confined       bool
	ConfineAdapter string
	ConfineRel     string

	// Guarded by Hub.mu.
	room string // current room key ("" = not subscribed)
	path string // display path the client subscribed to ("<adapter>://<dir>")
	file string // currently focused file name ("" = none)
}

// AllowsPath reports whether this client may subscribe to (adapter, rel). An
// unconfined client may go anywhere (RBAC still applies); a confined client is
// restricted to its ticket's root and everything under it.
func (c *Client) AllowsPath(adapter, rel string) bool {
	if !c.Confined {
		return true
	}
	if adapter != c.ConfineAdapter {
		return false
	}
	if c.ConfineRel == "" {
		return true
	}
	return rel == c.ConfineRel || strings.HasPrefix(rel, c.ConfineRel+"/")
}

// NewClient builds a Client with a unique id and a buffered send channel.
// buffer sizes the outbound queue; a small value (e.g. 16) is plenty for
// presence + change frames.
func NewClient(userID int64, name string, buffer int) *Client {
	if buffer <= 0 {
		buffer = 16
	}
	return &Client{
		ID:     clientSeq.Add(1),
		UserID: userID,
		Name:   name,
		Send:   make(chan []byte, buffer),
	}
}

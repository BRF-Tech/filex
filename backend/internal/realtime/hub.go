package realtime

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
)

// ChangeEvent describes a mutation that happened inside a folder. It is the
// value the mutation handlers hand to Hub.EmitChange; the hub wraps it in a
// {type:"change", …} frame addressed to the affected room. Frontends treat it
// as a signal to re-fetch the listing (Action/Name are advisory, for future
// incremental patching / toasts).
type ChangeEvent struct {
	Action  string `json:"action"`             // create|delete|rename|move|upload|modify
	Name    string `json:"name,omitempty"`     // affected item's basename
	NewName string `json:"new_name,omitempty"` // rename target basename
}

// PresenceUser is one person currently in a room and the file they're focused
// on (empty = just browsing the folder). Presence is de-duplicated per user id,
// so two tabs from the same person collapse to a single entry.
type PresenceUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	File string `json:"file,omitempty"`
}

// wireChange / wirePresence are the JSON envelopes pushed to clients.
type wireChange struct {
	Type    string `json:"type"` // always "change"
	Path    string `json:"path"`
	Action  string `json:"action"`
	Name    string `json:"name,omitempty"`
	NewName string `json:"new_name,omitempty"`
}

type wirePresence struct {
	Type  string         `json:"type"` // always "presence"
	Path  string         `json:"path"`
	Users []PresenceUser `json:"users"`
}

// room is the set of clients viewing one folder. Frames echo each client's OWN
// subscribed display path (c.path), not a room-shared one: an embedded explorer
// subscribes with a confine-RELATIVE path while a native panel uses the absolute
// path for the same room, so a single shared path would fail one side's
// client-side path-matching.
type room struct {
	clients map[*Client]struct{}
}

// Hub is the process-wide registry of rooms and their subscribers. All state
// is guarded by a single mutex; broadcasts build the frame under the lock and
// enqueue it with a non-blocking send, so the critical section is short and a
// stalled client never blocks others.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*room
}

// NewHub returns an empty, ready-to-use Hub.
func NewHub() *Hub {
	return &Hub{rooms: make(map[string]*room)}
}

// RoomKey is the canonical room identity for a (storage, dir) pair. dir is
// normalized to the node-table form (leading slash, no trailing slash, "" for
// the storage root) so that a subscriber's path and a mutation handler's
// relative dir resolve to the same room regardless of how each was spelled.
func RoomKey(storageID int64, dir string) string {
	return fmt.Sprintf("%d:%s", storageID, normalizeDir(dir))
}

// normalizeDir mirrors handlers.normalizeDBPath without importing that package
// (which would create an import cycle). "" / "/" / "." → "", "foo/bar" and
// "/foo/bar/" → "/foo/bar".
func normalizeDir(dir string) string {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return ""
	}
	return strings.TrimRight(path.Clean("/"+dir), "/")
}

// Subscribe moves c into the room for (storageID, dir), leaving whatever room
// it was in before. displayPath is the "<adapter>://<dir>" the client asked
// for; it's echoed in outbound frames. Presence is re-broadcast to both the
// old and new rooms so everyone (including c) gets the fresh roster.
func (h *Hub) Subscribe(c *Client, storageID int64, dir, displayPath string) {
	key := RoomKey(storageID, dir)

	h.mu.Lock()
	oldKey := c.room
	if oldKey == key {
		// Re-subscribe to the same folder: just refresh presence.
		c.file = ""
		h.broadcastPresenceLocked(key)
		h.mu.Unlock()
		return
	}
	if oldKey != "" {
		h.removeLocked(c, oldKey)
	}
	rm := h.rooms[key]
	if rm == nil {
		rm = &room{clients: make(map[*Client]struct{})}
		h.rooms[key] = rm
	}
	rm.clients[c] = struct{}{}
	c.room = key
	c.path = displayPath
	c.file = ""

	h.broadcastPresenceLocked(key)
	if oldKey != "" {
		h.broadcastPresenceLocked(oldKey)
	}
	h.mu.Unlock()
}

// Unsubscribe removes c from its room entirely (called on disconnect) and
// refreshes presence for the room it left.
func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	oldKey := c.room
	if oldKey != "" {
		h.removeLocked(c, oldKey)
		c.room = ""
		h.broadcastPresenceLocked(oldKey)
	}
	h.mu.Unlock()
}

// SetFocus updates the file c is focused on within its current room and
// re-broadcasts presence. file "" clears the focus (e.g. preview closed).
func (h *Hub) SetFocus(c *Client, file string) {
	h.mu.Lock()
	if c.room != "" {
		c.file = file
		h.broadcastPresenceLocked(c.room)
	}
	h.mu.Unlock()
}

// EmitChange fans a change frame out to every client viewing (storageID, dir).
// Safe to call for a room with no subscribers (no-op). This is the method the
// mutation handlers reach through the handlers.ChangeEmitter interface.
func (h *Hub) EmitChange(storageID int64, dir string, ev ChangeEvent) {
	key := RoomKey(storageID, dir)
	h.mu.Lock()
	rm := h.rooms[key]
	if rm == nil || len(rm.clients) == 0 {
		h.mu.Unlock()
		return
	}
	// Stamp each client's own subscribed path so confined (relative) and native
	// (absolute) viewers of the same room each get a frame they recognize.
	for c := range rm.clients {
		frame, err := json.Marshal(wireChange{
			Type:    "change",
			Path:    c.path,
			Action:  ev.Action,
			Name:    ev.Name,
			NewName: ev.NewName,
		})
		if err != nil {
			continue
		}
		trySend(c, frame)
	}
	h.mu.Unlock()
}

// Presence returns the current roster for (storageID, dir). Exposed for tests
// and diagnostics; the live path broadcasts instead.
func (h *Hub) Presence(storageID int64, dir string) []PresenceUser {
	key := RoomKey(storageID, dir)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotLocked(key)
}

// removeLocked drops c from room key and deletes the room when it empties.
// Caller holds h.mu.
func (h *Hub) removeLocked(c *Client, key string) {
	rm := h.rooms[key]
	if rm == nil {
		return
	}
	delete(rm.clients, c)
	if len(rm.clients) == 0 {
		delete(h.rooms, key)
	}
}

// snapshotLocked builds the de-duplicated (by user id) presence roster for a
// room, sorted by name then id for stable output. Caller holds h.mu.
func (h *Hub) snapshotLocked(key string) []PresenceUser {
	rm := h.rooms[key]
	if rm == nil {
		return nil
	}
	byUser := make(map[int64]PresenceUser, len(rm.clients))
	for c := range rm.clients {
		existing, ok := byUser[c.UserID]
		if !ok {
			byUser[c.UserID] = PresenceUser{ID: c.UserID, Name: c.Name, File: c.file}
			continue
		}
		// Prefer an entry that has a focused file so a person reading a
		// document in one tab still shows that file.
		if existing.File == "" && c.file != "" {
			existing.File = c.file
			byUser[c.UserID] = existing
		}
	}
	out := make([]PresenceUser, 0, len(byUser))
	for _, u := range byUser {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// broadcastPresenceLocked pushes the current roster to every client in a room.
// Caller holds h.mu.
func (h *Hub) broadcastPresenceLocked(key string) {
	rm := h.rooms[key]
	if rm == nil {
		return
	}
	users := h.snapshotLocked(key)
	// Per-client path: each viewer gets the roster stamped with the path IT
	// subscribed to (relative for embedded, absolute for native) so its
	// client-side path-matching accepts the frame.
	for c := range rm.clients {
		frame, err := json.Marshal(wirePresence{
			Type:  "presence",
			Path:  c.path,
			Users: users,
		})
		if err != nil {
			continue
		}
		trySend(c, frame)
	}
}

// trySend enqueues frame on c.Send without blocking; a full buffer drops the
// frame (see Client.Send rationale).
func trySend(c *Client, frame []byte) {
	select {
	case c.Send <- frame:
	default:
	}
}

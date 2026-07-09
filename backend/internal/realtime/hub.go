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
// on (empty = just browsing the folder). Presence is de-duplicated per
// identity (user id + optional per-end-user key), so two tabs from the same
// person collapse to a single entry while two end users behind one shared
// proxy token stay distinct.
type PresenceUser struct {
	ID   int64  `json:"id"`
	UID  string `json:"uid"` // stable identity key (Client.Identity) for client-side keying/colours
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
	// A rename/delete invalidates any presence focus pointing at the old name —
	// the focuser's client has no reason to re-send it, so fix it server-side
	// and re-broadcast the roster below.
	presenceDirty := false
	for c := range rm.clients {
		if c.file == "" || c.file != ev.Name {
			continue
		}
		switch ev.Action {
		case "rename", "move":
			if ev.NewName != "" && ev.NewName != ev.Name {
				c.file = ev.NewName
				presenceDirty = true
			}
		case "delete":
			c.file = ""
			presenceDirty = true
		}
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
	if presenceDirty {
		h.broadcastPresenceLocked(key)
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

// snapshotLocked builds the de-duplicated (by identity) presence roster for a
// room, sorted by name then identity for stable output. Caller holds h.mu.
func (h *Hub) snapshotLocked(key string) []PresenceUser {
	rm := h.rooms[key]
	if rm == nil {
		return nil
	}
	byIdent := make(map[string]PresenceUser, len(rm.clients))
	for c := range rm.clients {
		ident := c.Identity()
		existing, ok := byIdent[ident]
		if !ok {
			byIdent[ident] = PresenceUser{ID: c.UserID, UID: ident, Name: c.Name, File: c.file}
			continue
		}
		// Prefer an entry that has a focused file so a person reading a
		// document in one tab still shows that file.
		if existing.File == "" && c.file != "" {
			existing.File = c.file
			byIdent[ident] = existing
		}
	}
	out := make([]PresenceUser, 0, len(byIdent))
	for _, u := range byIdent {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UID < out[j].UID
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
	// Per-client frame: the roster excludes the RECIPIENT's own identity
	// (presence answers "who ELSE is here" — seeing yourself is noise) and is
	// stamped with the path the recipient subscribed to (relative for embedded,
	// absolute for native) so its client-side path-matching accepts the frame.
	for c := range rm.clients {
		ident := c.Identity()
		others := make([]PresenceUser, 0, len(users))
		for _, u := range users {
			if u.UID != ident {
				others = append(others, u)
			}
		}
		frame, err := json.Marshal(wirePresence{
			Type:  "presence",
			Path:  c.path,
			Users: others,
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

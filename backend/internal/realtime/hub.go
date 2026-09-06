package realtime

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
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
	// Avatar is the profile picture to draw instead of initials — omitted when
	// the person has none, which is the fallback the strip has always used.
	Avatar string `json:"avatar,omitempty"`
}

// wireChange / wirePresence are the JSON envelopes pushed to clients.
type wireChange struct {
	Type    string `json:"type"` // always "change"
	Path    string `json:"path"`
	Action  string `json:"action"`
	Name    string `json:"name,omitempty"`
	NewName string `json:"new_name,omitempty"`
	// Count is how many change events this frame stands for. Absent (0) means
	// one, which is every frame a client saw before coalescing existed, so an
	// older reader is unaffected. >1 says "this folder changed n times, here is
	// the end of it" — and when those n changes were not all the same thing,
	// Name/NewName are omitted rather than naming one of them.
	Count int `json:"count,omitempty"`
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

	// ── change coalescing ──────────────────────────────────────────────
	//
	// One folder can be changed far more often than a listing is worth
	// re-fetching. Two shapes were measured on 2026-09-06:
	//
	//   - one NFS write = 3-6 frames. NFSv3 has no "close", so go-nfs opens,
	//     writes and CLOSES the handle for EVERY write RPC (nfs_onwrite.go),
	//     and filex commits + announces on each close. `cp -p` of a 5 MB file
	//     over a real mount: 1 CREATE + 5 × 1 MiB WRITE = 6 frames.
	//   - extracting a 5 000-file zip = 5 000 frames, one per member.
	//
	// The room therefore emits on the LEADING edge — the first change in a
	// quiet room goes out with nothing added to it, because the latency of an
	// ordinary single write is the number this whole layer exists to protect —
	// and merges everything that follows into one trailing frame per `window`.
	// `window` starts at coalesceMin and doubles per flush up to coalesceMax,
	// so a short burst settles almost at once while a long one costs a bounded
	// trickle instead of thousands of frames. It resets the moment a change
	// arrives into a room that has gone quiet.
	//
	// ⚠ pending is never dropped, only merged: the flush that ends a burst
	// carries the burst's last event, so an explorer's final re-list sees the
	// final state. A burst that ended with a silently discarded frame would
	// leave that folder stale until the person navigated away and back.
	lastSent time.Time
	window   time.Duration
	pending  *pendingChange
	timer    *time.Timer
}

// pendingChange is the merge of every change a room has seen since its last
// frame. It keeps the LAST event (the one that describes where the folder
// ended up) and remembers whether every merged event was identical, which is
// the common case: the 5 extra frames of one NFS write are the same
// upload-of-the-same-name over and over, so the frame that lands is exactly
// the frame a single write would have produced, plus a count.
type pendingChange struct {
	ev      ChangeEvent
	count   int
	uniform bool
}

func (p *pendingChange) merge(ev ChangeEvent) {
	if p.uniform && p.ev != ev {
		p.uniform = false
	}
	p.ev = ev
	p.count++
}

// frame renders the merged event. A mixed burst keeps the last action (the
// listing is refetched either way) but drops Name/NewName: naming one of 5 000
// changed files is worse than naming none, and Count already says it was many.
func (p *pendingChange) frame() ChangeEvent {
	if p.uniform {
		return p.ev
	}
	return ChangeEvent{Action: p.ev.Action}
}

// Hub is the process-wide registry of rooms and their subscribers. All state
// is guarded by a single mutex; broadcasts build the frame under the lock and
// enqueue it with a non-blocking send, so the critical section is short and a
// stalled client never blocks others.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*room

	// Coalescing window bounds — see room. Fields rather than constants so the
	// tests can shrink them and stay fast; nothing outside this package sets
	// them.
	coalesceMin time.Duration
	coalesceMax time.Duration

	// now is time.Now, swappable in tests.
	now func() time.Time
}

// Default coalescing bounds.
//
// coalesceMin is the floor on the gap between two frames for one folder. It is
// deliberately NOT a delay on the first frame: a quiet room emits immediately,
// so a single write's announce latency is untouched — measured 2026-09-06, a
// WebDAV PUT's frame landed 24 ms after the request before and 21 ms after it,
// and an NFS write's first frame at +17 ms either way. It only sets how quickly
// a SECOND frame may follow.
//
// coalesceMax is where the backoff stops, so a long job keeps telling an open
// explorer that the folder is still filling up instead of going silent until it
// finishes. It is the number that decides worst-case staleness DURING a burst,
// and it was chosen against the 3 s write-to-visible bar with room to spare:
// measured over a 5 000-file extraction, the longest a folder went un-refreshed
// was 2.0 s at 1.5 s, against 2.7 s at 2 s and 1.7 s at 1 s — and every step
// down doubles the frames (102 → 134 → 217).
const (
	defaultCoalesceMin = 200 * time.Millisecond
	defaultCoalesceMax = 1500 * time.Millisecond
)

// NewHub returns an empty, ready-to-use Hub.
func NewHub() *Hub {
	return &Hub{
		rooms:       make(map[string]*room),
		coalesceMin: defaultCoalesceMin,
		coalesceMax: defaultCoalesceMax,
		now:         time.Now,
	}
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

// EmitChange tells every client viewing (storageID, dir) that the folder
// changed. Safe to call for a room with no subscribers (no-op). This is the
// method the mutation handlers reach through the handlers.ChangeEmitter
// interface.
//
// The first change in a quiet room is broadcast synchronously, exactly as it
// always was. Changes arriving on top of a recent one are merged and sent as a
// single frame when the room's window elapses — see room for why, and for the
// promise that the last event of a burst is the one that lands.
func (h *Hub) EmitChange(storageID int64, dir string, ev ChangeEvent) {
	key := RoomKey(storageID, dir)
	h.mu.Lock()
	rm := h.rooms[key]
	if rm == nil || len(rm.clients) == 0 {
		h.mu.Unlock()
		return
	}
	// ⚠ Presence is fixed up for EVERY event, coalesced or not. It is per-client
	// state rather than a frame, so folding it into the merge would lose a focus
	// correction for a file that was renamed in the middle of a burst.
	presenceDirty := h.applyPresenceLocked(rm, ev)

	now := h.now()
	if rm.pending == nil && now.Sub(rm.lastSent) >= rm.effectiveWindow(h) {
		// Quiet room: straight out, nothing added. The burst (if there was one)
		// is over, so the window goes back to its floor.
		rm.window = h.coalesceMin
		rm.lastSent = now
		h.sendChangeLocked(rm, ev, 0)
	} else {
		if rm.pending == nil {
			rm.pending = &pendingChange{ev: ev, count: 1, uniform: true}
		} else {
			rm.pending.merge(ev)
		}
		h.armFlushLocked(key, rm, now)
	}

	if presenceDirty {
		h.broadcastPresenceLocked(key)
	}
	h.mu.Unlock()
}

// effectiveWindow is the room's current gap floor, defaulting to the hub's
// minimum for a room that has never emitted.
func (rm *room) effectiveWindow(h *Hub) time.Duration {
	if rm.window <= 0 {
		return h.coalesceMin
	}
	return rm.window
}

// applyPresenceLocked carries a viewer's focus across a rename and clears it on
// a delete — the focuser's client has no reason to re-send it, so it is fixed
// server-side. Reports whether the roster needs re-broadcasting.
// Caller holds h.mu.
func (h *Hub) applyPresenceLocked(rm *room, ev ChangeEvent) bool {
	dirty := false
	for c := range rm.clients {
		if c.file == "" || c.file != ev.Name {
			continue
		}
		switch ev.Action {
		case "rename", "move":
			if ev.NewName != "" && ev.NewName != ev.Name {
				c.file = ev.NewName
				dirty = true
			}
		case "delete":
			c.file = ""
			dirty = true
		}
	}
	return dirty
}

// armFlushLocked schedules the trailing frame for a room that has something
// pending, at the earliest moment its window allows. A timer already running
// is left alone — it will pick up whatever has accumulated by the time it
// fires. Caller holds h.mu.
func (h *Hub) armFlushLocked(key string, rm *room, now time.Time) {
	if rm.timer != nil {
		return
	}
	wait := rm.effectiveWindow(h) - now.Sub(rm.lastSent)
	if wait < 0 {
		wait = 0
	}
	rm.timer = time.AfterFunc(wait, func() { h.flush(key) })
}

// flush sends the merged frame for one room and widens its window, so a burst
// that keeps going costs progressively fewer frames while still reporting
// progress. A room that emptied or was already flushed is a no-op.
func (h *Hub) flush(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rm := h.rooms[key]
	if rm == nil {
		return
	}
	rm.timer = nil
	p := rm.pending
	if p == nil {
		return
	}
	rm.pending = nil
	rm.lastSent = h.now()
	if next := rm.effectiveWindow(h) * 2; next > h.coalesceMax {
		rm.window = h.coalesceMax
	} else {
		rm.window = next
	}
	h.sendChangeLocked(rm, p.frame(), p.count)
}

// sendChangeLocked stamps each client's OWN subscribed path — so confined
// (relative) and native (absolute) viewers of the same room each get a frame
// they recognize — and enqueues it. count 0/1 renders as a plain frame.
// Caller holds h.mu.
func (h *Hub) sendChangeLocked(rm *room, ev ChangeEvent, count int) {
	if count == 1 {
		count = 0
	}
	for c := range rm.clients {
		frame, err := json.Marshal(wireChange{
			Type:    "change",
			Path:    c.path,
			Action:  ev.Action,
			Name:    ev.Name,
			NewName: ev.NewName,
			Count:   count,
		})
		if err != nil {
			continue
		}
		trySend(c, frame)
	}
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
		// Nobody left to tell: drop the room and its armed flush with it. The
		// timer would find no room and do nothing anyway, but leaving one
		// running per emptied room is a slow leak on a busy server.
		if rm.timer != nil {
			rm.timer.Stop()
			rm.timer = nil
		}
		rm.pending = nil
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
			byIdent[ident] = PresenceUser{ID: c.UserID, UID: ident, Name: c.Name, File: c.file, Avatar: c.Avatar}
			continue
		}
		// Prefer an entry that has a focused file so a person reading a
		// document in one tab still shows that file.
		if existing.File == "" && c.file != "" {
			existing.File = c.file
			byIdent[ident] = existing
		}
		// …and a picture from whichever of their connections carries one: an
		// older client that never sends an avatar must not blank out the face
		// their other tab is already showing.
		if existing.Avatar == "" && c.Avatar != "" {
			existing.Avatar = c.Avatar
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

package realtime

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// ── recursive watches ───────────────────────────────────────────────────
//
// A room is one folder, and a client sits in exactly one room at a time.
// That is the right shape for an explorer (it shows one folder) and the
// wrong one for anything that MIRRORS a tree: the desktop sync engine keeps
// whole subtrees on disk, and a change three levels below a paired folder
// has to reach it just as fast as a change in the folder itself. Joining a
// room per sub-folder would take one socket per folder — thousands for a
// real tree — so a watch is a different kind of subscription:
//
//   - recursive: a watch on `docs://Projects` hears every change at or below
//     that folder;
//   - many per connection: one socket carries every paired root of an
//     account;
//   - silent: a watcher is not a viewer, so it never appears in anyone's
//     presence bar and never receives presence frames. A sync engine that
//     showed up as "Burak (filex desktop)" in every folder it mirrors would
//     be a lie about who is looking.
//
// The frame a watcher receives is its own type, "tree_change", so a browser
// that only knows "change"/"presence" never mistakes one for the other.
//
// ⚠ Why RBAC is checked once, at the root: grants in filex are additive — the
// effective level on a path is the HIGHEST grant covering it or any ancestor
// (acl.Set.Effective). Whoever may read the root may read everything below it,
// so the per-event path check a room would need is not needed here. The ws
// handler checks the root exactly like a room subscribe (confine + RBAC).
//
// ⚠ Derived events are not delivered. The folder-size refresher (internal/
// sync.SizeRefresher) announces a "modify" on every ANCESTOR of a changed
// folder ~2 s after the change itself, so that open explorers repaint their
// size column. For a mirror those frames say nothing new — the real change
// already arrived — and a watcher that obeyed them would re-list every folder
// up to the root after every single save, including its own uploads. See
// ChangeEvent.Derived.

// WatchRoot is one validated root a client asked to watch.
type WatchRoot struct {
	StorageID int64
	Dir       string // any spelling; normalised like a room key
	Display   string // the client's own spelling, echoed in frames
}

// watch is one registered root of one client, with its own coalescing state.
type watch struct {
	client    *Client
	storageID int64
	prefix    string // normalizeDir form: "" or "/a/b"
	display   string

	lastSent time.Time
	window   time.Duration
	pending  *watchPending
	timer    *time.Timer
}

// watchMaxDirs bounds the set of distinct folders one merged frame names. A
// 5 000-file extraction touches thousands of folders; past this many the frame
// says "overflow" instead, and a mirror does a full pass — which is what it
// would have to do with that many folders anyway, and cheaper than a frame
// listing every one of them.
const watchMaxDirs = 256

// watchPending is the merge of every change a watch has seen since its last
// frame.
type watchPending struct {
	dirs     map[string]struct{} // root-relative, "" = the root itself
	overflow bool
	count    int
	last     ChangeEvent
	uniform  bool
}

func (p *watchPending) add(rel string, ev ChangeEvent) {
	if p.dirs == nil {
		p.dirs = map[string]struct{}{}
	}
	if !p.overflow {
		if _, ok := p.dirs[rel]; !ok && len(p.dirs) >= watchMaxDirs {
			p.overflow = true
			p.dirs = nil
		} else {
			p.dirs[rel] = struct{}{}
		}
	}
	if p.count == 0 {
		p.uniform = true
	} else if p.uniform && p.last != ev {
		p.uniform = false
	}
	p.last = ev
	p.count++
}

// wireTreeChange is the envelope a watcher receives.
//
// Dirs are RELATIVE to the watched root ("" is the root itself), not absolute
// paths: a confined (embedded) client spells its root differently from the
// storage's absolute path, and a relative answer is the one spelling both
// sides agree on. Action/Name/NewName are carried only when every merged event
// was the same one — naming one of many would be worse than naming none.
type wireTreeChange struct {
	Type     string   `json:"type"` // always "tree_change"
	Root     string   `json:"root"`
	Dirs     []string `json:"dirs,omitempty"`
	Overflow bool     `json:"overflow,omitempty"`
	Action   string   `json:"action,omitempty"`
	Name     string   `json:"name,omitempty"`
	NewName  string   `json:"new_name,omitempty"`
	Count    int      `json:"count,omitempty"`
}

// Watch replaces the set of roots c watches. An empty roots slice removes every
// watch. Nothing is sent from here; the caller acknowledges.
func (h *Hub) Watch(c *Client, roots []WatchRoot) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unwatchLocked(c)
	for _, r := range roots {
		w := &watch{
			client:    c,
			storageID: r.StorageID,
			prefix:    normalizeDir(r.Dir),
			display:   r.Display,
		}
		if h.watches == nil {
			h.watches = map[int64][]*watch{}
		}
		h.watches[r.StorageID] = append(h.watches[r.StorageID], w)
		c.watches = append(c.watches, w)
	}
}

// Watching reports how many roots c currently watches. For tests and
// diagnostics.
func (h *Hub) Watching(c *Client) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(c.watches)
}

// WatchedRoots lists the folders of storageID that some client watches
// recursively right now, each once, in normalizeDir form ("" is the storage
// root, otherwise "/a/b"). A lazily catalogued storage keeps these subtrees
// current, because a desktop sync pair mirrors every folder under its root
// whether or not anybody opens it (internal/sync lazy.go).
func (h *Hub) WatchedRoots(storageID int64) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, w := range h.watches[storageID] {
		if !seen[w.prefix] {
			seen[w.prefix] = true
			out = append(out, w.prefix)
		}
	}
	sort.Strings(out)
	return out
}

// unwatchLocked drops every watch of c. Caller holds h.mu.
func (h *Hub) unwatchLocked(c *Client) {
	for _, w := range c.watches {
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
		w.pending = nil
		list := h.watches[w.storageID]
		for i, x := range list {
			if x == w {
				list = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(h.watches, w.storageID)
		} else {
			h.watches[w.storageID] = list
		}
	}
	c.watches = nil
}

// relUnder reports whether dir (normalizeDir form) is prefix or below it, and
// the root-relative remainder.
func relUnder(prefix, dir string) (string, bool) {
	if prefix == "" {
		return strings.TrimPrefix(dir, "/"), true
	}
	if dir == prefix {
		return "", true
	}
	if strings.HasPrefix(dir, prefix+"/") {
		return dir[len(prefix)+1:], true
	}
	return "", false
}

// emitWatchesLocked hands one change to every watch covering (storageID, dir).
// Same leading-edge-then-merge shape as a room (see room): the first change in
// a quiet watch goes out at once, so a single save costs a mirror nothing in
// latency, and a burst becomes one trailing frame per window. Caller holds h.mu.
func (h *Hub) emitWatchesLocked(storageID int64, dir string, ev ChangeEvent) {
	if ev.Derived {
		return
	}
	list := h.watches[storageID]
	if len(list) == 0 {
		return
	}
	dir = normalizeDir(dir)
	now := h.now()
	for _, w := range list {
		rel, ok := relUnder(w.prefix, dir)
		if !ok {
			continue
		}
		if w.pending == nil && now.Sub(w.lastSent) >= w.effectiveWindow(h) {
			w.window = h.coalesceMin
			frame := wireTreeChange{Type: "tree_change", Root: w.display, Dirs: []string{rel},
				Action: ev.Action, Name: ev.Name, NewName: ev.NewName}
			if h.sendWatchLocked(w, frame) {
				w.lastSent = now
				continue
			}
			// ⚠ The client's queue is full. A dropped room frame costs a viewer
			// one stale listing; a dropped watch frame costs a mirror a missed
			// edit until its next full pass. So nothing is dropped here: the
			// change goes into the pending merge and the flush retries it.
		}
		if w.pending == nil {
			w.pending = &watchPending{}
		}
		w.pending.add(rel, ev)
		h.armWatchFlushLocked(w, now)
	}
}

func (w *watch) effectiveWindow(h *Hub) time.Duration {
	if w.window <= 0 {
		return h.coalesceMin
	}
	return w.window
}

func (h *Hub) armWatchFlushLocked(w *watch, now time.Time) {
	if w.timer != nil {
		return
	}
	wait := w.effectiveWindow(h) - now.Sub(w.lastSent)
	if wait < 0 {
		wait = 0
	}
	w.timer = time.AfterFunc(wait, func() { h.flushWatch(w) })
}

// flushWatch sends a watch's merged frame. If the client's queue is still
// full, the merge is kept and retried one window later — the only way a
// pending change leaves is by being delivered, or by the client going away.
func (h *Hub) flushWatch(w *watch) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.timer = nil
	p := w.pending
	if p == nil || !h.watchAliveLocked(w) {
		return
	}
	frame := wireTreeChange{Type: "tree_change", Root: w.display, Overflow: p.overflow}
	if !p.overflow {
		frame.Dirs = make([]string, 0, len(p.dirs))
		for d := range p.dirs {
			frame.Dirs = append(frame.Dirs, d)
		}
		sort.Strings(frame.Dirs)
	}
	if p.uniform {
		frame.Action, frame.Name, frame.NewName = p.last.Action, p.last.Name, p.last.NewName
	} else {
		frame.Action = p.last.Action
	}
	if p.count > 1 {
		frame.Count = p.count
	}
	now := h.now()
	if !h.sendWatchLocked(w, frame) {
		w.lastSent = now
		h.armWatchFlushLocked(w, now)
		return
	}
	w.pending = nil
	w.lastSent = now
	if next := w.effectiveWindow(h) * 2; next > h.coalesceMax {
		w.window = h.coalesceMax
	} else {
		w.window = next
	}
}

// watchAliveLocked reports whether w is still registered (its client may have
// re-watched or disconnected while the timer was pending).
func (h *Hub) watchAliveLocked(w *watch) bool {
	for _, x := range h.watches[w.storageID] {
		if x == w {
			return true
		}
	}
	return false
}

func (h *Hub) sendWatchLocked(w *watch, frame wireTreeChange) bool {
	raw, err := json.Marshal(frame)
	if err != nil {
		return true // nothing sensible to retry
	}
	select {
	case w.client.Send <- raw:
		return true
	default:
		return false
	}
}

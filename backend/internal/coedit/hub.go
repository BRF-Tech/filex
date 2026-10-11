package coedit

import "sync"

// hub is this process's half of live delivery: which members' sockets are
// open here, so a new entry wakes them at once and a cursor reaches them.
// Entries themselves are always read from the database, so nothing here can
// lose one or hand one out of order.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[*Watcher]struct{}
}

func newHub() *hub { return &hub{subs: map[string]map[*Watcher]struct{}{}} }

// Watcher is one member's open socket in this process.
type Watcher struct {
	SID    string
	Client string
	// Notify gets a signal (never more than one waiting) when an entry lands
	// in this process: read the log from where the socket is.
	Notify chan struct{}
	// Eph gets the other members' cursors. A full buffer drops them: a
	// cursor is worth nothing late.
	Eph chan Eph
	// Ended gets why the membership ended ("left", "timeout", "closed").
	Ended chan string
	h     *hub
	once  sync.Once
}

func (h *hub) watch(sid, client string) *Watcher {
	w := &Watcher{SID: sid, Client: client, Notify: make(chan struct{}, 1), Eph: make(chan Eph, 64), Ended: make(chan string, 1), h: h}
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.subs[sid]
	if set == nil {
		set = map[*Watcher]struct{}{}
		h.subs[sid] = set
	}
	set[w] = struct{}{}
	return w
}

// Close takes the watcher out of the hub. Safe to call twice.
func (w *Watcher) Close() {
	w.once.Do(func() {
		w.h.mu.Lock()
		defer w.h.mu.Unlock()
		if set := w.h.subs[w.SID]; set != nil {
			delete(set, w)
			if len(set) == 0 {
				delete(w.h.subs, w.SID)
			}
		}
	})
}

func (h *hub) publish(sid string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.subs[sid] {
		select {
		case w.Notify <- struct{}{}:
		default:
		}
	}
}

func (h *hub) eph(sid string, e Eph) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.subs[sid] {
		if w.Client == e.Client {
			continue
		}
		select {
		case w.Eph <- e:
		default:
		}
	}
}

// end tells the sockets of one member (client "" = every member) that the
// membership ended.
func (h *hub) end(sid, client, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.subs[sid] {
		if client != "" && w.Client != client {
			continue
		}
		select {
		case w.Ended <- reason:
		default:
		}
	}
}

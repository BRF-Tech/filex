package coedit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Cursors across processes (filex 0.56). A cursor frame is not kept in the
// log: the process a member's socket is on hands it at once to the members
// connected to that process (hub.eph). For the members connected to another
// process on the same database, the member's LATEST sealed cursor is also
// written to its row in coedit_cursors (migration 00109) - at most every
// CursorEvery per member, the last one always - and each socket reads the
// rows of its session that changed since it last looked, on the poll that
// already reads the log (Options.PollEvery). A member therefore sees another
// process's cursor within CursorEvery + PollEvery, and its own process's at
// once; the reader takes a cursor only when its counter grew, so one
// delivered both ways is shown once.

// CursorEvery is the least time between two writes of one member's cursor
// to the database.
const CursorEvery = 500 * time.Millisecond

type cursorKey struct{ sid, client string }

type cursorSlot struct {
	last    time.Time
	pending *Eph
	timer   *time.Timer
}

// cursorWriter throttles the database writes of the members' cursors.
type cursorWriter struct {
	mu    sync.Mutex
	slots map[cursorKey]*cursorSlot
}

// put writes e as its member's latest cursor now, or - when the member's
// last write was less than CursorEvery ago - when that time is up (the
// latest one then; the ones between are not worth a write).
func (w *cursorWriter) put(r *Relay, sid string, e Eph) {
	k := cursorKey{sid, e.Client}
	now := r.opts.Now()
	w.mu.Lock()
	if w.slots == nil {
		w.slots = map[cursorKey]*cursorSlot{}
	}
	s := w.slots[k]
	if s == nil {
		s = &cursorSlot{}
		w.slots[k] = s
	}
	if s.timer == nil && now.Sub(s.last) >= CursorEvery {
		s.last = now
		w.mu.Unlock()
		r.writeCursor(sid, e)
		return
	}
	e2 := e
	s.pending = &e2
	if s.timer == nil {
		wait := CursorEvery - now.Sub(s.last)
		if wait < 0 {
			wait = 0
		}
		s.timer = time.AfterFunc(wait, func() { w.flush(r, k) })
	}
	w.mu.Unlock()
}

func (w *cursorWriter) flush(r *Relay, k cursorKey) {
	w.mu.Lock()
	s := w.slots[k]
	if s == nil {
		w.mu.Unlock()
		return
	}
	e := s.pending
	s.pending, s.timer = nil, nil
	s.last = r.opts.Now()
	w.mu.Unlock()
	if e != nil {
		r.writeCursor(k.sid, *e)
	}
}

// forget drops a member's slot (it left), or every slot of a session (client
// ""); a write already scheduled is stopped.
func (w *cursorWriter) forget(sid, client string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, s := range w.slots {
		if k.sid != sid || (client != "" && k.client != client) {
			continue
		}
		if s.timer != nil {
			s.timer.Stop()
		}
		delete(w.slots, k)
	}
}

// prune drops the slots idle for a minute: a member that stopped moving its
// cursor costs nothing.
func (w *cursorWriter) prune(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, s := range w.slots {
		if s.timer == nil && now.Sub(s.last) > time.Minute {
			delete(w.slots, k)
		}
	}
}

func (r *Relay) writeCursor(sid string, e Eph) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	at := e.At
	if at == 0 {
		at = r.nowMs()
	}
	if err := r.store.PutCoEditCursor(ctx, &model.CoEditCursor{SID: sid, Client: e.Client, Ctr: int64(e.Ctr), CT: e.CT, AtMs: at}); err != nil {
		slog.Debug("coedit: a cursor was not written for the other processes", slog.String("sid", sid), slog.String("err", err.Error()))
	}
}

// Cursors returns the latest cursors of a session's members written after
// sinceMs (Unix ms), oldest first: what a socket reads on its poll. The
// caller checked the reader is a member, and skips its own.
func (r *Relay) Cursors(ctx context.Context, sid string, sinceMs int64) ([]Eph, error) {
	rows, err := r.store.ListCoEditCursors(ctx, sid, sinceMs)
	if err != nil {
		return nil, err
	}
	out := make([]Eph, 0, len(rows))
	for _, c := range rows {
		out = append(out, Eph{Client: c.Client, Ctr: uint64(c.Ctr), CT: c.CT, At: c.AtMs})
	}
	return out, nil
}

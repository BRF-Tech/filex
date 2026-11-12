// Package spancount counts per client over the current span of the clock and
// the one before it: the counting under filex's per-client limiters (the
// misses a short-link guesser makes, the visits a public route takes, the
// uploads a file request takes from one address).
//
// A count is the two spans added up, so it never drops to zero at a
// boundary: a fixed window would let a client make twice the limit across
// one. A limiter that holds a client to a limit over this count lets it make
// at most that limit in any stretch one span long, and a client that reached
// it waits until the end of the next span (two spans at most).
//
// ⚠⚠ Built to be flooded. Every operation is O(1): a span's counts live in
// one map, replaced whole when the clock turns to the next span (the clock,
// not a sweep, ends them), and a map holds at most Max clients. A client
// that finds the map full is not counted, rather than growing the server's
// memory or its CPU without end. A sweep over every client on each new one
// turned a flood of fresh addresses into an O(n²) loop twice: the short-link
// limiter's first measurement (2026-11-11, #313) and the file request's
// address limiter (#325).
//
// A Window is not safe for concurrent use: the limiter holding it keeps the
// lock.
package spancount

import "time"

// MaxClients is the most clients one span's map holds when a Window names no
// Max of its own.
const MaxClients = 200000

// Window counts per client over the current span of the clock and the one
// before it.
type Window struct {
	// Span is the length of one span; it must be set. The clock is cut into
	// spans of it (time.Truncate), the same cut in every process.
	Span time.Duration
	// Max is the most clients one span's map holds; 0 = MaxClients.
	Max int

	start     time.Time
	cur, prev map[string]int
}

func (w *Window) max() int {
	if w.Max > 0 {
		return w.Max
	}
	return MaxClients
}

// turn moves the window to now's span: the current map becomes the one
// before when the span that follows it starts, and both are new after a
// longer quiet.
func (w *Window) turn(now time.Time) {
	if w.Span <= 0 {
		panic("spancount: a Window needs a Span")
	}
	start := now.Truncate(w.Span)
	switch {
	case w.cur == nil:
		w.start, w.cur = start, map[string]int{}
	case start.After(w.start):
		if start.Sub(w.start) == w.Span {
			w.prev = w.cur
		} else {
			w.prev = nil
		}
		w.start, w.cur = start, map[string]int{}
	}
}

// Count answers client's count over this span and the one before, and counts
// nothing.
func (w *Window) Count(now time.Time, client string) int {
	w.turn(now)
	return w.cur[client] + w.prev[client]
}

// Add counts n for client and answers its count over this span and the one
// before; 0 when this span's map is full and the client is not in it (it is
// not tracked).
func (w *Window) Add(now time.Time, client string, n int) int {
	w.turn(now)
	c, ok := w.cur[client]
	if !ok && len(w.cur) >= w.max() {
		return 0
	}
	c += n
	w.cur[client] = c
	return c + w.prev[client]
}

// Forget drops client's count from both spans.
func (w *Window) Forget(client string) {
	delete(w.cur, client)
	delete(w.prev, client)
}

// Clients answers how many clients this span's map and the one before hold.
func (w *Window) Clients() (cur, prev int) { return len(w.cur), len(w.prev) }

package shortlink

import (
	"net/netip"
	"sync"
	"time"
)

// ── Guessing an address ────────────────────────────────────────────────────
//
// An app's public route answers secrets a few symbols long (a short link's
// code), so it must not be walked by trying one after another. The app's
// keyspace floor (pkg/pluginkit/shortcode) makes a guess almost certainly
// wrong; this limiter makes wrong guesses slow. The app answers `miss`, filex
// counts it against the client, and a client past either window is answered
// "too many tries" on EVERY address it asks for - one that exists included,
// so the lockout itself tells it nothing:
//
//   - MissBurst misses in this MissBurstWindow and the one before: held back
//     for MissBurstLock;
//   - MissDaily misses today and yesterday (UTC days): held back for
//     MissDailyLock.
//
// The miss that reaches the limit locks: the next request is not asked of
// the app at all.
//
// The second is what bounds a patient guesser: the first alone lets a client
// spread 20 tries over every 10 minutes, 2,880 a day.
//
// ⚠⚠ Built to be flooded. Every operation is O(1): counts live in maps for
// the current window, replaced whole when the window turns (the clock, not a
// sweep, ends them), and each map holds at most MaxClients clients. A flood
// from more addresses than that is not tracked past the cap - those clients
// get their first misses uncounted - rather than growing the server's memory
// or its CPU without end (a sweep over every client on each new one turned a
// botnet flood into an O(n²) loop in the first measurement, 2026-11-11). The
// warm-instance pool still bounds what such a flood costs.
//
// ⚠ Per process. Two filex processes behind one address each keep their own
// count, so the rate an attacker gets doubles with them; the floor assumes
// ten thousand addresses for that reason. A file PIN's lock is kept on its
// share row instead because there the secret is four digits.

// The limiter's rule. A client is an IPv4 address or an IPv6 /64.
const (
	MissBurst       = 20
	MissBurstWindow = 10 * time.Minute
	MissBurstLock   = 30 * time.Minute
	MissDaily       = 100
	MissDailyWindow = 24 * time.Hour
	MissDailyLock   = 24 * time.Hour
	// MissesPerDay is the most wrong guesses one client gets in a day.
	MissesPerDay = MissDaily
	// AlarmPerHour misses from every client together raise the alarm once in
	// an hour: many addresses guessing at once is an attack the per-client
	// limit alone does not stop.
	AlarmPerHour = 2000
	// MaxClients is the most clients one window's map holds.
	MaxClients = 200000
)

// window counts per client over the current span of the clock and the one
// before it: a count is the two added up, so it never drops to zero at a
// boundary (a fixed window would let a client make twice the limit across
// one), and turning the span is one map replaced, not a sweep.
type window struct {
	span      time.Duration
	start     time.Time
	cur, prev map[string]int
}

// turn moves the window to now's span.
func (w *window) turn(now time.Time) {
	start := now.Truncate(w.span)
	switch {
	case w.cur == nil:
		w.start, w.cur = start, map[string]int{}
	case start.After(w.start):
		if start.Sub(w.start) == w.span {
			w.prev = w.cur
		} else {
			w.prev = nil
		}
		w.start, w.cur = start, map[string]int{}
	}
}

// add counts one for client and answers its count over the two spans; 0
// when the map is full and the client was not in it (not tracked).
func (w *window) add(now time.Time, client string) int {
	w.turn(now)
	n, ok := w.cur[client]
	if !ok && len(w.cur) >= MaxClients {
		return 0
	}
	n++
	w.cur[client] = n
	return n + w.prev[client]
}

// forget drops a client's count (a lockout starts it again).
func (w *window) forget(client string) {
	delete(w.cur, client)
	delete(w.prev, client)
}

// MissLimiter counts the addresses each client asked for that led nowhere.
type MissLimiter struct {
	// Alarm is called (outside the lock) once in an hour when the misses of
	// every client together pass AlarmPerHour; nil = nobody is told.
	Alarm func(misses int)
	// Now is the clock; nil = time.Now.
	Now func() time.Time

	mu       sync.Mutex
	burst    window
	daily    window
	locks    map[string]time.Time
	locksAt  time.Time
	total    int
	totalEnd time.Time
	alarmed  bool
}

func (l *MissLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// ClientKey is the limiter's key for an address: the address itself for IPv4,
// its /64 for IPv6 (one subscriber holds a whole /64). "" for no address.
func ClientKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return a.String()
	}
	return p.String()
}

// Locked reports whether the client is held back now.
func (l *MissLimiter) Locked(client string) bool {
	if client == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	until, ok := l.locks[client]
	return ok && l.now().Before(until)
}

// lock holds a client until until (the later of two locks wins). The locks
// map is emptied of ended locks at most once a minute, in one pass.
func (l *MissLimiter) lock(now time.Time, client string, until time.Time) {
	if l.locks == nil {
		l.locks = map[string]time.Time{}
	}
	if now.Sub(l.locksAt) > time.Minute {
		l.locksAt = now
		for k, u := range l.locks {
			if !now.Before(u) {
				delete(l.locks, k)
			}
		}
	}
	if cur, ok := l.locks[client]; ok && cur.After(until) {
		return
	}
	if _, ok := l.locks[client]; !ok && len(l.locks) >= MaxClients {
		return
	}
	l.locks[client] = until
}

// Miss counts one address that led nowhere and answers whether the client is
// locked out now.
func (l *MissLimiter) Miss(client string) bool {
	now := l.now()
	l.mu.Lock()
	if l.burst.span == 0 {
		l.burst.span, l.daily.span = MissBurstWindow, MissDailyWindow
	}
	alarm := 0
	if now.After(l.totalEnd) {
		l.total, l.totalEnd, l.alarmed = 0, now.Add(time.Hour), false
	}
	l.total++
	if l.total > AlarmPerHour && !l.alarmed {
		l.alarmed = true
		alarm = l.total
	}
	locked := false
	if client != "" {
		if n := l.daily.add(now, client); n >= MissDaily {
			l.lock(now, client, now.Add(MissDailyLock))
		}
		if n := l.burst.add(now, client); n >= MissBurst {
			l.lock(now, client, now.Add(MissBurstLock))
			// A new count once the lockout ends.
			l.burst.forget(client)
		}
		until, ok := l.locks[client]
		locked = ok && now.Before(until)
	}
	l.mu.Unlock()
	if alarm > 0 && l.Alarm != nil {
		l.Alarm(alarm)
	}
	return locked
}

// ── Asking too often ───────────────────────────────────────────────────────

// HitLimit visits from one client over this minute and the last reach the
// app at all, hits and misses alike: one address cannot take the route's
// whole pool.
const (
	HitLimit  = 120
	HitWindow = time.Minute
)

// RateLimiter is a fixed window per client, O(1) a request (window).
type RateLimiter struct {
	Limit  int
	Window time.Duration
	Now    func() time.Time

	mu sync.Mutex
	w  window
}

// Allow counts one request and answers whether it is within the limit. A
// client the full window does not track is let through: the pool bounds it.
func (l *RateLimiter) Allow(client string) bool {
	if client == "" {
		return true
	}
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w.span == 0 {
		l.w.span = l.Window
	}
	n := l.w.add(now, client)
	return n <= l.Limit
}

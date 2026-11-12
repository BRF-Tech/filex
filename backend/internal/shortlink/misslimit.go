package shortlink

import (
	"net/netip"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/spancount"
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
// sweep, ends them; internal/spancount), and each map holds at most
// MaxClients clients. A flood from more addresses than that is not tracked
// past the cap - those clients get their first misses uncounted - rather
// than growing the server's memory or its CPU without end (a sweep over
// every client on each new one turned a botnet flood into an O(n²) loop in
// the first measurement, 2026-11-11). The warm-instance pool still bounds
// what such a flood costs.
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
	MaxClients = spancount.MaxClients
)

// MissLimiter counts the addresses each client asked for that led nowhere.
type MissLimiter struct {
	// Alarm is called (outside the lock) once in an hour when the misses of
	// every client together pass AlarmPerHour; nil = nobody is told.
	Alarm func(misses int)
	// Now is the clock; nil = time.Now.
	Now func() time.Time

	mu       sync.Mutex
	burst    spancount.Window
	daily    spancount.Window
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
	if l.burst.Span == 0 {
		l.burst = spancount.Window{Span: MissBurstWindow, Max: MaxClients}
		l.daily = spancount.Window{Span: MissDailyWindow, Max: MaxClients}
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
		if n := l.daily.Add(now, client, 1); n >= MissDaily {
			l.lock(now, client, now.Add(MissDailyLock))
		}
		if n := l.burst.Add(now, client, 1); n >= MissBurst {
			l.lock(now, client, now.Add(MissBurstLock))
			// A new count once the lockout ends.
			l.burst.Forget(client)
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

// RateLimiter counts each client's requests over this window of the clock and
// the one before, O(1) a request (spancount.Window).
type RateLimiter struct {
	Limit  int
	Window time.Duration
	Now    func() time.Time

	mu sync.Mutex
	w  spancount.Window
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
	if l.w.Span == 0 {
		l.w = spancount.Window{Span: l.Window, Max: MaxClients}
	}
	n := l.w.Add(now, client, 1)
	return n <= l.Limit
}

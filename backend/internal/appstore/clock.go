package appstore

import (
	"sync"
	"time"
)

// clock answers "what time is it, at the latest?" for a license's grace.
//
// A license holds while the store cannot be reached until the store's own
// `grace_until`, a time IT signed. Judged by the server's wall clock alone,
// that deadline would be the administrator's to move: turn the clock back and
// the grace never ends. So the answer is the later of two readings:
//
//   - the wall clock;
//   - the PROVEN time of the store the license is with: the latest
//     `checked_at` that store signed, plus the time filex has run since,
//     counted on the process's monotonic clock (which a change of the wall
//     clock does not move) and kept in the database by the license loop, so a
//     restart carries it on. Time filex was not running is not counted: a
//     server that was off for a week resumes its grace where it stopped,
//     never later.
//
// Turning the clock back therefore changes nothing. Turning it forward ends a
// grace early - fail-safe - for as long as the clock stays ahead; the proven
// time never takes the wall clock in, so a mistake fixed is a mistake gone.
//
// ⚠⚠ The proof is PER STORE. A store's signed time is evidence about that
// store's licenses only: one store's answer (a mistake, or a store that wants
// another store's apps held) moves nothing for the apps another store
// licensed. And a store's checked_at is taken only within a day of this
// server's wall clock (answerSkew, license.go), so no answer can push the
// proven time further ahead than that.
//
// ⚠⚠ A restart. The proven time is kept at start and at the loop's rounds
// (Run: a minute after start, then hourly); what a run lived after its last
// keeping is unknown to the next one. Restarting filex often with the wall
// clock held back would otherwise stretch a grace by everything lost in
// between. So a start that follows a run which did not stop cleanly (the loop
// keeps the time once more on a clean shutdown, and says so) counts that run
// as having lived until its next keeping: every store's proven time moves
// forward by restartAllowance at once, and that is kept before anything else.
// The allowance is filex's own pessimism, not a store's word: it is recorded
// as the store's `debt`, and the store's next answer takes it back (the proof
// returns towards the store's checked_at, never below it), so crashes during
// normal running do not push the proven time ahead for good.
//
// ⚠ Only an answer the store signed LATER than any it gave before takes debt
// back: the latest checked_at taken (`signed`) is kept with the proof, across
// restarts too. Otherwise the store's last genuine answer, replayed (through
// a proxy whose certificate the administrator trusts), wiped the allowance at
// every unclean restart and the grace stretched again (store review, second
// round, Y2; lesson #1070).
type clock struct {
	mu   sync.Mutex
	wall func() time.Time
	mono func() time.Duration

	stores map[string]*proof
}

// proof is a store's proven time `at`, taken at the monotonic reading `mono`;
// debt is how much of it is restart allowance, not a store's word.
type proof struct {
	at   time.Time
	mono time.Duration
	debt time.Duration
	// signed is the latest checked_at the store signed and filex took.
	signed time.Time
}

// restartAllowance is how long a run that did not stop cleanly is taken to
// have lived past its last keeping: the loop's keeping interval (Run).
const restartAllowance = time.Hour

func newClock(wall func() time.Time, mono func() time.Duration) *clock {
	return &clock{wall: wall, mono: mono, stores: map[string]*proof{}}
}

// provenLocked is store's proven time now; zero before any proof.
func (c *clock) provenLocked(store string) time.Time {
	p := c.stores[store]
	if p == nil || p.at.IsZero() {
		return time.Time{}
	}
	return p.at.Add(c.mono() - p.mono)
}

// NowFor is the later of the wall clock and store's proven time: the time a
// license of that store is judged at.
func (c *clock) NowFor(store string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.wall().UTC()
	if p := c.provenLocked(store); p.After(now) {
		now = p
	}
	return now
}

// Now is the later of the wall clock and every store's proven time: what the
// panel may say filex has proof of. No license is judged by it.
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.wall().UTC()
	for store := range c.stores {
		if p := c.provenLocked(store); p.After(now) {
			now = p
		}
	}
	return now
}

// Proven is every store's proven time now, its debt and the latest
// checked_at taken from it (what the loop keeps).
func (c *clock) Proven() (map[string]time.Time, map[string]time.Duration, map[string]time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]time.Time, len(c.stores))
	debt := map[string]time.Duration{}
	signed := map[string]time.Time{}
	for store, p := range c.stores {
		if t := c.provenLocked(store); !t.IsZero() {
			out[store] = t
			if p.debt > 0 {
				debt[store] = p.debt
			}
			if !p.signed.IsZero() {
				signed[store] = p.signed
			}
		}
	}
	return out, debt, signed
}

// Prove takes a store's signed time. The proven time only moves forward: a
// store whose clock is behind the proof filex already has moves nothing -
// except the proof's restart allowance (debt), which the store's word takes
// back, down to its checked_at and no further, and only when that word is
// newer than any the store gave before (a replay takes nothing back).
func (c *clock) Prove(store string, t time.Time) {
	if t.IsZero() || store == "" {
		return
	}
	t = t.UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.provenLocked(store)
	p := c.stores[store]
	if t.After(cur) {
		c.stores[store] = &proof{at: t, mono: c.mono(), signed: t}
		return
	}
	if p == nil {
		return
	}
	newer := t.After(p.signed)
	if newer {
		p.signed = t
	}
	if p.debt > 0 && newer {
		back := cur.Add(-p.debt)
		if back.Before(t) {
			back = t
		}
		left := p.debt - cur.Sub(back)
		if left < 0 {
			left = 0
		}
		c.stores[store] = &proof{at: back, mono: c.mono(), debt: left, signed: p.signed}
	}
}

// Restore puts back the proven times (their debt, the latest checked_at
// taken) a previous run kept, counted on from now.
func (c *clock) Restore(times map[string]time.Time, debt map[string]time.Duration, signed map[string]time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for store, t := range times {
		if t.IsZero() || store == "" || !t.UTC().After(c.provenLocked(store)) {
			continue
		}
		c.stores[store] = &proof{at: t.UTC(), mono: c.mono(), debt: debt[store], signed: signed[store].UTC()}
	}
}

// Allow moves every store's proven time forward by d, as debt (a start after
// a run that did not stop cleanly).
func (c *clock) Allow(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for store, p := range c.stores {
		if t := c.provenLocked(store); !t.IsZero() {
			c.stores[store] = &proof{at: t.Add(d), mono: c.mono(), debt: p.debt + d, signed: p.signed}
		}
	}
}

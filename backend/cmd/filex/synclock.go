package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/filesync"
)

// ── one engine per pair on this computer (filesync lock.go) ──────────────
//
// A one-shot `filex sync run` needs nothing here: each pass takes its pair's
// lock itself, a busy pair's pass returns a *filesync.BusyError, the reporter
// prints the busy line and the command exits with exitPairBusy once every
// other pair has run.
//
// `filex sync run --watch` holds the lock of every pair it syncs, from the
// moment it adopts the pair until it lets it go (removed, paused, or the
// watcher stops) — a lock taken and dropped around each pass would let a
// second watcher slip in between two passes and plan from a baseline this
// one is halfway through. A pair it cannot lock is left out of everything —
// no pass, no stream root, no file-system watch — and asked again at
// lockRetry; when the other process goes (quits, is killed, crashes) the OS
// lets go of the lock and this watcher adopts the pair like a newly added one:
// a full pass first.
//
// The lines, on stdout, are the contract with desktop/src/syncstatus.ts:
//
//	<pair>: lock: busy — <why>    another process holds the pair; nothing runs for it here
//	<pair>: lock: acquired        …not any more: this process syncs it now
//
// ⚠ Busy is reported only after lockGrace. The desktop app restarts its
// watcher whenever a setting changes, and on macOS/Linux the old one exits
// gracefully (SIGTERM, then a final checkpoint of up to 5 s): the new watcher
// meets its own predecessor's lock. Reporting that at once flashed "another
// filex is syncing this folder" under every folder on every restart; the fast
// retries inside the grace take the pair over the moment the old one is gone.

// Variables, not constants, only so the end-to-end test can shorten them.
var (
	// lockGrace is how long a pair may be busy before it is reported.
	lockGrace = 10 * time.Second
	// lockRetryFast is how often a busy pair is tried within the grace, and
	// lockRetry after it. A try is one open and one non-blocking lock call —
	// no server traffic — so a few seconds is cheap and hands the pair over
	// soon after the other process stops.
	lockRetryFast = 500 * time.Millisecond
	lockRetry     = 5 * time.Second
)

// lockBusyLine is the line that says a pair is busy (without the pair id), in
// the lock's own words whatever wrapped them on the way here.
func lockBusyLine(err error) string {
	var be *filesync.BusyError
	if errors.As(err, &be) {
		err = be
	}
	return "lock: busy — " + err.Error()
}

// pairLocker is what the watcher needs from pairLocks.
type pairLocker interface {
	// take makes sure this process holds the pair's lock; nil when it does
	// (already did, or does now). A pair another process holds is a
	// *filesync.BusyError.
	take(id string) error
	// retain gives back every held lock whose pair is not in keep.
	retain(keep map[string]bool)
	releaseAll()
}

// pairLocks is the set of pair locks one process holds. The watcher takes and
// gives back (pairLocker); each pass's engine is handed the held lock (get),
// so it does not try to take it a second time.
type pairLocks struct {
	st   *filesync.Store
	mu   sync.Mutex
	held map[string]*filesync.PairLock
}

func newPairLocks(st *filesync.Store) *pairLocks {
	return &pairLocks{st: st, held: map[string]*filesync.PairLock{}}
}

func (pl *pairLocks) take(id string) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.held[id] != nil {
		return nil
	}
	lk, err := pl.st.LockPair(id)
	if err != nil {
		return err
	}
	pl.held[id] = lk
	return nil
}

// get is the held lock of pair id, or nil — a pass then takes its own.
func (pl *pairLocks) get(id string) *filesync.PairLock {
	if pl == nil {
		return nil
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.held[id]
}

func (pl *pairLocks) retain(keep map[string]bool) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for id, lk := range pl.held {
		if !keep[id] {
			lk.Release()
			delete(pl.held, id)
		}
	}
}

func (pl *pairLocks) releaseAll() { pl.retain(nil) }

// busyPair is a pair this watcher wants and cannot lock.
type busyPair struct {
	since    time.Time
	next     time.Time // the next try
	reported bool      // "lock: busy" was printed
	lastErr  string    // a lock error that is NOT busy, printed once per message
}

// claim takes the locks of the wanted pairs and returns the ones this watcher
// holds — the pairs it syncs. Busy pairs not yet due for another try are not
// tried. Locks of pairs no longer wanted are given back. Without a locker
// every wanted pair is synced (each pass then locks itself).
func (l *liveLoop) claim(wanted []filesync.Pair) []filesync.Pair {
	l.mu.Lock()
	l.wanted = wanted
	l.mu.Unlock()
	if l.locker == nil {
		return wanted
	}
	now := l.now()
	keep := map[string]bool{}
	var active []filesync.Pair
	for _, p := range wanted {
		keep[p.ID] = true
		b := l.busy[p.ID]
		if b != nil && now.Before(b.next) {
			continue
		}
		err := l.locker.take(p.ID)
		if err == nil {
			if b != nil {
				delete(l.busy, p.ID)
				if b.reported {
					fmt.Fprintf(l.out, "%s: lock: acquired\n", p.ID)
				}
			}
			active = append(active, p)
			continue
		}
		if b == nil {
			b = &busyPair{since: now}
			l.busy[p.ID] = b
		}
		if errors.Is(err, filesync.ErrPairBusy) {
			if !b.reported && now.Sub(b.since) >= l.lockGrace {
				b.reported = true
				fmt.Fprintf(l.out, "%s: %s\n", p.ID, lockBusyLine(err))
			}
		} else if msg := err.Error(); msg != b.lastErr {
			// Not another process: the lock file itself cannot be had (a
			// permission, a full disk). It is this pair's error until the
			// lock is taken and a pass completes.
			b.lastErr = msg
			fmt.Fprintf(l.errOut, "%s: %s\n", p.ID, msg)
		}
		if b.reported || b.lastErr != "" {
			b.next = now.Add(l.lockRetry)
		} else {
			b.next = now.Add(l.lockRetryFast)
		}
	}
	for id := range l.busy {
		if !keep[id] {
			delete(l.busy, id)
		}
	}
	l.locker.retain(keep)
	return active
}

// lockDue is when the next busy pair is due for another try (zero: none).
func (l *liveLoop) lockDue() time.Time {
	var due time.Time
	for _, b := range l.busy {
		if due.IsZero() || b.next.Before(due) {
			due = b.next
		}
	}
	return due
}

// retryLocks tries the busy pairs that are due; a pair it now holds is
// installed like a newly added one. Nothing else is touched when every pair
// is still busy — installing re-arms the file-system watches, and doing that
// every few seconds for nothing would cost a folder that is too large to
// watch a walk each time.
func (l *liveLoop) retryLocks() {
	l.mu.Lock()
	wanted := l.wanted
	before := len(l.pairs)
	l.mu.Unlock()
	active := l.claim(wanted)
	if len(active) != before {
		l.install(active)
	}
}

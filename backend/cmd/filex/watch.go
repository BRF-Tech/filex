package main

import (
	"time"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// watchPlanner decides, pair by pair and tick by tick, whether a watcher
// needs to walk a pair at all.
//
// The old watcher ran every pair every tick, and a run starts by listing the
// whole server tree one folder at a time. A single Mac with 7,048 synced
// folders therefore sent 100–150 thousand listings an hour, around the clock,
// to learn that nothing had changed. The live paths (synclive.go) answer a
// change the moment it happens; the tick is the safety net, and it walks a
// pair only when:
//
//   - it is new to this watcher, or its entry in pairs.json was edited
//     (paused, moved, a hold confirmed or discarded);
//   - its local tree changed (a local walk, no request) — the file-system
//     watcher normally catches that at once, and this catches what it missed;
//   - the server says something under its folder changed (`action=changes`:
//     one request, answered from the server's change log) — asked only while
//     the live stream is NOT connected, because while it is, every change the
//     log could report was already announced over it;
//   - its last pass failed: the folders that failed are retried (a targeted
//     pass, not a walk), with a growing gap;
//   - the safety-net interval passed (fullEvery): changes the server's log
//     cannot see, such as bytes written straight into the storage and found
//     by a scan, are still picked up;
//   - on a server with no change log, a timer that doubles from the tick to
//     maxIdle while nothing happens and resets as soon as something does.
type watchPlanner struct {
	tick      time.Duration // --watch
	maxIdle   time.Duration // --watch-max
	fullEvery time.Duration // --full-every
	state     map[string]*pairWatch
}

// cursorsKept is how many recent change-log cursors a pair keeps while the
// live stream is connected. The OLDEST is what a reconnect asks about: a frame
// still in flight when the connection died was announced after a cursor taken
// a tick or two earlier, never after the newest one.
const cursorsKept = 3

// pairWatch is what the watcher remembers about one pair.
type pairWatch struct {
	pair filesync.Pair // as it was when it last ran
	// cursors are change-log cursors, oldest first: the one taken just
	// BEFORE the last full pass, then (while the stream is live) one per
	// tick. Empty = the server has no change log.
	cursors []string
	localFP string        // the local tree as the last full pass left it
	lastRun time.Time     // the last full pass: the safety net counts from here
	lastTry time.Time     // the last full pass or failed retry: the backoff counts from here
	idle    time.Duration // how long an unprompted walk or retry waits after lastTry
	clean   bool          // the last pass finished without errors
	retry   []string      // folders whose actions failed (a targeted retry)
}

func (st *pairWatch) cursor() string {
	if len(st.cursors) == 0 {
		return ""
	}
	return st.cursors[0]
}

func newWatchPlanner(tick, maxIdle, fullEvery time.Duration) *watchPlanner {
	if maxIdle < tick {
		maxIdle = tick
	}
	return &watchPlanner{tick: tick, maxIdle: maxIdle, fullEvery: fullEvery, state: map[string]*pairWatch{}}
}

// watchDecision is the answer for one pair on one tick.
type watchDecision struct {
	run    bool
	dirs   []string // nil = a full pass; otherwise the folders to retry
	cursor string   // the cursor to remember if a full pass runs
	why    string
}

// samePairConfig compares what a person (or the desktop app) controls about a
// pair. Held is the engine's own note and changes as a hold is refreshed.
func samePairConfig(a, b filesync.Pair) bool {
	return a.ID == b.ID && a.Local == b.Local && a.Remote == b.Remote && a.Account == b.Account &&
		a.Paused == b.Paused && a.File == b.File && a.HoldNew == b.HoldNew
}

// decide answers whether p runs now, with the live stream NOT connected.
// localFP walks the pair's local side; changes asks the server's change log
// (since "" = "give me a cursor"). The only error it returns is a 401: the
// token is gone and the watcher must stop.
func (w *watchPlanner) decide(p filesync.Pair, now time.Time, localFP func() (string, error), changes func(since string) (string, bool, error)) (watchDecision, error) {
	return w.plan(p, now, false, localFP, changes)
}

// liveTick is decide while the live stream IS connected: everything but the
// change-log question, which the stream answers better. Instead it takes a
// fresh cursor, so a later reconnect asks only about what happened since.
func (w *watchPlanner) liveTick(p filesync.Pair, now time.Time, localFP func() (string, error), changes func(since string) (string, bool, error)) (watchDecision, error) {
	return w.plan(p, now, true, localFP, changes)
}

func (w *watchPlanner) plan(p filesync.Pair, now time.Time, live bool, localFP func() (string, error), changes func(since string) (string, bool, error)) (watchDecision, error) {
	// The cursor a full pass should remember is the one BEFORE it starts, so
	// a change made while it runs is seen on the next tick.
	fresh := func(why string) (watchDecision, error) {
		cur, _, err := changes("")
		if cliclient.IsUnauthorized(err) {
			return watchDecision{}, err
		}
		if err != nil {
			cur = ""
		}
		return watchDecision{run: true, cursor: cur, why: why}, nil
	}

	st := w.state[p.ID]
	if st == nil || !samePairConfig(st.pair, p) {
		return fresh("new or edited pair")
	}
	if w.fullEvery > 0 && now.Sub(st.lastRun) >= w.fullEvery {
		return fresh("safety-net walk")
	}
	if fp, err := localFP(); err != nil || fp != st.localFP {
		return fresh("local change")
	}
	due := now.Sub(st.lastTry) >= st.idle
	if !st.clean && due {
		if len(st.retry) > 0 {
			return watchDecision{run: true, dirs: append([]string(nil), st.retry...), why: "retry"}, nil
		}
		return fresh("retry")
	}
	if live {
		// The stream announces every change the log could report; keep the
		// cursors fresh for the moment it drops.
		cur, _, err := changes("")
		switch {
		case cliclient.IsUnauthorized(err):
			return watchDecision{}, err
		case err == nil && cur != "":
			st.cursors = append(st.cursors, cur)
			if len(st.cursors) > cursorsKept {
				st.cursors = st.cursors[len(st.cursors)-cursorsKept:]
			}
		}
		return watchDecision{}, nil
	}
	if c := st.cursor(); c != "" {
		cur, changed, err := changes(c)
		switch {
		case err == nil && changed:
			return watchDecision{run: true, cursor: cur, why: "server change"}, nil
		case err == nil:
			return watchDecision{}, nil
		case cliclient.IsUnauthorized(err):
			return watchDecision{}, err
		}
		// Any other failure (a proxy error, a timeout): fall back to the
		// timer rather than guess either way.
	}
	if due {
		return fresh("timer")
	}
	return watchDecision{}, nil
}

// ran records a FULL pass of p that started with cursor. fp is the local tree
// as the pass left it (res.LocalFingerprint, or a fresh walk when the pass
// did not get that far).
func (w *watchPlanner) ran(p filesync.Pair, at time.Time, cursor string, res filesync.Result, runErr error, fp string) {
	st := w.state[p.ID]
	if st == nil {
		st = &pairWatch{idle: w.tick}
		w.state[p.ID] = st
	}
	st.pair = p
	st.lastRun = at
	st.lastTry = at
	st.cursors = nil
	if cursor != "" {
		st.cursors = []string{cursor}
	}
	st.localFP = fp
	st.clean = runErr == nil && len(res.Errors) == 0
	st.retry = nil
	if runErr == nil {
		st.retry = append([]string(nil), res.Retry...)
	}
	if st.clean && res.Planned > 0 {
		// Something moved: stay close, the person may be busy in there.
		st.idle = w.tick
		return
	}
	w.backOff(st)
}

// ranTargeted records a targeted pass (live or a retry). It says nothing about
// the rest of the tree, so the full-pass clock and the cursors stay; a failure
// is retried like a full pass's. fp, when set, is the local tree as it is now
// — the caller passes one only when the file-system watcher covers the pair,
// which is what makes a fresh walk safe here (an edit made during the pass
// raised its own event and is already queued).
func (w *watchPlanner) ranTargeted(p filesync.Pair, at time.Time, dirs []string, res filesync.Result, runErr error, fp string) {
	st := w.state[p.ID]
	if st == nil {
		return // no full pass yet: the first one is still owed
	}
	if fp != "" {
		st.localFP = fp
	}
	failed := runErr != nil || len(res.Errors) > 0
	if !failed {
		// The folders it just went through are fine now.
		keep := st.retry[:0]
		for _, d := range st.retry {
			if !containsDir(dirs, d) {
				keep = append(keep, d)
			}
		}
		st.retry = keep
		if len(st.retry) == 0 && !st.clean {
			st.clean = true
			st.idle = w.tick
		}
		return
	}
	st.clean = false
	for _, d := range res.Retry {
		if !containsDir(st.retry, d) {
			st.retry = append(st.retry, d)
		}
	}
	st.lastTry = at
	w.backOff(st)
}

// backOff doubles the wait before the next unprompted walk or retry.
func (w *watchPlanner) backOff(st *pairWatch) {
	next := st.idle * 2
	if next <= 0 {
		next = w.tick
	}
	if next > w.maxIdle {
		next = w.maxIdle
	}
	st.idle = next
}

// forget drops what the watcher knew about pairs that are gone.
func (w *watchPlanner) forget(keep map[string]bool) {
	for id := range w.state {
		if !keep[id] {
			delete(w.state, id)
		}
	}
}

func containsDir(dirs []string, d string) bool {
	for _, x := range dirs {
		if x == d {
			return true
		}
	}
	return false
}

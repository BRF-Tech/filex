package main

import (
	"time"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// watchPlanner decides, pair by pair and tick by tick, whether a watcher
// needs to run a pair at all.
//
// The old watcher ran every pair every tick, and a run starts by listing the
// whole server tree one folder at a time. A single Mac with 7,048 synced
// folders therefore sent 100–150 thousand listings an hour, around the clock,
// to learn that nothing had changed. Now a pair runs only when:
//
//   - it is new to this watcher, or its entry in pairs.json was edited
//     (paused, moved, a hold confirmed or discarded);
//   - its local tree changed (a local walk, no request);
//   - the server says something under its folder changed (`action=changes`:
//     one request, answered from the server's change log);
//   - its last run failed (retried with a growing gap);
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

// pairWatch is what the watcher remembers about one pair.
type pairWatch struct {
	pair    filesync.Pair // as it was when it last ran
	cursor  string        // change-log cursor taken just BEFORE that run; "" = none
	localFP string        // the local tree as that run left it
	lastRun time.Time
	idle    time.Duration // how long an unprompted run waits after the last one
	clean   bool          // that run finished without errors
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
	cursor string // the cursor to remember if it runs
	why    string
}

// samePairConfig compares what a person (or the desktop app) controls about a
// pair. Held is the engine's own note and changes as a hold is refreshed.
func samePairConfig(a, b filesync.Pair) bool {
	return a.ID == b.ID && a.Local == b.Local && a.Remote == b.Remote && a.Account == b.Account &&
		a.Paused == b.Paused && a.File == b.File && a.HoldNew == b.HoldNew
}

// decide answers whether p runs now. localFP walks the pair's local side;
// changes asks the server's change log (since "" = "give me a cursor"). The
// only error it returns is a 401: the token is gone and the watcher must stop.
func (w *watchPlanner) decide(p filesync.Pair, now time.Time, localFP func() (string, error), changes func(since string) (string, bool, error)) (watchDecision, error) {
	// The cursor a run should remember is the one BEFORE it starts, so a
	// change made while it runs is seen on the next tick.
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
	due := now.Sub(st.lastRun) >= st.idle
	if !st.clean {
		if due {
			return fresh("retry")
		}
		return watchDecision{}, nil
	}
	if st.cursor != "" {
		cur, changed, err := changes(st.cursor)
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

// ran records a run of p that started with cursor. fp is the local tree as
// the run left it (res.LocalFingerprint, or a fresh walk when the run did not
// get that far).
func (w *watchPlanner) ran(p filesync.Pair, at time.Time, cursor string, res filesync.Result, runErr error, fp string) {
	st := w.state[p.ID]
	if st == nil {
		st = &pairWatch{idle: w.tick}
		w.state[p.ID] = st
	}
	st.pair = p
	st.lastRun = at
	st.cursor = cursor
	st.localFP = fp
	st.clean = runErr == nil && len(res.Errors) == 0
	if st.clean && res.Planned > 0 {
		// Something moved: stay close, the person may be busy in there.
		st.idle = w.tick
		return
	}
	next := st.idle * 2
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

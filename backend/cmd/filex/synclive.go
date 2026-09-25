package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// ── `filex sync run --watch`: live, with a cheap safety net ──────────────
//
// Before v0.43 the watcher was a loop: sync every pair, sleep --watch (the
// desktop app passes 30 s), repeat. An edit on either side waited for the next
// lap — measured on this engine against a local server, a save in the browser
// took 6–25 s to reach the synced file (median 15 s), a local save 9–29 s to
// reach the server (median 25 s). And every lap walked the whole tree: one
// idle Mac with 7,048 synced folders sent 100–150 thousand listings an hour.
//
// Now three things wake it:
//
//   - the server's change stream (cliclient.ChangeStream): a folder changed on
//     the server → reconcile that folder (filesync RunDirs), one listing;
//   - the local file system (localWatcher): a file changed in a synced folder
//     → reconcile that folder, but only after the disk shows it really
//     differs from what the baseline agreed (LocalDirChanged) — the engine's
//     own downloads raise events too, and they must not cost a round-trip;
//   - the interval (--watch): the safety net for anything those two could
//     not tell us — a dropped connection, a server without the stream, a
//     platform or folder the file-system watcher cannot cover. It no longer
//     walks every pair: the watchPlanner (watch.go) walks a pair only when
//     its local tree changed, the server's change log (`action=changes`)
//     says something under it changed while the stream was down, a failed
//     folder is due a retry, or the --full-every safety net is due. A
//     reconnect asks the change log too, instead of walking everything.
//
// Every pass is still one engine run at a time, in this goroutine. Changes that
// arrive while a pass runs are collected and answered right after it.
//
// ⚠ A token the server refuses (HTTP 401) ends the whole watcher with exit
// status 3 (exitSignedOut): every pair of this process shares the token, and
// the desktop app asks the person to reconnect instead of restarting it. And
// with a --window, nothing talks to the server outside it — not a pass, not
// the stream — while changes seen in the meantime wait for the window.
//
// ⚠ It syncs only the pairs whose lock it holds (synclock.go): a pair another
// process on this computer is syncing — the desktop app's other copy, a
// terminal — gets nothing from this loop until that process lets go.

// Debounce. A change is answered after its source has been quiet for a moment,
// and never later than liveMaxWait after the first change of a burst.
//
// ⚠ Not zero. An editor's save is several file-system events (write the
// temporary file, rename it over the old one, delete a backup), and a browser
// autosave or an office force-save arrives as a burst of frames; answering the
// first event of each would sync a half-saved file or run three passes for one
// save. And not large: "the change must arrive immediately" is the request.
//
//   - remote 150 ms: the server already coalesces a burst per watch (leading
//     frame at once, then one merged frame per window), so the client only
//     needs to catch the frame that trails a leading one.
//   - local 400 ms: covers the save sequence of the editors this was measured
//     with (a rename-over save plus its metadata events land within ~100 ms);
//     a program that writes a file in pieces for longer is caught by the
//     ceiling and synced again when it finishes.
//   - ceiling 2 s: an editor that autosaves every second must still sync while
//     it keeps going (a pure trailing debounce never fires during a stream —
//     filex lesson #84), and at most every 2 s, not once per save.
//
// ⚠ The two sources keep SEPARATE clocks (lane). With one shared clock every
// browser save waited out the local quiet too — because the download that
// answered the PREVIOUS browser save had just raised file-system events of its
// own: measured 430 ms browser→disk with one clock, 200 ms with two.
const (
	liveRemoteQuiet = 150 * time.Millisecond
	liveLocalQuiet  = 400 * time.Millisecond
	liveMaxWait     = 2 * time.Second
	// liveRaceRetry is how soon a pass that stood down (the other side changed
	// mid-pass) is followed by the pass that keeps both versions — and
	// liveRaceRetries how many such follow-ups in a row before the pair is
	// left to the poll, so a file rewritten non-stop cannot spin the engine.
	liveRaceRetry   = time.Second
	liveRaceRetries = 3
	// liveStartupWait bounds how long the first full pass waits for the
	// stream's first answer, so the pass it would trigger and the startup pass
	// are one pass, not two.
	liveStartupWait = 3 * time.Second
)

// lane is one source of owed work (the server, or the local disk) with its own
// debounce clock.
type lane struct {
	set   bool
	full  bool
	dirs  map[string]bool
	first time.Time
	due   time.Time
	// cursor is the change-log cursor the planner took for a full pass it
	// asked for (see watchPlanner.ran); "" = take one when it runs.
	cursor string
}

// note records one more change: due moves to now+quiet, but never past the
// burst's ceiling.
func (ln *lane) note(now time.Time, quiet, maxWait time.Duration) {
	if !ln.set {
		*ln = lane{set: true, dirs: map[string]bool{}, first: now, due: now.Add(quiet)}
		return
	}
	due := now.Add(quiet)
	if ceiling := ln.first.Add(maxWait); due.After(ceiling) {
		due = ceiling
	}
	if due.After(ln.due) {
		ln.due = due
	}
}

// pendingWork is what is owed to one pair.
type pendingWork struct {
	remote lane
	local  lane
}

// liveLoop is the scheduler. Everything except the pass runner is plain data,
// so the scheduling rules are testable without a server or a disk.
type liveLoop struct {
	loadPairs func() ([]filesync.Pair, error)
	// runPass runs one pass: dirs == nil is a full pass.
	runPass func(ctx context.Context, p filesync.Pair, dirs []string) (filesync.Result, error)
	// localChanged reports whether a local folder really differs from the
	// baseline (the engine's LocalDirChanged).
	localChanged func(p filesync.Pair, dir string) bool
	// changes asks the server's change log (cliclient Changes); localFP walks
	// a pair's local side (filesync.LocalFingerprint). Both feed the planner;
	// nil changes = a server that cannot answer (always a timer).
	changes func(ctx context.Context, p filesync.Pair, since string) (string, bool, error)
	localFP func(p filesync.Pair) (string, error)
	// planner decides what the interval tick runs (watch.go). nil = a full
	// pass of every pair every interval, the pre-0.43 behaviour.
	planner *watchPlanner
	// window, when set, is the part of the day passes may run in (--window);
	// clock is the time of day it is judged against (nowFunc in production).
	window *syncWindow
	clock  func() time.Time
	// stream and local are optional (nil = pure polling).
	stream   liveStream
	local    localSource
	interval time.Duration
	out      io.Writer
	// errOut is where a pair's failures go (stderr in production); nil = out.
	errOut io.Writer
	now    func() time.Time
	// locker holds the lock of every pair this watcher syncs, for as long as
	// it syncs it (synclock.go). nil = no locks held here; each pass then
	// takes its own.
	locker pairLocker
	// lockGrace, lockRetryFast and lockRetry: see synclock.go; 0 = defaults.
	lockGrace, lockRetryFast, lockRetry time.Duration
	// localDown is set when there is no file-system watcher at all; every
	// pair is then reported as left to the full check.
	localDown error

	remoteQuiet, localQuiet, maxWait time.Duration
	raceRetry                        time.Duration

	mu sync.Mutex
	// pairs are the pairs this watcher syncs; wanted is every pair it was
	// asked to — pairs plus the ones another process holds (busy).
	pairs      []filesync.Pair
	wanted     []filesync.Pair
	busy       map[string]*busyPair // only touched by the loop's goroutine
	pend       map[string]*pendingWork
	raceCount  map[string]int
	reload     bool
	check      bool            // ask the planner about every pair now (after a reconnect)
	streamLive bool            // the stream's last state was "connected"
	pollOnly   map[string]bool // pairs the file-system watcher cannot cover
	fatal      error           // the token was refused: the loop stops with this
	kick       chan struct{}
	connected  chan struct{} // closed on the stream's first answer (live or not)
	firstOnce  sync.Once
}

// liveStream is the part of cliclient.ChangeStream the loop drives.
type liveStream interface {
	SetRoots(roots []string)
	Run(ctx context.Context)
}

// localSource is the part of localWatcher the loop drives.
type localSource interface {
	Sync(pairs []filesync.Pair)
	Run(ctx context.Context)
}

func (l *liveLoop) init() {
	if l.now == nil {
		l.now = time.Now
	}
	if l.clock == nil {
		l.clock = time.Now
	}
	if l.remoteQuiet == 0 {
		l.remoteQuiet = liveRemoteQuiet
	}
	if l.localQuiet == 0 {
		l.localQuiet = liveLocalQuiet
	}
	if l.maxWait == 0 {
		l.maxWait = liveMaxWait
	}
	if l.raceRetry == 0 {
		l.raceRetry = liveRaceRetry
	}
	if l.errOut == nil {
		l.errOut = l.out
	}
	if l.lockGrace == 0 {
		l.lockGrace = lockGrace
	}
	if l.lockRetryFast == 0 {
		l.lockRetryFast = lockRetryFast
	}
	if l.lockRetry == 0 {
		l.lockRetry = lockRetry
	}
	l.busy = map[string]*busyPair{}
	l.pend = map[string]*pendingWork{}
	l.raceCount = map[string]int{}
	l.pollOnly = map[string]bool{}
	l.kick = make(chan struct{}, 1)
	l.connected = make(chan struct{})
}

func (l *liveLoop) wake() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

// work returns the pending entry for a pair, creating it. Caller holds l.mu.
func (l *liveLoop) work(id string) *pendingWork {
	w := l.pend[id]
	if w == nil {
		w = &pendingWork{}
		l.pend[id] = w
	}
	return w
}

// noteRemote maps one server announcement onto the pairs it concerns.
func (l *liveLoop) noteRemote(tc cliclient.TreeChange) {
	l.mu.Lock()
	now := l.now()
	for _, p := range l.pairs {
		if p.WatchRoot() != tc.Root {
			continue
		}
		if p.File {
			// A single-file pair watches its parent folder recursively; only
			// the parent itself is about this file.
			hit := tc.Overflow
			for _, d := range tc.Dirs {
				hit = hit || d == ""
			}
			if hit {
				w := l.work(p.ID)
				w.remote.note(now, l.remoteQuiet, l.maxWait)
				w.remote.full = true
			}
			continue
		}
		w := l.work(p.ID)
		w.remote.note(now, l.remoteQuiet, l.maxWait)
		if tc.Overflow {
			w.remote.full = true
		}
		for _, d := range tc.Dirs {
			w.remote.dirs[d] = true
		}
	}
	l.mu.Unlock()
	l.wake()
}

// noteLocal records a file-system change in one pair's folder.
func (l *liveLoop) noteLocal(pairID, dir string) {
	l.mu.Lock()
	now := l.now()
	for _, p := range l.pairs {
		if p.ID != pairID {
			continue
		}
		w := l.work(p.ID)
		w.local.note(now, l.localQuiet, l.maxWait)
		if p.File {
			w.local.full = true
		} else {
			w.local.dirs[dir] = true
		}
	}
	l.mu.Unlock()
	l.wake()
}

// fullNow asks for a full pass of one pair, due at once. Caller holds l.mu.
func (l *liveLoop) fullNow(id string) {
	w := l.work(id)
	w.remote.note(l.now(), 0, l.maxWait)
	w.remote.full = true
	w.remote.due = l.now()
}

// dirsNow asks for a targeted pass of some folders of one pair, due at once.
// Caller holds l.mu.
func (l *liveLoop) dirsNow(id string, dirs []string) {
	w := l.work(id)
	w.remote.note(l.now(), 0, l.maxWait)
	for _, d := range dirs {
		w.remote.dirs[d] = true
	}
	w.remote.due = l.now()
}

// noteAll asks for a full pass of every pair, due now.
func (l *liveLoop) noteAll() {
	l.mu.Lock()
	for _, p := range l.pairs {
		l.fullNow(p.ID)
	}
	l.mu.Unlock()
	l.wake()
}

// checkAll asks the planner about every pair at once — what a (re)connected
// stream does: whatever changed while it was down was announced to nobody,
// and the change log says which pairs that was.
func (l *liveLoop) checkAll() {
	l.mu.Lock()
	l.check = true
	l.mu.Unlock()
	l.wake()
}

// notePairsFile schedules a re-read of pairs.json.
func (l *liveLoop) notePairsFile() {
	l.mu.Lock()
	l.reload = true
	l.mu.Unlock()
	l.wake()
}

// streamState prints the stream's state for people and for the desktop app,
// which parses `live: <connected|polling|offline>` into its status word.
func (l *liveLoop) streamState(st cliclient.StreamState, detail string) {
	fmt.Fprintf(l.out, "live: %s — %s\n", st, detail)
	l.mu.Lock()
	l.streamLive = st == cliclient.StreamLive
	l.mu.Unlock()
	l.firstOnce.Do(func() { close(l.connected) })
}

// localState prints, per pair, whether changes made on this computer are
// watched — `<pair>: local: poll-only — <code> — <detail>` when they cannot
// be and wait for the full check, `<pair>: local: watched` when a folder
// reported before is watched again. The desktop shows it under that folder
// (desktop/src/syncstatus.ts LocalNote). Only transitions are printed.
func (l *liveLoop) localState(pairID string, err error) {
	l.mu.Lock()
	if l.pollOnly != nil {
		l.pollOnly[pairID] = err != nil
	}
	l.mu.Unlock()
	switch {
	case err == nil:
		fmt.Fprintf(l.out, "%s: local: watched\n", pairID)
	case errors.Is(err, errTooLargeToWatch):
		fmt.Fprintf(l.out, "%s: local: poll-only — too-large — %s\n", pairID, strings.TrimPrefix(err.Error(), errTooLargeToWatch.Error()+": "))
	default:
		fmt.Fprintf(l.out, "%s: local: poll-only — unavailable — %v\n", pairID, err)
	}
}

// localWatched reports whether the file-system watcher covers a pair — the
// condition under which a fresh local walk after a targeted pass is safe
// (see watchPlanner.ranTargeted).
func (l *liveLoop) localWatched(id string) bool {
	if l.local == nil || l.localDown != nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.pollOnly[id]
}

// setFatal stops the loop with err (the first one wins).
func (l *liveLoop) setFatal(err error) {
	l.mu.Lock()
	if l.fatal == nil {
		l.fatal = err
	}
	l.mu.Unlock()
	l.wake()
}

func (l *liveLoop) fatalErr() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fatal
}

// signedOut turns a refused token into the watcher's exit (exitSignedOut);
// any other error is not the loop's to act on.
func (l *liveLoop) signedOut(err error) bool {
	if !cliclient.IsUnauthorized(err) {
		return false
	}
	l.setFatal(&exitError{code: exitSignedOut, err: errSignedOut})
	return true
}

// applyPairs installs a (re-)read pair list — the part of it this watcher can
// lock (claim, synclock.go).
func (l *liveLoop) applyPairs(wanted []filesync.Pair) {
	l.install(l.claim(wanted))
}

// install makes pairs the pairs this watcher syncs: roots for the stream,
// folders for the file-system watcher, and a full pass for every pair not
// seen before — or edited since (paused, moved, a hold confirmed or
// discarded), or just taken over from another process.
func (l *liveLoop) install(pairs []filesync.Pair) {
	l.mu.Lock()
	known := map[string]filesync.Pair{}
	for _, p := range l.pairs {
		known[p.ID] = p
	}
	l.pairs = pairs
	ids := map[string]bool{}
	var fresh []string
	for _, p := range pairs {
		ids[p.ID] = true
		old, seen := known[p.ID]
		if !seen || !samePairConfig(old, p) {
			l.fullNow(p.ID)
		}
		if !seen {
			fresh = append(fresh, p.ID)
		}
	}
	for id := range l.pend {
		if !ids[id] {
			delete(l.pend, id)
		}
	}
	l.mu.Unlock()
	if l.planner != nil {
		l.planner.forget(ids)
	}
	if l.localDown != nil {
		for _, id := range fresh {
			l.localState(id, l.localDown)
		}
	}

	if l.stream != nil {
		seen := map[string]bool{}
		var roots []string
		for _, p := range pairs {
			if r := p.WatchRoot(); !seen[r] {
				seen[r] = true
				roots = append(roots, r)
			}
		}
		l.stream.SetRoots(roots)
	}
	if l.local != nil {
		l.local.Sync(pairs)
	}
	l.wake()
}

func (l *liveLoop) reloadPairs() error {
	pairs, err := l.loadPairs()
	if err != nil {
		return fmt.Errorf("re-read pairs: %w", err)
	}
	l.applyPairs(pairs)
	return nil
}

// tick is the interval's (or a reconnect's) safety net: the planner decides,
// pair by pair, whether anything is owed. askLog forces the change-log
// question even while the stream says it is connected (a reconnect: what
// happened while it was down was announced to nobody).
func (l *liveLoop) tick(ctx context.Context, askLog bool) {
	l.mu.Lock()
	pairs := append([]filesync.Pair(nil), l.pairs...)
	live := l.streamLive && !askLog
	l.mu.Unlock()
	if l.planner == nil {
		l.noteAll()
		return
	}
	now := l.now()
	for _, p := range pairs {
		if ctx.Err() != nil {
			return
		}
		p := p
		localFP := func() (string, error) { return l.fingerprint(p) }
		changes := func(since string) (string, bool, error) { return l.askChanges(ctx, p, since) }
		var d watchDecision
		var err error
		if live {
			d, err = l.planner.liveTick(p, now, localFP, changes)
		} else {
			d, err = l.planner.decide(p, now, localFP, changes)
		}
		if err != nil {
			l.signedOut(err)
			return
		}
		if !d.run {
			continue
		}
		l.mu.Lock()
		if d.dirs == nil {
			l.fullNow(p.ID)
			l.work(p.ID).remote.cursor = d.cursor
		} else {
			l.dirsNow(p.ID, d.dirs)
		}
		l.mu.Unlock()
	}
	l.wake()
}

func (l *liveLoop) fingerprint(p filesync.Pair) (string, error) {
	if l.localFP == nil {
		return "", nil
	}
	return l.localFP(p)
}

func (l *liveLoop) askChanges(ctx context.Context, p filesync.Pair, since string) (string, bool, error) {
	if l.changes == nil {
		return "", false, cliclient.ErrChangesUnsupported
	}
	return l.changes(ctx, p, since)
}

// readyWork is one pair's work that fell due.
type readyWork struct {
	pair   filesync.Pair
	full   bool
	cursor string
	remote map[string]bool
	local  map[string]bool
}

// take removes and returns the work that is due, per pair in pair order, and
// the time the next not-yet-due work falls due. A lane that is not due yet
// stays pending — unless a full pass is going to run, which covers it.
func (l *liveLoop) take() (ready []readyWork, next time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	later := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, p := range l.pairs {
		w := l.pend[p.ID]
		if w == nil {
			continue
		}
		rDue := w.remote.set && !w.remote.due.After(now)
		lDue := w.local.set && !w.local.due.After(now)
		if !rDue && !lDue {
			if w.remote.set {
				later(w.remote.due)
			}
			if w.local.set {
				later(w.local.due)
			}
			continue
		}
		r := readyWork{pair: p, full: (rDue && w.remote.full) || (lDue && w.local.full)}
		if rDue {
			r.remote = w.remote.dirs
			r.cursor = w.remote.cursor
			w.remote = lane{}
		}
		if lDue {
			r.local = w.local.dirs
			w.local = lane{}
		}
		if r.full {
			*w = pendingWork{}
		}
		if !w.remote.set && !w.local.set {
			delete(l.pend, p.ID)
		} else {
			if w.remote.set {
				later(w.remote.due)
			}
			if w.local.set {
				later(w.local.due)
			}
		}
		ready = append(ready, r)
	}
	return ready, next
}

// execute runs one pair's due work.
func (l *liveLoop) execute(ctx context.Context, r readyWork) {
	p := r.pair
	var dirs []string
	if !r.full {
		for d := range r.remote {
			dirs = append(dirs, d)
		}
		for d := range r.local {
			// ⚠ The engine's own writes raise file-system events; a folder the
			// disk shows unchanged against the baseline costs no request.
			if !r.remote[d] && l.localChanged(p, d) {
				dirs = append(dirs, d)
			}
		}
		if len(dirs) == 0 {
			return
		}
		sort.Strings(dirs)
	}
	cursor := r.cursor
	if r.full && cursor == "" && l.planner != nil {
		// Taken BEFORE the pass: a change made while it runs is newer than
		// this cursor, and the next question about it finds it.
		cur, _, err := l.askChanges(ctx, p, "")
		if l.signedOut(err) {
			return
		}
		if err == nil {
			cursor = cur
		}
	}
	res, err := l.runPass(ctx, p, dirs)
	if l.signedOut(err) {
		return
	}
	if ctx.Err() != nil {
		// Cut short (the watcher is stopping, or the sync window closed): not
		// recorded, so the planner owes the pass again.
		return
	}
	if l.planner != nil {
		if r.full {
			fp := res.LocalFingerprint
			if fp == "" {
				fp, _ = l.fingerprint(p)
			}
			l.planner.ran(p, l.now(), cursor, res, err, fp)
		} else {
			fp := ""
			if l.localWatched(p.ID) {
				fp, _ = l.fingerprint(p)
			}
			l.planner.ranTargeted(p, l.now(), dirs, res, err, fp)
		}
	}
	if r.full && l.local != nil {
		// A pair's first pass may have just created its local folder; the
		// watcher retries whatever it could not watch before.
		l.mu.Lock()
		pairs := append([]filesync.Pair(nil), l.pairs...)
		l.mu.Unlock()
		l.local.Sync(pairs)
	}

	raced := len(res.Raced) > 0
	l.mu.Lock()
	if raced && l.raceCount[p.ID] < liveRaceRetries {
		// The other side moved while this pass ran; the pass that keeps both
		// versions should follow now, not on the next poll.
		l.raceCount[p.ID]++
		w := l.work(p.ID)
		w.remote.note(l.now(), l.raceRetry, l.raceRetry)
		if r.full {
			w.remote.full = true
		}
		for _, d := range dirs {
			w.remote.dirs[d] = true
		}
	} else if !raced {
		l.raceCount[p.ID] = 0
	}
	l.mu.Unlock()
}

// requeue puts taken work back, due at once: a pass the sync window cut short
// is still owed, and so is every pass that had not started yet.
func (l *liveLoop) requeue(r readyWork) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.full {
		l.fullNow(r.pair.ID)
		l.work(r.pair.ID).remote.cursor = r.cursor
		return
	}
	var dirs []string
	for d := range r.remote {
		dirs = append(dirs, d)
	}
	for d := range r.local {
		dirs = append(dirs, d)
	}
	if len(dirs) > 0 {
		l.dirsNow(r.pair.ID, dirs)
	}
}

// windowOpen reports whether passes may run now (always, without a window).
func (l *liveLoop) windowOpen() bool {
	return l.window == nil || l.window.contains(l.clock())
}

// passCtx is the context one pass runs in: it ends when the sync window does,
// so a pass still busy then stops like a Ctrl-C (its ledger written) and
// carries on in the next window.
func (l *liveLoop) passCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if l.window == nil {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, l.window.closesAfter(l.clock()))
}

// Run drives everything until ctx ends.
func (l *liveLoop) Run(ctx context.Context) error {
	l.init()
	if l.locker != nil {
		// The OS lets go when the process ends anyway; this is for a watcher
		// that stops inside a process that goes on (tests, and whatever
		// embeds the loop next).
		defer l.locker.releaseAll()
	}
	pairs, err := l.loadPairs()
	if err != nil {
		return err
	}
	if l.local != nil {
		go l.local.Run(ctx)
	}
	l.applyPairs(pairs) // roots + watches; every pair owes a first full pass

	// The stream runs only while passes may: outside a --window nothing talks
	// to the server at all.
	var stopStream context.CancelFunc
	startStream := func() {
		if l.stream == nil || stopStream != nil {
			return
		}
		sctx, cancel := context.WithCancel(ctx)
		stopStream = cancel
		go l.stream.Run(sctx)
	}
	defer func() {
		if stopStream != nil {
			stopStream()
		}
	}()
	waiting := false
	if l.windowOpen() {
		startStream()
		if l.stream != nil {
			// Let the stream answer first: its "connected" asks about exactly
			// the pairs the startup is about to walk anyway, and they must be
			// the same pass.
			select {
			case <-l.connected:
			case <-time.After(liveStartupWait):
			case <-ctx.Done():
				return nil
			}
		}
	}
	if l.stream == nil {
		fmt.Fprintf(l.out, "live: polling — changes are found by the interval poll only\n")
	}

	nextPoll := l.now().Add(l.interval)
	for {
		if err := l.fatalErr(); err != nil {
			return err
		}
		if !l.windowOpen() {
			// Changes keep arriving from the local file system and wait; the
			// stream is down until the window opens.
			if !waiting {
				fmt.Fprintf(l.out, "sync: waiting for the sync window %s\n", l.window)
				waiting = true
			}
			if stopStream != nil {
				stopStream()
				stopStream = nil
			}
			wait := l.window.opensAfter(l.clock()).Sub(l.clock())
			if wait > l.interval || wait <= 0 {
				wait = l.interval
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
		if waiting {
			waiting = false
			startStream()
			l.checkAll()
		}

		ready, next := l.take()
		for i, r := range ready {
			if ctx.Err() != nil {
				return nil
			}
			pctx, cancel := l.passCtx(ctx)
			l.execute(pctx, r)
			cut := pctx.Err() != nil && ctx.Err() == nil
			cancel()
			if cut {
				fmt.Fprintf(l.out, "sync: the sync window %s closed; the rest continues when it opens\n", l.window)
				// This pass and every one not started yet are still owed.
				for _, rest := range ready[i:] {
					l.requeue(rest)
				}
				break
			}
			if l.fatalErr() != nil {
				break
			}
		}
		if len(ready) > 0 {
			continue // anything that arrived during those passes is due now
		}

		l.mu.Lock()
		reload := l.reload
		l.reload = false
		check := l.check
		l.check = false
		l.mu.Unlock()
		if reload {
			if err := l.reloadPairs(); err != nil {
				return err
			}
			continue
		}
		if check {
			l.tick(ctx, true)
			continue
		}

		now := l.now()
		lockDue := l.lockDue()
		if !lockDue.IsZero() && !now.Before(lockDue) {
			// A pair another process holds is due for another try.
			l.retryLocks()
			continue
		}
		if !now.Before(nextPoll) {
			// pairs.json is edited by OTHER processes — the desktop app
			// writes it while this watcher runs. Re-read it every lap, as
			// the plain poll always did, even where a file-system watch on
			// it is also in place.
			if err := l.reloadPairs(); err != nil {
				return err
			}
			l.tick(ctx, false)
			nextPoll = l.now().Add(l.interval)
			continue
		}
		wait := nextPoll.Sub(now)
		if !next.IsZero() && next.Sub(now) < wait {
			wait = next.Sub(now)
		}
		if !lockDue.IsZero() && lockDue.Sub(now) < wait {
			wait = lockDue.Sub(now)
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-l.kick:
			timer.Stop()
		case <-timer.C:
		}
	}
}

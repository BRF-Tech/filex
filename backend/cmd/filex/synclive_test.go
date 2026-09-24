package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// passRec is one pass the loop ran.
type passRec struct {
	pair string
	dirs []string // nil = full pass
	at   time.Time
}

// fakeStream plays the server's change stream: it reports "connected" at once
// (as a healthy server does) and records the roots it was asked to watch.
type fakeStream struct {
	mu    sync.Mutex
	roots []string
	loop  *liveLoop
	// onRun, when set, is told the stream was started.
	onRun func()
}

func (f *fakeStream) SetRoots(r []string) {
	f.mu.Lock()
	f.roots = append([]string(nil), r...)
	f.mu.Unlock()
}

func (f *fakeStream) Run(ctx context.Context) {
	if f.onRun != nil {
		f.onRun()
	}
	f.loop.streamState(cliclient.StreamLive, "watching")
	f.loop.noteAll()
	<-ctx.Done()
}

type loopRig struct {
	t      *testing.T
	mu     sync.Mutex // guards pairs, raced, local — the loop reads them from its goroutine
	loop   *liveLoop
	passes chan passRec
	pairs  []filesync.Pair
	cancel context.CancelFunc
	done   chan struct{}
	raced  func(p filesync.Pair) bool
	local  func(p filesync.Pair, dir string) bool
	out    *syncBuffer // written by the loop's goroutines, read by the test
}

func newLoopRig(t *testing.T, pairs []filesync.Pair, interval time.Duration) *loopRig {
	r := &loopRig{t: t, passes: make(chan passRec, 100), pairs: pairs, out: &syncBuffer{}}
	r.raced = func(filesync.Pair) bool { return false }
	r.local = func(filesync.Pair, string) bool { return true }
	r.loop = &liveLoop{
		loadPairs: func() ([]filesync.Pair, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]filesync.Pair(nil), r.pairs...), nil
		},
		runPass: func(_ context.Context, p filesync.Pair, dirs []string) (filesync.Result, error) {
			r.passes <- passRec{pair: p.ID, dirs: dirs, at: time.Now()}
			r.mu.Lock()
			f := r.raced
			r.mu.Unlock()
			var res filesync.Result
			if f(p) {
				res.Raced = []string{"download x: changed on this computer while it was being synced"}
			}
			return res, nil
		},
		localChanged: func(p filesync.Pair, dir string) bool {
			r.mu.Lock()
			f := r.local
			r.mu.Unlock()
			return f(p, dir)
		},
		interval:    interval,
		out:         r.out,
		remoteQuiet: 20 * time.Millisecond,
		localQuiet:  40 * time.Millisecond,
		maxWait:     120 * time.Millisecond,
		raceRetry:   30 * time.Millisecond,
	}
	r.loop.stream = &fakeStream{loop: r.loop}
	return r
}

func (r *loopRig) set(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn()
}

func (r *loopRig) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		_ = r.loop.Run(ctx)
	}()
	r.t.Cleanup(func() { cancel(); <-r.done })
}

func (r *loopRig) next(within time.Duration) passRec {
	r.t.Helper()
	select {
	case p := <-r.passes:
		return p
	case <-time.After(within):
		r.t.Fatalf("no pass within %s", within)
		return passRec{}
	}
}

func (r *loopRig) none(within time.Duration) {
	r.t.Helper()
	select {
	case p := <-r.passes:
		r.t.Fatalf("unexpected pass %+v", p)
	case <-time.After(within):
	}
}

var folderPair = filesync.Pair{ID: "p1", Local: "/tmp/x", Remote: "docs://proj"}

// Startup: one full pass — the stream's "connected" asks for the same full
// pass the startup owes, and they must be ONE pass, not two.
func TestLiveStartupIsOneFullPass(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	if p := r.next(time.Second); p.dirs != nil {
		t.Fatalf("startup must be a full pass: %+v", p)
	}
	r.none(150 * time.Millisecond)
	if !strings.Contains(r.out.String(), "live: connected") {
		t.Fatalf("the state line the desktop parses is missing: %q", r.out.String())
	}
}

// A server announcement → a pass of exactly that folder, after the short
// remote quiet, not on the next poll.
func TestLiveRemoteChangeRunsThatFolderAtOnce(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	r.next(time.Second)

	sent := time.Now()
	r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{"a/b"}})
	p := r.next(time.Second)
	if len(p.dirs) != 1 || p.dirs[0] != "a/b" {
		t.Fatalf("want a pass of a/b, got %+v", p)
	}
	if lag := p.at.Sub(sent); lag > 200*time.Millisecond {
		t.Fatalf("the pass took %s to start", lag)
	}
}

// A burst of announcements for one pair is ONE pass naming every folder.
func TestLiveBurstIsOnePass(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	r.next(time.Second)
	for _, d := range []string{"a", "b", "a", "c"} {
		r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{d}})
	}
	p := r.next(time.Second)
	if strings.Join(p.dirs, ",") != "a,b,c" {
		t.Fatalf("want one pass of a,b,c, got %+v", p)
	}
	r.none(100 * time.Millisecond)
}

// An editor autosaving faster than the quiet window must not starve the
// pair: the ceiling forces a pass while the saves keep coming — and the
// passes stay bounded (no storm of one pass per save).
func TestLiveContinuousChangesAreSyncedWithoutAStorm(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	r.next(time.Second)

	stop := time.After(600 * time.Millisecond)
	tick := time.NewTicker(10 * time.Millisecond) // "saves" every 10 ms, quiet is 20 ms
	defer tick.Stop()
	saves := 0
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-tick.C:
			saves++
			r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{""}})
		}
	}
	got := 0
drain:
	for {
		select {
		case <-r.passes:
			got++
		case <-time.After(250 * time.Millisecond):
			break drain
		}
	}
	if got < 3 {
		t.Fatalf("a steady stream of saves must still sync while it lasts: %d passes for %d saves", got, saves)
	}
	if got > saves/3 {
		t.Fatalf("storm: %d passes for %d saves", got, saves)
	}
}

// The engine's own downloads raise file-system events. When the disk shows
// the folder unchanged against the baseline, no pass (and no request) runs.
func TestLiveOwnWritesCostNothing(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.set(func() { r.local = func(filesync.Pair, string) bool { return false } })
	r.start()
	r.next(time.Second)
	r.loop.noteLocal("p1", "docs")
	r.none(150 * time.Millisecond)

	r.set(func() { r.local = func(filesync.Pair, string) bool { return true } })
	r.loop.noteLocal("p1", "docs")
	if p := r.next(time.Second); len(p.dirs) != 1 || p.dirs[0] != "docs" {
		t.Fatalf("a real local change must run that folder: %+v", p)
	}
}

// A single-file pair watches its folder; only a change IN that folder is
// about the file, and it is answered with the file pair's own pass.
func TestLiveFilePairListensToItsFolderOnly(t *testing.T) {
	fp := filesync.Pair{ID: "f1", Local: "/tmp/f/a.txt", Remote: "docs://proj/a.txt", File: true}
	r := newLoopRig(t, []filesync.Pair{fp}, time.Hour)
	r.start()
	r.next(time.Second)
	r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{"sub"}})
	r.none(100 * time.Millisecond)
	r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{""}})
	if p := r.next(time.Second); p.pair != "f1" || p.dirs != nil {
		t.Fatalf("want the file pair's pass, got %+v", p)
	}
	fs := r.loop.stream.(*fakeStream)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.roots) != 1 || fs.roots[0] != "docs://proj" {
		t.Fatalf("a file pair must watch its folder: %v", fs.roots)
	}
}

// Too many folders changed to name them: a full pass.
func TestLiveOverflowIsAFullPass(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	r.next(time.Second)
	r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Overflow: true})
	if p := r.next(time.Second); p.dirs != nil {
		t.Fatalf("overflow must be a full pass: %+v", p)
	}
}

// The interval poll stays: a full pass of every pair each interval.
func TestLivePollStillRuns(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, 150*time.Millisecond)
	r.start()
	r.next(time.Second)
	if p := r.next(time.Second); p.dirs != nil {
		t.Fatalf("the poll must be a full pass: %+v", p)
	}
}

// A pass that stood down because the other side moved is followed at once by
// the pass that keeps both versions — a bounded number of times.
func TestLiveRacedPassIsFollowedUpButNotForever(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	r.next(time.Second)
	r.set(func() { r.raced = func(filesync.Pair) bool { return true } })
	r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{"x"}})
	r.next(time.Second)
	for i := 0; i < liveRaceRetries; i++ {
		if p := r.next(time.Second); len(p.dirs) != 1 || p.dirs[0] != "x" {
			t.Fatalf("follow-up %d: %+v", i, p)
		}
	}
	r.none(200 * time.Millisecond)
}

// A pair added while the watcher runs (the desktop app's "keep on this
// computer") gets its first pass as soon as pairs.json changes — and its
// folder is added to the stream's roots.
func TestLiveNewPairStartsWithoutWaitingForThePoll(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.start()
	r.next(time.Second)
	r.set(func() { r.pairs = append(r.pairs, filesync.Pair{ID: "p2", Local: "/tmp/y", Remote: "docs://other"}) })
	r.loop.notePairsFile()
	if p := r.next(time.Second); p.pair != "p2" || p.dirs != nil {
		t.Fatalf("the new pair's first pass: %+v", p)
	}
	fs := r.loop.stream.(*fakeStream)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	roots := append([]string(nil), fs.roots...)
	sort.Strings(roots)
	if strings.Join(roots, ",") != "docs://other,docs://proj" {
		t.Fatalf("roots = %v", fs.roots)
	}
}

// ⚠ Browser saves must not wait for the local quiet. The download that
// answered the PREVIOUS browser save raised file-system events of its own; with
// one shared clock every following browser save waited them out (measured
// 430 ms browser→disk, against 200 ms with a clock per source).
func TestLiveRemoteIsNotHeldBackByPendingLocalEvents(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.loop.localQuiet = 400 * time.Millisecond
	r.set(func() { r.local = func(filesync.Pair, string) bool { return false } })
	r.start()
	r.next(time.Second)

	r.loop.noteLocal("p1", "") // our own download's events, still settling
	sent := time.Now()
	r.loop.noteRemote(cliclient.TreeChange{Root: "docs://proj", Dirs: []string{""}})
	p := r.next(time.Second)
	if lag := p.at.Sub(sent); lag > 200*time.Millisecond {
		t.Fatalf("the browser save waited %s behind unrelated local events", lag)
	}
	if len(p.dirs) != 1 || p.dirs[0] != "" {
		t.Fatalf("pass = %+v", p)
	}
	r.none(600 * time.Millisecond) // the local lane settles into "unchanged": no pass
}

// With no file-system watcher at all, every pair says so under its own folder
// — a pair added later included.
func TestLiveNoWatcherIsReportedPerPair(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.loop.localDown = errors.New("too many open files")
	r.start()
	r.next(time.Second)
	r.set(func() { r.pairs = append(r.pairs, filesync.Pair{ID: "p2", Local: "/tmp/y", Remote: "docs://other"}) })
	r.loop.notePairsFile()
	r.next(time.Second)
	for _, id := range []string{"p1", "p2"} {
		want := id + ": local: poll-only — unavailable — too many open files\n"
		if !strings.Contains(r.out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, r.out.String())
		}
	}
}

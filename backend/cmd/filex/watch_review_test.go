package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// The watcher's safety net, as merged with the live paths (v0.43.0 + PR #35).

// A pair whose last pass failed used to stop asking the change log until its
// retry was due — so a server edit waited out the whole back-off (up to
// --watch-max) behind one broken file.
func TestWatch_AFailingPairStillHearsTheChangeLog(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.9", changed: true}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 1, "upload a.txt: HTTP 423")

	d, err := w.decide(p, t0.Add(30*time.Second), fixedFP("fp1"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run, "a change on the server must not wait for the retry")
	require.Equal(t, "server change", d.why)
}

// A failed pass retries the FOLDERS that failed, not the whole tree: one
// locked file used to cost a full walk every --watch-max for as long as it
// stayed locked — most of the load the change log exists to remove.
func TestWatch_ARetryIsTheFailedFoldersOnly(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	p := filesync.Pair{ID: "p"}
	w.ran(p, t0, "e.1", filesync.Result{Planned: 3, Errors: []string{"upload a/x.txt: HTTP 423"}, Retry: []string{"a"}}, nil, "fp1")

	d, err := w.decide(p, t0.Add(time.Minute), fixedFP("fp1"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run)
	require.Equal(t, []string{"a"}, d.dirs, "a targeted retry of the folder that failed")

	// It still fails: the gap grows.
	w.ranTargeted(p, t0.Add(time.Minute), d.dirs, filesync.Result{Errors: []string{"x"}, Retry: []string{"a"}}, nil, "")
	d, _ = w.decide(p, t0.Add(2*time.Minute), fixedFP("fp1"), f.ask)
	require.False(t, d.run, "the second retry waits longer than the first")
	d, _ = w.decide(p, t0.Add(3*time.Minute), fixedFP("fp1"), f.ask)
	require.True(t, d.run)

	// It goes through: nothing is owed any more.
	w.ranTargeted(p, t0.Add(3*time.Minute), d.dirs, filesync.Result{}, nil, "")
	d, _ = w.decide(p, t0.Add(10*time.Minute), fixedFP("fp1"), f.ask)
	require.False(t, d.run, "a clean retry clears the debt")
}

// While the live stream is connected the tick does not ask "did anything
// change?" — the stream already said — it only keeps a few recent cursors, and
// a reconnect asks about the OLDEST of them.
func TestWatch_WhileLiveTheTickOnlyKeepsCursors(t *testing.T) {
	var asked []string
	cursors := []string{"e.2", "e.3", "e.4", "e.5"}
	ask := func(since string) (string, bool, error) {
		asked = append(asked, since)
		if since == "" {
			c := cursors[0]
			cursors = cursors[1:]
			return c, true, nil
		}
		return "e.9", since != "e.9", nil
	}
	w := newPlanner()
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 0)
	for i := 1; i <= 4; i++ {
		d, err := w.liveTick(p, t0.Add(time.Duration(i)*30*time.Second), fixedFP("fp1"), ask)
		require.NoError(t, err)
		require.False(t, d.run)
	}
	require.Equal(t, []string{"", "", "", ""}, asked, "live ticks only take cursors")
	require.Equal(t, []string{"e.3", "e.4", "e.5"}, w.state["p"].cursors, "the last few are kept")

	asked = nil
	d, err := w.decide(p, t0.Add(3*time.Minute), fixedFP("fp1"), ask)
	require.NoError(t, err)
	require.Equal(t, []string{"e.3"}, asked, "a reconnect asks about the oldest cursor kept")
	require.True(t, d.run)
}

// ── the loop ──

// With the planner in place the interval no longer walks a quiet pair: it asks
// the change log (the stream is not connected in this rig), and walks only
// when that says something changed.
func TestLiveTickAsksInsteadOfWalking(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, 60*time.Millisecond)
	r.loop.stream = nil
	r.loop.planner = newWatchPlanner(60*time.Millisecond, time.Hour, time.Hour)
	r.loop.localFP = func(filesync.Pair) (string, error) { return "fp", nil }
	var mu sync.Mutex
	changed := false
	questions := 0
	r.loop.changes = func(_ context.Context, _ filesync.Pair, since string) (string, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		questions++
		if since == "" {
			return "e.1", true, nil
		}
		return "e.1", changed, nil
	}
	r.loop.runPass = func(_ context.Context, p filesync.Pair, dirs []string) (filesync.Result, error) {
		r.passes <- passRec{pair: p.ID, dirs: dirs, at: time.Now()}
		return filesync.Result{LocalFingerprint: "fp"}, nil
	}
	r.start()
	if p := r.next(time.Second); p.dirs != nil {
		t.Fatalf("startup must be a full pass: %+v", p)
	}
	r.none(400 * time.Millisecond) // several ticks: asked, nothing changed, no walk
	mu.Lock()
	asked := questions
	changed = true
	mu.Unlock()
	if asked < 3 {
		t.Fatalf("the ticks did not ask the change log (%d question(s))", asked)
	}
	if p := r.next(time.Second); p.dirs != nil {
		t.Fatalf("a change the log reports is a full pass: %+v", p)
	}
}

// A refused token ends the loop with exit status 3 — from a pass, or from
// the change stream's ticket.
func TestLiveRefusedTokenEndsTheWatcher(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, time.Hour)
	r.loop.runPass = func(context.Context, filesync.Pair, []string) (filesync.Result, error) {
		return filesync.Result{}, &cliclient.APIError{Status: 401, Message: "unauthorized"}
	}
	done := make(chan error, 1)
	go func() { done <- r.loop.Run(context.Background()) }()
	select {
	case err := <-done:
		var ee *exitError
		require.True(t, errors.As(err, &ee), "got %v", err)
		require.Equal(t, exitSignedOut, ee.code)
	case <-time.After(3 * time.Second):
		t.Fatal("the loop kept running on a refused token")
	}
}

// Outside the sync window nothing talks to the server — not a pass, and not
// the change stream either — and it says so once.
func TestLiveOutsideTheWindowNothingStarts(t *testing.T) {
	r := newLoopRig(t, []filesync.Pair{folderPair}, 20*time.Millisecond)
	win, err := parseSyncWindow("22:00-07:00")
	require.NoError(t, err)
	r.loop.window = win
	r.loop.clock = func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local) }
	var started atomic.Bool
	r.loop.stream = &fakeStream{loop: r.loop, onRun: func() { started.Store(true) }}
	r.start()
	r.none(300 * time.Millisecond)
	require.False(t, started.Load(), "the change stream was started outside the window")
	require.Equal(t, 1, strings.Count(r.out.String(), "sync: waiting for the sync window 22:00-07:00"))
}

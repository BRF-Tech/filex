package sync

import (
	"context"
	"sort"
	gosync "sync"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/realtime"
)

type recordingEmitter struct {
	mu     gosync.Mutex
	events []string
}

func (e *recordingEmitter) EmitChange(storageID int64, dir string, ev realtime.ChangeEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, dir+"|"+ev.Action)
}

func (e *recordingEmitter) dirs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := append([]string(nil), e.events...)
	sort.Strings(out)
	return out
}

type countingRecompute struct {
	mu    gosync.Mutex
	calls map[int64]int
	delay time.Duration
}

func (c *countingRecompute) fn(_ context.Context, id int64) error {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[int64]int{}
	}
	c.calls[id]++
	return nil
}

func (c *countingRecompute) n(id int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[id]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Issue #27: a change through the wrapped emitter must lead to a recompute of
// that storage's folder sizes, and the refreshed folder plus every ancestor
// must be told to reload.
func TestSizeRefresher_ChangeRecomputesAndRefreshesAncestors(t *testing.T) {
	rec := &countingRecompute{}
	hub := &recordingEmitter{}
	r := NewSizeRefresher(rec.fn, hub, 20*time.Millisecond, 200*time.Millisecond)
	emit := r.Wrap(hub)

	emit.EmitChange(7, "/photos/2026", realtime.ChangeEvent{Action: "move", Name: "a.jpg"})

	waitFor(t, "recompute", func() bool { return rec.n(7) == 1 })
	waitFor(t, "ancestor refresh", func() bool { return len(hub.dirs()) == 4 })
	got := hub.dirs()
	want := []string{"/photos/2026|move", "photos/2026|modify", "photos|modify", "|modify"}
	sort.Strings(want)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

// A burst of changes is one recompute, not one per change.
func TestSizeRefresher_BurstCoalesces(t *testing.T) {
	rec := &countingRecompute{}
	r := NewSizeRefresher(rec.fn, nil, 40*time.Millisecond, time.Second)
	for i := 0; i < 50; i++ {
		r.Touch(3, "inbox")
	}
	waitFor(t, "recompute", func() bool { return rec.n(3) == 1 })
	time.Sleep(120 * time.Millisecond)
	if n := rec.n(3); n != 1 {
		t.Fatalf("recomputes = %d, want 1 for one burst", n)
	}
}

// lesson #84: a steady stream must not starve the recompute. Changes every
// 10 ms with a 50 ms quiet gap would postpone a plain debounce forever; the
// ceiling makes it run while the stream is still going.
func TestSizeRefresher_SteadyStreamHitsTheCeiling(t *testing.T) {
	rec := &countingRecompute{}
	r := NewSizeRefresher(rec.fn, nil, 50*time.Millisecond, 150*time.Millisecond)
	stop := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(stop) {
		r.Touch(9, "upload")
		time.Sleep(10 * time.Millisecond)
	}
	if n := rec.n(9); n < 2 {
		t.Fatalf("recomputes during a 600 ms stream = %d, want at least 2 (ceiling 150 ms)", n)
	}
}

// A change that arrives while a recompute is walking the storage runs it once
// more afterwards — the walk may already have read the rows that changed.
func TestSizeRefresher_ChangeDuringRecomputeRunsAgain(t *testing.T) {
	rec := &countingRecompute{delay: 80 * time.Millisecond}
	r := NewSizeRefresher(rec.fn, nil, 10*time.Millisecond, 100*time.Millisecond)
	r.Touch(5, "a")
	time.Sleep(40 * time.Millisecond) // the recompute is now sleeping inside fn
	r.Touch(5, "b")
	waitFor(t, "second recompute", func() bool { return rec.n(5) == 2 })
}

// Storages are independent.
func TestSizeRefresher_StoragesAreSeparate(t *testing.T) {
	rec := &countingRecompute{}
	r := NewSizeRefresher(rec.fn, nil, 10*time.Millisecond, 100*time.Millisecond)
	r.Touch(1, "")
	r.Touch(2, "x")
	waitFor(t, "both storages", func() bool { return rec.n(1) == 1 && rec.n(2) == 1 })
}

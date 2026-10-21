package rowgate

// Issue #127: a change that WAITS for the gate comes before the next
// judgement, not only a change that holds it. A storage scan lets go of the
// gate after one directory and asks for it again at once for the next; a
// change that was woken when the gate opened lost that race nearly every
// time, and on a local storage of tens of thousands of entries the operations
// queue's delete sat "Queued" for the whole scan.
//
// These tests ask only what the gate promised before the fix as well: they
// fail on the gate of 0.57.4.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Every test uses storage ids of its own (9_300_0xx): the gates are
// process-wide.

// scanLoop is a storage scan as it judges a storage: it takes the gate for one
// directory (hold), lets go of it and asks for the next one at once, until the
// test ends. It answers how many directories it has judged.
func scanLoop(t *testing.T, id int64, hold time.Duration) *atomic.Int64 {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			release, err := JudgeWithin(ctx, id, 30*time.Second)
			if err != nil {
				continue
			}
			n.Add(1)
			time.Sleep(hold)
			release()
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return &n
}

// waitFor waits until n has passed was, at most d.
func waitFor(t *testing.T, n *atomic.Int64, was int64, d time.Duration, why string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for n.Load() <= was {
		if time.Now().After(deadline) {
			t.Fatal(why)
		}
		time.Sleep(time.Millisecond)
	}
}

// The scan lets go of a directory and asks for the next one on the same
// goroutine, before the change woken by the release has run: the change gets
// the gate first all the same, and the scan's next directory waits for it.
//
// Break: in open, leave g.waiting out - the scan's next judgement takes the
// gate ahead of the change that waited for it.
func TestAJudgementDoesNotBargeAheadOfAWaitingChange(t *testing.T) {
	const id = 9_300_001
	directory := Judge(id)
	moved := make(chan func(), 1)
	go func() {
		release, err := MoveCtx(context.Background(), id)
		if err == nil {
			moved <- release
		}
	}()
	time.Sleep(50 * time.Millisecond) // the change waits for the directory
	directory()
	if next, ok := TryJudge(id); ok {
		next()
		t.Fatal("the scan's next judgement took the gate ahead of the change that waited for it")
	}
	var release func()
	select {
	case release = <-moved:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting change never got the gate")
	}
	judged := make(chan func(), 1)
	go func() {
		r, err := JudgeWithin(context.Background(), id, 5*time.Second)
		if err == nil {
			judged <- r
		}
	}()
	select {
	case r := <-judged:
		r()
		release()
		t.Fatal("a judgement took the gate while a change held it")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case r := <-judged:
		r()
	case <-time.After(5 * time.Second):
		t.Fatal("the scan's next directory never got the gate once the change let go")
	}
}

// Under a scan that takes the gate back the moment it lets go, every change
// that waits for it - in MoveCtx, FenceCtx or Await - gets through within a
// directory or so, and the scan goes on between them.
//
// Break: as above - each change waits until the test gives up on it (the
// scan wins every release).
func TestAWaitingChangeGetsThroughAScanThatTakesTheGateBackAtOnce(t *testing.T) {
	const id = 9_300_002
	n := scanLoop(t, id, 2*time.Millisecond)
	waitFor(t, n, 0, 5*time.Second, "the scan never started")
	ask := map[string]func(ctx context.Context) (func(), error){
		"MoveCtx": func(ctx context.Context) (func(), error) { return MoveCtx(ctx, id) },
		"FenceCtx": func(ctx context.Context) (func(), error) {
			return FenceCtx(ctx, id, "/klasor")
		},
		"Await": func(ctx context.Context) (func(), error) { return noop, Await(ctx, id) },
	}
	for name, take := range ask {
		for i := 0; i < 20; i++ {
			before := n.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			release, err := take(ctx)
			cancel()
			if err != nil {
				t.Fatalf("%s %d never got through the scan: %v", name, i, err)
			}
			release()
			waitFor(t, n, before, 5*time.Second, name+": the scan did not go on after the change")
		}
	}
}

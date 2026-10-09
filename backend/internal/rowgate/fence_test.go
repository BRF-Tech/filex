package rowgate

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Every test uses storage ids of its own (9_200_0xx): the gates are
// process-wide.

// A long change fences its prefixes, not the storage: a judgement takes the
// gate while the fence stands (the rest of the storage is scanned), another
// change joins without waiting, and the fence set says exactly which paths
// are the long change's - the prefixes and everything below them, and in a
// parent's listing the fenced child.
//
// Break: make FenceCtx hold the gate (MoveCtx) - the judgement does not get it.
func TestAFenceKeepsTheStorageOpenToJudgements(t *testing.T) {
	const id = 9_200_001
	release, err := FenceCtx(context.Background(), id, "a/b/", "/c/d")
	if err != nil {
		t.Fatalf("FenceCtx = %v", err)
	}
	defer release()
	judged, ok := TryJudge(id)
	if !ok {
		t.Fatal("a judgement could not take the gate while only a fence stood: the whole storage's scan waits")
	}
	f := Fences(id)
	judged()
	for _, p := range []string{"/a/b", "a/b", "/a/b/x.txt", "/a/b/deep/er", "/c/d", "c/d/e"} {
		if !f.Covers(p) {
			t.Errorf("Covers(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/a", "/a/bb", "/a/b2/x", "/c", "/", "/x/a/b"} {
		if f.Covers(p) {
			t.Errorf("Covers(%q) = true, want false", p)
		}
	}
	if got := f.Kids("/a"); len(got) != 1 || got[0] != "/a/b" {
		t.Errorf("Kids(/a) = %v, want [/a/b]", got)
	}
	if got := f.Kids("c/"); len(got) != 1 || got[0] != "/c/d" {
		t.Errorf("Kids(c/) = %v, want [/c/d]", got)
	}
	if got := f.Kids("/"); len(got) != 0 {
		t.Errorf("Kids(/) = %v, want none", got)
	}
	other, ok := TryMove(id)
	if !ok {
		t.Fatal("a change could not join while a fence stood")
	}
	other()
}

// A fence opens once its change lets go, and counts as a finished change
// (Moves): a walk that fetched the tree before the change asks the storage
// again.
func TestAFenceOpensAndCountsAsAFinishedChange(t *testing.T) {
	const id = 9_200_002
	moves := Moves(id)
	release, err := FenceCtx(context.Background(), id, "/x")
	if err != nil {
		t.Fatalf("FenceCtx = %v", err)
	}
	if Moves(id) != moves {
		t.Fatal("a fence was counted as finished before its change let go")
	}
	release()
	release()
	if !Fences(id).Empty() {
		t.Fatal("the fence is still there after its change let go")
	}
	if got := Moves(id); got != moves+1 {
		t.Fatalf("Moves = %d after one fenced change, want %d", got, moves+1)
	}
}

// A fence is set only while no judgement holds the gate: one set while a
// judgement listed would come too late for that listing. It waits, and the
// next judgement sees it.
//
// Break: set the fence without asking g.judged - it is set at once.
func TestAFenceWaitsForAJudgementAndTheNextOneSeesIt(t *testing.T) {
	const id = 9_200_003
	judged := Judge(id)
	set := make(chan func(), 1)
	go func() {
		release, _ := FenceCtx(context.Background(), id, "/f")
		set <- release
	}()
	select {
	case release := <-set:
		release()
		judged()
		t.Fatal("a fence was set while a judgement held the gate")
	case <-time.After(50 * time.Millisecond):
	}
	if !Fences(id).Empty() {
		judged()
		t.Fatal("the fence set shows a fence a judgement in progress never saw")
	}
	judged()
	var release func()
	select {
	case release = <-set:
	case <-time.After(5 * time.Second):
		t.Fatal("the fence was never set once the judgement let go")
	}
	defer release()
	next, ok := TryJudge(id)
	if !ok {
		t.Fatal("the next judgement could not take the gate beside the fence")
	}
	defer next()
	if !Fences(id).Covers("/f/g") {
		t.Fatal("the next judgement does not see the fence")
	}
}

// A long change called off while it waits for the gate (its request gone, its
// job cancelled) sets no fence and counts no change.
func TestACancelledFenceTakesNothing(t *testing.T) {
	const id = 9_200_004
	judged := Judge(id)
	moves := Moves(id)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		release, err := FenceCtx(ctx, id, "/f")
		release()
		got <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-got:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FenceCtx after its change was called off = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		judged()
		t.Fatal("a fence kept waiting after its change was called off")
	}
	judged()
	if !Fences(id).Empty() {
		t.Fatal("a fence that was called off was set")
	}
	if Moves(id) != moves {
		t.Fatal("a fence that was never set was counted as a finished change")
	}
}

// A long change cancelled half way (its job's Cancel ends the copy loop with
// the context's error) lets its fence go.
func TestACancelledFencedChangeLetsItsFenceGo(t *testing.T) {
	const id = 9_200_005
	ctx, cancel := context.WithCancel(context.Background())
	err := FencedChangeCtx(ctx, id, []string{"/src", "/dst"},
		func() error {
			if !Fences(id).Covers("/src/a") || !Fences(id).Covers("/dst/a") {
				t.Error("the change ran without its fence")
			}
			cancel()
			return ctx.Err()
		},
		func() { t.Error("the rows followed a change that was cancelled") },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FencedChangeCtx = %v, want context.Canceled", err)
	}
	if !Fences(id).Empty() {
		t.Fatal("a cancelled change left its fence standing: the prefixes would never be scanned again")
	}
}

// A long change that panics (a driver, the rows) lets its fence go: a fence
// left standing keeps its prefixes out of every later scan until the process
// restarts.
//
// Break: release the fence without defer in FencedChangeCtx.
func TestAPanickingFencedChangeLetsItsFenceGo(t *testing.T) {
	const id = 9_200_006
	for _, at := range []string{"move", "follow"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: the panic did not come through", at)
				}
			}()
			_ = FencedChangeCtx(context.Background(), id, []string{"/p"},
				func() error {
					if at == "move" {
						panic("driver")
					}
					return nil
				},
				func() { panic("rows") },
			)
		}()
		if !Fences(id).Empty() {
			t.Fatalf("%s: a panic left the fence standing", at)
		}
	}
}

// HoldCtx takes ONE of the two: the gate for a short change (no fence), the
// fence for a long one - and a fence on the storage's root is the gate.
func TestHoldCtxTakesTheGateOrAFenceNeverBoth(t *testing.T) {
	const id = 9_200_007
	gate, err := HoldCtx(context.Background(), id)
	if err != nil {
		t.Fatalf("HoldCtx = %v", err)
	}
	if judged, ok := TryJudge(id); ok {
		judged()
		t.Fatal("a short change did not hold the gate")
	}
	if !Fences(id).Empty() {
		t.Fatal("a short change set a fence")
	}
	gate()

	fence, err := HoldCtx(context.Background(), id, "/long")
	if err != nil {
		t.Fatalf("HoldCtx with a fence = %v", err)
	}
	judged, ok := TryJudge(id)
	if !ok {
		fence()
		t.Fatal("a long change held the gate as well as its fence")
	}
	judged()
	fence()

	root, err := FenceCtx(context.Background(), id, "", "/")
	if err != nil {
		t.Fatalf("FenceCtx on the root = %v", err)
	}
	if judged, ok := TryJudge(id); ok {
		judged()
		root()
		t.Fatal("a fence on the storage's root did not hold the gate")
	}
	root()
}

// Snapshot says how many prefixes are fenced and how long the oldest fence
// has stood (filex_rowgate_fences, filex_rowgate_oldest_fence_seconds).
func TestSnapshotCountsTheFences(t *testing.T) {
	const id = 9_200_008
	a, _ := FenceCtx(context.Background(), id, "/a", "/b")
	b, _ := FenceCtx(context.Background(), id, "/b", "/c")
	time.Sleep(40 * time.Millisecond)
	s := stateOf(id)
	if s.Fences != 3 || s.OldestFence < 30*time.Millisecond {
		a()
		b()
		t.Fatalf("Snapshot = %+v, want 3 fenced prefixes, the oldest fence at least 30ms old", s)
	}
	if s.Changes != 0 {
		t.Errorf("Snapshot counts fences as changes holding the gate: %+v", s)
	}
	a()
	if got := stateOf(id).Fences; got != 2 {
		t.Errorf("Fences after one fence opened = %d, want 2 (/b is still fenced by the other)", got)
	}
	b()
	if s := stateOf(id); s.Fences != 0 || s.OldestFence != 0 {
		t.Errorf("Snapshot after every fence opened = %+v, want none", s)
	}
}

package rowgate

// Issue #127: a change announced without waiting (Want) - the operations
// queue's job put back for a judgement - keeps new judgements back for as long
// as the announcement stands, and no longer.

import (
	"context"
	"testing"
	"time"
)

// Under a scan that takes the gate back the moment it lets go, an announced
// change gets the gate: the scan finishes the directory in hand (Await says
// when) and takes no next one until the announcement is called off; then it
// goes on.
//
// Break: leave g.wants out of open - the scan's next directory takes the gate
// the moment the one in hand lets go, and the change finds it judged again.
func TestAnAnnouncedChangeGetsTheGateFromAScanThatGoesOnAfterIt(t *testing.T) {
	const id = 9_300_101
	n := scanLoop(t, id, 2*time.Millisecond)
	waitFor(t, n, 0, 5*time.Second, "the scan never started")
	for i := 0; i < 10; i++ {
		cancel := Want(id, 5*time.Second)
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		err := Await(ctx, id)
		stop()
		if err != nil {
			cancel()
			t.Fatalf("round %d: the directory in hand never ended: %v", i, err)
		}
		held := n.Load()
		time.Sleep(20 * time.Millisecond)
		if got := n.Load(); got != held {
			cancel()
			t.Fatalf("round %d: the scan judged %d more directories while a change was announced", i, got-held)
		}
		if s := stateOf(id); s.Waiting != 1 || s.Judged {
			cancel()
			t.Fatalf("round %d: Snapshot = %+v, want one change waiting and no judgement", i, s)
		}
		release, ok := TryMove(id)
		cancel()
		cancel() // a second call is the first one's
		if !ok {
			t.Fatalf("round %d: the announced change found the gate judged", i)
		}
		release()
		waitFor(t, n, held, 5*time.Second, "the scan did not go on once the change was through")
	}
}

// An announcement nobody calls off - its job was not run after all - runs out
// after its time, the scan goes on, and it counts as lapsed. Its cancel is
// harmless afterwards.
//
// Break: drop the timer in Want - the scan never goes on.
func TestAnAnnouncementThatIsNotCalledOffRunsOut(t *testing.T) {
	const id = 9_300_102
	n := scanLoop(t, id, 2*time.Millisecond)
	waitFor(t, n, 0, 5*time.Second, "the scan never started")
	lapsed := stateOf(id).LapsedWants
	start := time.Now()
	cancel := Want(id, 150*time.Millisecond)
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := Await(ctx, id); err != nil {
		cancel()
		t.Fatalf("the directory in hand never ended: %v", err)
	}
	held := n.Load()
	waitFor(t, n, held, 5*time.Second, "the scan never went on after the announcement ran out")
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Errorf("the scan went on after %v, before the announcement ran out", waited)
	}
	if got := stateOf(id).LapsedWants; got != lapsed+1 {
		t.Errorf("LapsedWants = %d, want %d", got, lapsed+1)
	}
	cancel()
	cancel()
	if got := stateOf(id).LapsedWants; got != lapsed+1 {
		t.Errorf("a cancel after the announcement ran out counted again: LapsedWants = %d", got)
	}
	if s := stateOf(id); s.Waiting != 0 {
		t.Errorf("Snapshot after the announcement ran out = %+v, want nothing waiting", s)
	}
}

// Snapshot counts the changes waiting for the gate - in MoveCtx while a
// judgement holds it, and announced - and a change that gives up leaves the
// count, so the next judgement is not kept waiting for it.
func TestSnapshotCountsTheChangesWaitingForTheGate(t *testing.T) {
	const id = 9_300_103
	judged := Judge(id)
	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	go func() {
		_, err := MoveCtx(ctx, id)
		gaveUp <- err
	}()
	want := Want(id, time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for stateOf(id).Waiting != 2 {
		if time.Now().After(deadline) {
			cancel()
			want()
			judged()
			t.Fatalf("Snapshot = %+v, want two changes waiting", stateOf(id))
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-gaveUp
	want()
	if s := stateOf(id); s.Waiting != 0 {
		judged()
		t.Fatalf("Snapshot after both gave up = %+v, want none waiting", s)
	}
	judged()
	release, ok := TryJudge(id)
	if !ok {
		t.Fatal("the next judgement waited for changes that had given up")
	}
	release()
}

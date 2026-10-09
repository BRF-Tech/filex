package rowgate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Every test uses storage ids of its own: the gates are process-wide.

// Two-step changes on one storage run side by side; a judgement waits until
// every one of them has let go, and holds the storage alone.
func TestJudgeWaitsForEveryMoveAndMovesRunTogether(t *testing.T) {
	const id = 9_100_001
	a := Move(id)
	b := Move(id) // a second change does not wait for the first
	judged := make(chan struct{})
	go func() {
		release := Judge(id)
		close(judged)
		release()
	}()
	select {
	case <-judged:
		t.Fatal("a judgement ran while two changes were half way")
	case <-time.After(50 * time.Millisecond):
	}
	a()
	select {
	case <-judged:
		t.Fatal("a judgement ran while one change was still half way")
	case <-time.After(50 * time.Millisecond):
	}
	b()
	select {
	case <-judged:
	case <-time.After(5 * time.Second):
		t.Fatal("the judgement never got the gate once both changes let go")
	}
}

// A change that arrives while a judgement holds the gate waits for it.
func TestMoveWaitsForAJudgement(t *testing.T) {
	const id = 9_100_002
	release := Judge(id)
	var moved atomic.Bool
	done := make(chan struct{})
	go func() {
		r := Move(id)
		moved.Store(true)
		r()
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if moved.Load() {
		t.Fatal("a change started while the storage was being judged")
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the change never started after the judgement let go")
	}
}

// Moves counts finished changes, per storage, and a release called twice is
// one release.
func TestMovesCountsFinishedChangesPerStorage(t *testing.T) {
	const id, other = 9_100_003, 9_100_004
	before, otherBefore := Moves(id), Moves(other)
	r := Move(id)
	if Moves(id) != before {
		t.Fatal("a change was counted before it finished")
	}
	r()
	r()
	if got := Moves(id); got != before+1 {
		t.Fatalf("Moves = %d after one finished change, want %d", got, before+1)
	}
	if Moves(other) != otherBefore {
		t.Fatal("a change on one storage was counted on another")
	}
	// The gate is open again: a judgement does not wait.
	done := make(chan struct{})
	go func() { Judge(id)(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a release called twice left the gate held")
	}
}

// Storages do not share a gate.
func TestJudgementsOfTwoStoragesDoNotWaitForEachOther(t *testing.T) {
	const id, other = 9_100_005, 9_100_006
	release := Judge(id)
	defer release()
	var wg sync.WaitGroup
	wg.Add(2)
	done := make(chan struct{})
	go func() { defer wg.Done(); Judge(other)() }()
	go func() { defer wg.Done(); Move(other)() }()
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("another storage waited for this one's judgement")
	}
}

// sec055: a judgement waiting for the gate keeps no change out. Up to 0.55's
// first cut the gate was a sync.RWMutex, which favours the writer: a scan
// waiting behind one long change (a folder moved object by object over WebDAV)
// kept every later change on the storage out - and the operations queue's
// worker with them - until the long change let go.
//
// Break: take the gate's judgement with a lock that new changes queue behind
// (sync.RWMutex.Lock) - the second change waits for the long one.
func TestAWaitingJudgementKeepsNoChangeOut(t *testing.T) {
	const id = 9_100_007
	long := Move(id)
	judged := make(chan struct{})
	go func() {
		release := Judge(id)
		close(judged)
		release()
	}()
	time.Sleep(50 * time.Millisecond) // the judgement is waiting behind the long change
	moved := make(chan struct{})
	go func() {
		Move(id)()
		close(moved)
	}()
	select {
	case <-moved:
	case <-time.After(2 * time.Second):
		long()
		t.Fatal("a change waited behind a judgement that was only waiting for the gate")
	}
	select {
	case <-judged:
		t.Fatal("a judgement ran while a change held the gate")
	default:
	}
	long()
	select {
	case <-judged:
	case <-time.After(5 * time.Second):
		t.Fatal("the judgement never got the gate once the long change let go")
	}
}

// A judgement given a budget steps back for that long and is deferred
// (ErrBusy) instead of waiting for a long change to end; the gate is not
// touched, and the next judgement once the change is over gets it.
func TestAJudgementIsDeferredWhileALongChangeHoldsTheGate(t *testing.T) {
	const id = 9_100_008
	before := stateOf(id).DeferredJudgements
	long := Move(id)
	_, err := JudgeWithin(context.Background(), id, 100*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		long()
		t.Fatalf("JudgeWithin while a change held the gate = %v, want ErrBusy", err)
	}
	if got := stateOf(id).DeferredJudgements; got != before+1 {
		t.Errorf("DeferredJudgements = %d, want %d", got, before+1)
	}
	// The change was not disturbed, and another one still joins it.
	other, ok := TryMove(id)
	if !ok {
		t.Fatal("a change could not join the gate after a judgement gave up")
	}
	other()
	long()
	release, err := JudgeWithin(context.Background(), id, 5*time.Second)
	if err != nil {
		t.Fatalf("the judgement after the change = %v", err)
	}
	release()
}

// sec055: a change waits for the gate on its request's context; a request
// that has gone takes nothing.
//
// Break: wait in MoveCtx without ctx (rowgate.Move as up to 0.55's first cut)
// - the cancelled change takes the gate once the judgement lets go.
func TestACancelledChangeTakesNoGate(t *testing.T) {
	const id = 9_100_009
	judged := Judge(id)
	moves := Moves(id)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		release, err := MoveCtx(ctx, id)
		release() // a release that does nothing when the gate was not taken
		got <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-got:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("MoveCtx after its request went = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		judged()
		t.Fatal("a change kept waiting for the gate after its request had gone")
	}
	judged()
	release, ok := TryJudge(id)
	if !ok {
		t.Fatal("the gate is held after the only change asking for it gave up")
	}
	release()
	if Moves(id) != moves {
		t.Fatal("a change that never got the gate was counted as finished")
	}
	// A request already gone does not take a free gate either.
	if _, err := MoveCtx(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("MoveCtx with a request already gone = %v, want context.Canceled", err)
	}
}

// Snapshot says how long the oldest change has held the gate, and counts the
// changes that held it for LongHold or more (filex_rowgate_*).
func TestSnapshotSaysHowLongAChangeHasHeldTheGate(t *testing.T) {
	const id = 9_100_010
	was := LongHold
	LongHold = 10 * time.Millisecond
	t.Cleanup(func() { LongHold = was })
	before := stateOf(id).LongChanges
	release := Move(id)
	time.Sleep(40 * time.Millisecond)
	s := stateOf(id)
	if s.Changes != 1 || s.OldestChange < 30*time.Millisecond {
		release()
		t.Fatalf("Snapshot = %+v, want one change held for at least 30ms", s)
	}
	release()
	s = stateOf(id)
	if s.Changes != 0 || s.OldestChange != 0 {
		t.Errorf("Snapshot after the release = %+v, want no change holding the gate", s)
	}
	if s.LongChanges != before+1 {
		t.Errorf("LongChanges = %d, want %d", s.LongChanges, before+1)
	}
}

func stateOf(id int64) State {
	of(id) // the gate exists, so Snapshot lists it
	for _, s := range Snapshot() {
		if s.StorageID == id {
			return s
		}
	}
	return State{StorageID: id}
}

// Await waits for a judgement to let go and takes nothing; it ends with its
// context.
func TestAwaitWaitsForAJudgementAndTakesNothing(t *testing.T) {
	const id = 9_100_011
	judged := Judge(id)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := Await(ctx, id); !errors.Is(err, context.DeadlineExceeded) {
		judged()
		t.Fatalf("Await while a judgement held the gate = %v, want the context's end", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- Await(context.Background(), id) }()
	time.Sleep(50 * time.Millisecond)
	judged()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Await after the judgement = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Await never saw the judgement let go")
	}
	release, ok := TryJudge(id)
	if !ok {
		t.Fatal("Await took the gate")
	}
	release()
}

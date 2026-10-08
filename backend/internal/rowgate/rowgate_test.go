package rowgate

import (
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

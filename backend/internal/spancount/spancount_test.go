package spancount

import (
	"strconv"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// A count is this span and the one before: it holds across a boundary, still
// holds in the next span, and is gone two spans on.
func TestWindow_CountsThisSpanAndTheOneBefore(t *testing.T) {
	w := &Window{Span: time.Minute}
	at := t0.Add(59 * time.Second)
	for i := 1; i <= 3; i++ {
		if n := w.Add(at, "a", 1); n != i {
			t.Fatalf("add %d: count %d", i, n)
		}
	}
	at = t0.Add(61 * time.Second) // the next span, two seconds later
	if n := w.Count(at, "a"); n != 3 {
		t.Fatalf("over the boundary the count is %d, want the 3 from just before it", n)
	}
	if n := w.Add(at, "a", 2); n != 5 {
		t.Fatalf("this span's 2 and the last span's 3: %d", n)
	}
	if n := w.Count(t0.Add(2*time.Minute), "a"); n != 2 {
		t.Fatalf("a span on, only the last span's 2 is left: %d", n)
	}
	if n := w.Count(t0.Add(3*time.Minute), "a"); n != 0 {
		t.Fatalf("two spans on, nothing is left: %d", n)
	}
	if cur, prev := w.Clients(); cur != 0 || prev != 0 {
		t.Fatalf("both maps are new: %d, %d", cur, prev)
	}
}

// After a quiet longer than a span both maps are new: the old current map is
// not taken for the one before.
func TestWindow_AQuietEndsBothSpans(t *testing.T) {
	w := &Window{Span: time.Minute}
	w.Add(t0, "a", 7)
	if n := w.Count(t0.Add(5*time.Minute), "a"); n != 0 {
		t.Fatalf("five minutes later: %d", n)
	}
}

// A full map does not take a new client - Add answers 0 and nothing grows -
// but it still counts the clients it holds.
func TestWindow_AFullMapTakesNoNewClient(t *testing.T) {
	w := &Window{Span: time.Hour, Max: 3}
	for i := 0; i < 3; i++ {
		w.Add(t0, "c"+strconv.Itoa(i), 1)
	}
	if n := w.Add(t0, "new", 1); n != 0 {
		t.Fatalf("a full map counted a new client: %d", n)
	}
	if n := w.Count(t0, "new"); n != 0 {
		t.Fatalf("the new client has a count: %d", n)
	}
	if n := w.Add(t0, "c0", 1); n != 2 {
		t.Fatalf("a client the map holds is still counted: %d", n)
	}
	if cur, _ := w.Clients(); cur != 3 {
		t.Fatalf("the map holds %d clients, its Max is 3", cur)
	}
	// The next span starts empty: the new client is counted there.
	if n := w.Add(t0.Add(time.Hour), "new", 1); n != 1 {
		t.Fatalf("the next span did not take the new client: %d", n)
	}
}

func TestWindow_ForgetDropsBothSpans(t *testing.T) {
	w := &Window{Span: time.Minute}
	w.Add(t0, "a", 4)
	w.Add(t0.Add(time.Minute), "a", 1)
	w.Forget("a")
	if n := w.Count(t0.Add(time.Minute), "a"); n != 0 {
		t.Fatalf("forgotten, still %d", n)
	}
}

// A Window without a Span would start a new map on every call and count
// nothing, so it says so instead.
func TestWindow_ASpanIsRequired(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a Window with no Span counted")
		}
	}()
	(&Window{}).Add(t0, "a", 1)
}

// A flood of fresh clients is O(1) a client and stops at Max.
func TestWindow_AFloodStaysBounded(t *testing.T) {
	w := &Window{Span: time.Hour}
	began := time.Now()
	for i := 0; i < MaxClients+50000; i++ {
		w.Add(t0, strconv.Itoa(i), 1)
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Fatalf("%d fresh clients took %v: not O(1) a client", MaxClients+50000, took)
	}
	if cur, _ := w.Clients(); cur != MaxClients {
		t.Fatalf("the map holds %d, the cap is %d", cur, MaxClients)
	}
}

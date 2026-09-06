package realtime

import (
	"encoding/json"
	"testing"
	"time"
)

// fastHub is a hub with a coalescing window small enough for a test to wait on
// without making the suite slow. The ratio (min → max, doubling) is the same
// one production runs.
func fastHub() *Hub {
	h := NewHub()
	h.coalesceMin = 20 * time.Millisecond
	h.coalesceMax = 80 * time.Millisecond
	return h
}

// waitChange reads the next change frame, failing if none arrives in time.
func waitChange(t *testing.T, c *Client, within time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case raw := <-c.Send:
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("bad frame json: %v (%s)", err, raw)
			}
			if m["type"] == "change" {
				return m
			}
		case <-deadline:
			t.Fatalf("no change frame within %s", within)
			return nil
		}
	}
}

// countChanges drains everything queued right now and counts change frames.
func countChanges(c *Client) (n int, last map[string]any) {
	for {
		select {
		case raw := <-c.Send:
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil && m["type"] == "change" {
				n++
				last = m
			}
		default:
			return n, last
		}
	}
}

// TestSingleChangeIsNotDelayed is the acceptance bar this whole layer exists to
// protect: one ordinary write must be on the wire before EmitChange returns, so
// coalescing costs a single upload nothing at all.
func TestSingleChangeIsNotDelayed(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 16)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "rapor.pdf"})

	n, last := countChanges(c)
	if n != 1 {
		t.Fatalf("want exactly 1 immediate frame, got %d", n)
	}
	if last["name"] != "rapor.pdf" {
		t.Fatalf("want the file's own name, got %#v", last)
	}
	if _, has := last["count"]; has {
		t.Fatalf("a single change must not carry a count: %#v", last)
	}
}

// TestNFSStyleBurstCollapses reproduces the measured NFS shape: one logical
// write arriving as several identical announcements (go-nfs closes the handle
// on every write RPC). Six frames must become two — one immediate, one merged —
// and the merged one must still name the file, because every merged event was
// the same event.
func TestNFSStyleBurstCollapses(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 32)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	ev := ChangeEvent{Action: "upload", Name: "big.iso"}
	for i := 0; i < 6; i++ {
		h.EmitChange(1, "docs", ev)
	}

	first := waitChange(t, c, time.Second)
	if first["name"] != "big.iso" {
		t.Fatalf("first frame should be the plain event, got %#v", first)
	}
	trailing := waitChange(t, c, time.Second)
	if trailing["name"] != "big.iso" || trailing["action"] != "upload" {
		t.Fatalf("merged frame lost the (identical) event: %#v", trailing)
	}
	if got, _ := trailing["count"].(float64); got != 5 {
		t.Fatalf("merged frame should stand for the other 5, got %#v", trailing["count"])
	}
	if n, _ := countChanges(c); n != 0 {
		t.Fatalf("6 identical changes should be 2 frames, got %d extra", n)
	}
}

// TestMixedBurstKeepsTheLastStateAndDropsTheName — a burst of DIFFERENT changes
// still ends in a frame (nothing is lost, the listing is refetched), but naming
// one of many would be a lie, so the name is omitted and the count says why.
func TestMixedBurstKeepsTheLastStateAndDropsTheName(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 32)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "a.txt"})
	h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "b.txt"})
	h.EmitChange(1, "docs", ChangeEvent{Action: "delete", Name: "c.txt"})

	_ = waitChange(t, c, time.Second) // the immediate a.txt
	merged := waitChange(t, c, time.Second)
	if merged["action"] != "delete" {
		t.Fatalf("merged frame should carry the burst's last action, got %#v", merged)
	}
	if _, has := merged["name"]; has {
		t.Fatalf("a mixed merge must not name one of its members: %#v", merged)
	}
	if got, _ := merged["count"].(float64); got != 2 {
		t.Fatalf("want count 2, got %#v", merged["count"])
	}
}

// TestBurstAlwaysEndsWithAFrame is the anti-staleness promise: whatever the
// timing, the last change of a burst is announced. A folder whose final frame
// was swallowed stays wrong until the person navigates away and back.
func TestBurstAlwaysEndsWithAFrame(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 64)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	for i := 0; i < 50; i++ {
		h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "f.txt"})
		time.Sleep(2 * time.Millisecond)
	}
	h.EmitChange(1, "docs", ChangeEvent{Action: "delete", Name: "last.txt"})

	deadline := time.Now().Add(2 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		if n, m := countChanges(c); n > 0 {
			last = m
		}
		if last != nil && last["action"] == "delete" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the burst's final change never arrived; last frame was %#v", last)
}

// TestQuietRoomResetsTheWindow — the backoff is a property of a burst, not of a
// room. Once a folder goes quiet, the next change is immediate again.
func TestQuietRoomResetsTheWindow(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 32)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	for i := 0; i < 5; i++ {
		h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "f.txt"})
	}
	// Let the burst drain and the room fall idle for longer than coalesceMax.
	time.Sleep(300 * time.Millisecond)
	countChanges(c)

	h.EmitChange(1, "docs", ChangeEvent{Action: "create", Name: "yeni.txt"})
	n, last := countChanges(c)
	if n != 1 || last["name"] != "yeni.txt" {
		t.Fatalf("a change into a quiet room must go straight out, got n=%d last=%#v", n, last)
	}
}

// TestWindowNeverExceedsMax — the backoff is bounded, so a long job keeps
// reporting progress instead of going silent.
func TestWindowNeverExceedsMax(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 256)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	stop := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(stop) {
		h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "f.txt"})
		time.Sleep(time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	h.mu.Lock()
	w := h.rooms[RoomKey(1, "docs")].window
	h.mu.Unlock()
	if w > h.coalesceMax {
		t.Fatalf("window ran past the cap: %s > %s", w, h.coalesceMax)
	}
}

// TestEmptiedRoomStopsItsTimer — an armed flush must not outlive the room, or a
// busy server accumulates one timer per folder anybody ever opened.
func TestEmptiedRoomStopsItsTimer(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "Ayşe", 4)
	h.Subscribe(c, 1, "docs", "s3://docs")
	drainAll(c)

	h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "a"})
	h.EmitChange(1, "docs", ChangeEvent{Action: "upload", Name: "b"}) // arms a flush
	h.Unsubscribe(c)

	time.Sleep(100 * time.Millisecond) // the flush would have fired by now
	h.mu.Lock()
	_, still := h.rooms[RoomKey(1, "docs")]
	h.mu.Unlock()
	if still {
		t.Fatal("the room should be gone once its last client left")
	}
}

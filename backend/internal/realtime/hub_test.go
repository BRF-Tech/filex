package realtime

import (
	"encoding/json"
	"testing"
)

// drain reads one frame from a client's Send channel without blocking forever.
// It fails the test if nothing is queued (the hub broadcasts synchronously, so
// a frame is always present by the time control returns from a Hub method).
func drain(t *testing.T, c *Client) map[string]any {
	t.Helper()
	select {
	case raw := <-c.Send:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("bad frame json: %v (%s)", err, raw)
		}
		return m
	default:
		t.Fatalf("expected a queued frame for client %d, got none", c.ID)
		return nil
	}
}

// drainAll empties a client's queue and returns the last frame of each type,
// so tests can assert on the final state regardless of intermediate churn.
func drainAll(c *Client) map[string]map[string]any {
	last := map[string]map[string]any{}
	for {
		select {
		case raw := <-c.Send:
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil {
				if t, _ := m["type"].(string); t != "" {
					last[t] = m
				}
			}
		default:
			return last
		}
	}
}

func presenceNames(m map[string]any) []string {
	users, _ := m["users"].([]any)
	names := make([]string, 0, len(users))
	for _, u := range users {
		if um, ok := u.(map[string]any); ok {
			if n, _ := um["name"].(string); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

// TestHubChangeBroadcast: two clients subscribe to the same folder, an emit
// reaches both; a client in a different folder does not receive it.
func TestHubChangeBroadcast(t *testing.T) {
	h := NewHub()
	ayse := NewClient(1, "Ayşe", 16)
	burak := NewClient(2, "Burak", 16)
	other := NewClient(3, "Other", 16)

	h.Subscribe(ayse, 7, "reports", "s3://reports")
	h.Subscribe(burak, 7, "/reports/", "s3://reports") // different spelling, same room
	h.Subscribe(other, 7, "photos", "s3://photos")

	// Clear the presence frames emitted by the subscribes.
	drainAll(ayse)
	drainAll(burak)
	drainAll(other)

	h.EmitChange(7, "reports", ChangeEvent{Action: "create", Name: "q3.pdf"})

	a := drain(t, ayse)
	if a["type"] != "change" || a["action"] != "create" || a["name"] != "q3.pdf" {
		t.Fatalf("ayşe got wrong change frame: %#v", a)
	}
	if a["path"] != "s3://reports" {
		t.Fatalf("expected room path echoed, got %v", a["path"])
	}
	b := drain(t, burak)
	if b["type"] != "change" || b["name"] != "q3.pdf" {
		t.Fatalf("burak got wrong change frame: %#v", b)
	}
	// The photos viewer must NOT have received anything.
	if got := drainAll(other); len(got) != 0 {
		t.Fatalf("other (different folder) unexpectedly got frames: %#v", got)
	}
}

// TestHubPresenceJoinLeaveFocus exercises the presence lifecycle.
func TestHubPresenceJoinLeaveFocus(t *testing.T) {
	h := NewHub()
	ayse := NewClient(1, "Ayşe", 16)
	burak := NewClient(2, "Burak", 16)

	// Ayşe joins alone.
	h.Subscribe(ayse, 5, "x", "s3://x")
	if got := presenceNames(drain(t, ayse)); len(got) != 1 || got[0] != "Ayşe" {
		t.Fatalf("expected [Ayşe], got %v", got)
	}

	// Burak joins → both see a 2-person roster.
	h.Subscribe(burak, 5, "x", "s3://x")
	if got := presenceNames(drainAll(ayse)["presence"]); len(got) != 2 {
		t.Fatalf("ayşe expected 2 users after burak joined, got %v", got)
	}
	if got := presenceNames(drainAll(burak)["presence"]); len(got) != 2 {
		t.Fatalf("burak expected 2 users, got %v", got)
	}

	// Burak focuses a file → presence carries the file for his entry.
	h.SetFocus(burak, "rapor.pdf")
	pres := drainAll(ayse)["presence"]
	if pres == nil {
		t.Fatal("ayşe expected a presence update after burak focus")
	}
	users, _ := pres["users"].([]any)
	foundFocus := false
	for _, u := range users {
		um := u.(map[string]any)
		if um["name"] == "Burak" && um["file"] == "rapor.pdf" {
			foundFocus = true
		}
	}
	if !foundFocus {
		t.Fatalf("expected Burak focused on rapor.pdf, got %#v", users)
	}

	// Burak leaves → Ayşe sees a 1-person roster again.
	h.Unsubscribe(burak)
	if got := presenceNames(drainAll(ayse)["presence"]); len(got) != 1 || got[0] != "Ayşe" {
		t.Fatalf("expected [Ayşe] after burak left, got %v", got)
	}

	// The Presence() accessor agrees.
	if snap := h.Presence(5, "x"); len(snap) != 1 || snap[0].Name != "Ayşe" {
		t.Fatalf("Presence() snapshot mismatch: %#v", snap)
	}
}

// TestHubResubscribeMovesRoom verifies switching folders leaves the old room.
func TestHubResubscribeMovesRoom(t *testing.T) {
	h := NewHub()
	ayse := NewClient(1, "Ayşe", 16)

	h.Subscribe(ayse, 1, "a", "s3://a")
	h.Subscribe(ayse, 1, "b", "s3://b")
	drainAll(ayse)

	// A change in the old folder must not reach her anymore.
	h.EmitChange(1, "a", ChangeEvent{Action: "create", Name: "stale"})
	if got := drainAll(ayse); len(got) != 0 {
		t.Fatalf("expected no frame from old folder, got %#v", got)
	}
	// A change in the new folder does.
	h.EmitChange(1, "b", ChangeEvent{Action: "create", Name: "fresh"})
	if m := drainAll(ayse)["change"]; m == nil || m["name"] != "fresh" {
		t.Fatalf("expected change from new folder, got %#v", m)
	}
	if snap := h.Presence(1, "a"); len(snap) != 0 {
		t.Fatalf("old room should be empty, got %#v", snap)
	}
}

// TestHubDedupePerUser: two tabs from one user collapse to a single roster
// entry, preferring the tab that has a focused file.
func TestHubDedupePerUser(t *testing.T) {
	h := NewHub()
	tab1 := NewClient(9, "Solo", 16)
	tab2 := NewClient(9, "Solo", 16)

	h.Subscribe(tab1, 2, "d", "s3://d")
	h.Subscribe(tab2, 2, "d", "s3://d")
	h.SetFocus(tab2, "doc.txt")

	snap := h.Presence(2, "d")
	if len(snap) != 1 {
		t.Fatalf("expected 1 deduped user, got %#v", snap)
	}
	if snap[0].File != "doc.txt" {
		t.Fatalf("expected focused file preserved, got %#v", snap[0])
	}
}

// TestHubEmitNoRoom is a no-op safety check (no subscribers).
func TestHubEmitNoRoom(t *testing.T) {
	h := NewHub()
	h.EmitChange(99, "nobody", ChangeEvent{Action: "delete", Name: "x"}) // must not panic
	if snap := h.Presence(99, "nobody"); snap != nil {
		t.Fatalf("expected nil snapshot, got %#v", snap)
	}
}

// TestHubNonBlockingSend: a client whose buffer is full still lets the hub
// broadcast to others (dropped frame, no deadlock).
func TestHubNonBlockingSend(t *testing.T) {
	h := NewHub()
	slow := NewClient(1, "Slow", 1)
	fast := NewClient(2, "Fast", 16)
	h.Subscribe(slow, 1, "r", "s3://r")
	h.Subscribe(fast, 1, "r", "s3://r")

	// Do NOT drain slow — its 1-slot buffer will fill and subsequent frames
	// drop. fast must still receive every change.
	for i := 0; i < 5; i++ {
		h.EmitChange(1, "r", ChangeEvent{Action: "create", Name: "f"})
	}
	// fast should have received frames (presence + changes); assert it got at
	// least one change without the test hanging.
	if got := drainAll(fast)["change"]; got == nil {
		t.Fatal("fast client should have received change frames")
	}
}

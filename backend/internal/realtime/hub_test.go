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
	ada := NewClient(2, "Ada", 16)
	other := NewClient(3, "Other", 16)

	h.Subscribe(ayse, 7, "reports", "s3://reports")
	h.Subscribe(ada, 7, "/reports/", "s3://reports") // different spelling, same room
	h.Subscribe(other, 7, "photos", "s3://photos")

	// Clear the presence frames emitted by the subscribes.
	drainAll(ayse)
	drainAll(ada)
	drainAll(other)

	h.EmitChange(7, "reports", ChangeEvent{Action: "create", Name: "q3.pdf"})

	a := drain(t, ayse)
	if a["type"] != "change" || a["action"] != "create" || a["name"] != "q3.pdf" {
		t.Fatalf("ayşe got wrong change frame: %#v", a)
	}
	if a["path"] != "s3://reports" {
		t.Fatalf("expected room path echoed, got %v", a["path"])
	}
	b := drain(t, ada)
	if b["type"] != "change" || b["name"] != "q3.pdf" {
		t.Fatalf("ada got wrong change frame: %#v", b)
	}
	// The photos viewer must NOT have received anything.
	if got := drainAll(other); len(got) != 0 {
		t.Fatalf("other (different folder) unexpectedly got frames: %#v", got)
	}
}

// TestHubPerClientPath: two clients share ONE room but subscribed with
// different display paths — an embedded explorer with a confine-RELATIVE path
// ("s3://") and a native panel with the absolute path ("s3://projeler/5").
// Every frame (change + presence) must echo each client's OWN path so their
// client-side path-matching accepts it; a room-shared path would break one side.
func TestHubPerClientPath(t *testing.T) {
	h := NewHub()
	embedded := NewClient(1, "Embedded", 16) // sees the room as its root
	native := NewClient(2, "Native", 16)     // sees the absolute path

	// Same room key (storage 7, dir "projeler/5"), different display paths.
	h.Subscribe(embedded, 7, "projeler/5", "s3://")
	h.Subscribe(native, 7, "projeler/5", "s3://projeler/5")

	// Presence frames from the subscribes must each carry the client's own path.
	if got := drainAll(embedded)["presence"]["path"]; got != "s3://" {
		t.Fatalf("embedded presence path = %v, want s3://", got)
	}
	if got := drainAll(native)["presence"]["path"]; got != "s3://projeler/5" {
		t.Fatalf("native presence path = %v, want s3://projeler/5", got)
	}

	// A single emit for the shared room reaches both, each with its own path.
	h.EmitChange(7, "projeler/5", ChangeEvent{Action: "upload", Name: "a.pdf"})
	if e := drain(t, embedded); e["type"] != "change" || e["path"] != "s3://" {
		t.Fatalf("embedded change frame = %#v, want path s3://", e)
	}
	if n := drain(t, native); n["type"] != "change" || n["path"] != "s3://projeler/5" {
		t.Fatalf("native change frame = %#v, want path s3://projeler/5", n)
	}
}

// TestHubPresenceJoinLeaveFocus exercises the presence lifecycle.
func TestHubPresenceJoinLeaveFocus(t *testing.T) {
	h := NewHub()
	ayse := NewClient(1, "Ayşe", 16)
	ada := NewClient(2, "Ada", 16)

	// Ayşe joins alone — presence answers "who ELSE is here", so her roster is
	// empty (self is excluded).
	h.Subscribe(ayse, 5, "x", "s3://x")
	if got := presenceNames(drain(t, ayse)); len(got) != 0 {
		t.Fatalf("expected empty roster for lone subscriber, got %v", got)
	}

	// Ada joins → each sees the OTHER (and not themselves).
	h.Subscribe(ada, 5, "x", "s3://x")
	if got := presenceNames(drainAll(ayse)["presence"]); len(got) != 1 || got[0] != "Ada" {
		t.Fatalf("ayşe expected [Ada] after ada joined, got %v", got)
	}
	if got := presenceNames(drainAll(ada)["presence"]); len(got) != 1 || got[0] != "Ayşe" {
		t.Fatalf("ada expected [Ayşe], got %v", got)
	}

	// Ada focuses a file → presence carries the file for his entry.
	h.SetFocus(ada, "rapor.pdf")
	pres := drainAll(ayse)["presence"]
	if pres == nil {
		t.Fatal("ayşe expected a presence update after ada focus")
	}
	users, _ := pres["users"].([]any)
	foundFocus := false
	for _, u := range users {
		um := u.(map[string]any)
		if um["name"] == "Ada" && um["file"] == "rapor.pdf" {
			foundFocus = true
		}
	}
	if !foundFocus {
		t.Fatalf("expected Ada focused on rapor.pdf, got %#v", users)
	}

	// Ada leaves → Ayşe's roster is empty again.
	h.Unsubscribe(ada)
	if got := presenceNames(drainAll(ayse)["presence"]); len(got) != 0 {
		t.Fatalf("expected empty roster after ada left, got %v", got)
	}

	// The Presence() accessor is the FULL room roster (diagnostics) — it still
	// includes Ayşe herself.
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

// TestHubPresenceKeySharedToken: embedded hosts authenticate every end user
// with ONE proxy token (same filex UserID). PresenceKey must keep them apart:
// two work users behind user id 1 are two roster entries, each sees the other
// (not themselves), and a native observer sees both.
func TestHubPresenceKeySharedToken(t *testing.T) {
	h := NewHub()
	ada := NewClient(1, "Ada", 16)
	ada.PresenceKey = "work-7"
	grace := NewClient(1, "Grace", 16)
	grace.PresenceKey = "work-8"
	native := NewClient(2, "Native", 16)

	h.Subscribe(ada, 3, "p", "s3://")
	h.Subscribe(grace, 3, "p", "s3://")
	h.Subscribe(native, 3, "p", "s3://p")

	if got := presenceNames(drainAll(native)["presence"]); len(got) != 2 {
		t.Fatalf("native expected both shared-token users, got %v", got)
	}
	got := presenceNames(drainAll(ada)["presence"])
	if len(got) != 2 {
		t.Fatalf("ada expected [Grace Native], got %v", got)
	}
	for _, n := range got {
		if n == "Ada" {
			t.Fatalf("ada must not see himself, got %v", got)
		}
	}

	// Wire entries carry a stable uid per identity (for client-side keying).
	pres := drainAll(grace)["presence"]
	users, _ := pres["users"].([]any)
	seen := map[string]bool{}
	for _, u := range users {
		um := u.(map[string]any)
		uid, _ := um["uid"].(string)
		if uid == "" || seen[uid] {
			t.Fatalf("expected unique non-empty uids, got %#v", users)
		}
		seen[uid] = true
	}
}

// TestHubRenameFocusFollow: a rename in the room updates any presence focus
// pointing at the old name — viewers see the focus under the NEW name without
// the focuser re-sending anything.
func TestHubRenameFocusFollow(t *testing.T) {
	h := NewHub()
	ayse := NewClient(1, "Ayşe", 16)
	ada := NewClient(2, "Ada", 16)
	h.Subscribe(ayse, 4, "docs", "s3://docs")
	h.Subscribe(ada, 4, "docs", "s3://docs")
	h.SetFocus(ayse, "eski.pdf")
	drainAll(ayse)
	drainAll(ada)

	h.EmitChange(4, "docs", ChangeEvent{Action: "rename", Name: "eski.pdf", NewName: "yeni.pdf"})

	pres := drainAll(ada)["presence"]
	if pres == nil {
		t.Fatal("expected a presence re-broadcast after rename")
	}
	users, _ := pres["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("ada expected [Ayşe], got %#v", users)
	}
	if um := users[0].(map[string]any); um["file"] != "yeni.pdf" {
		t.Fatalf("expected focus to follow rename to yeni.pdf, got %#v", um)
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

package realtime

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// treeFrames drains everything queued right now and returns the tree_change
// frames, in order.
func treeFrames(c *Client) []map[string]any {
	var out []map[string]any
	for {
		select {
		case raw := <-c.Send:
			var m map[string]any
			if json.Unmarshal(raw, &m) == nil && m["type"] == "tree_change" {
				out = append(out, m)
			}
		default:
			return out
		}
	}
}

// waitTree blocks for the next tree_change frame.
func waitTree(t *testing.T, c *Client, within time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case raw := <-c.Send:
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("bad frame: %v", err)
			}
			if m["type"] == "tree_change" {
				return m
			}
		case <-deadline:
			t.Fatalf("no tree_change within %s", within)
			return nil
		}
	}
}

func dirsOf(m map[string]any) []string {
	raw, _ := m["dirs"].([]any)
	out := make([]string, 0, len(raw))
	for _, d := range raw {
		out = append(out, d.(string))
	}
	return out
}

// A mirror keeps a whole subtree, so a change three levels down must reach
// it exactly like a change in the root — and a sibling that merely SHARES A
// PREFIX ("/proj-old" next to "/proj") must not.
func TestWatchIsRecursiveAndRespectsTheFolderBoundary(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "engine", 64)
	h.Watch(c, []WatchRoot{{StorageID: 1, Dir: "proj", Display: "docs://proj"}})

	h.EmitChange(1, "proj/a/b", ChangeEvent{Action: "upload", Name: "deep.txt"})
	f := waitTree(t, c, time.Second)
	if got := dirsOf(f); len(got) != 1 || got[0] != "a/b" {
		t.Fatalf("dirs must be root-relative, got %v", got)
	}
	if f["root"] != "docs://proj" || f["name"] != "deep.txt" || f["action"] != "upload" {
		t.Fatalf("single change must name itself and echo the root: %#v", f)
	}

	time.Sleep(100 * time.Millisecond) // let the watch go quiet
	h.EmitChange(1, "proj-old", ChangeEvent{Action: "upload", Name: "x"})
	h.EmitChange(1, "", ChangeEvent{Action: "create", Name: "proj2"})
	h.EmitChange(2, "proj", ChangeEvent{Action: "upload", Name: "other-storage"})
	time.Sleep(100 * time.Millisecond)
	if fr := treeFrames(c); len(fr) != 0 {
		t.Fatalf("changes outside the watched subtree leaked: %v", fr)
	}

	h.EmitChange(1, "proj", ChangeEvent{Action: "delete", Name: "gone.txt"})
	f = waitTree(t, c, time.Second)
	if got := dirsOf(f); len(got) != 1 || got[0] != "" {
		t.Fatalf("a change in the root itself is the empty relative dir, got %v", got)
	}
}

// Nobody has the folder open in an explorer — no room exists at all — and
// the mirror must still hear about it. (EmitChange used to return early when
// the room was empty.)
func TestWatchHearsFoldersNobodyIsViewing(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "engine", 16)
	h.Watch(c, []WatchRoot{{StorageID: 7, Dir: "", Display: "docs://"}})
	h.EmitChange(7, "unseen/folder", ChangeEvent{Action: "upload", Name: "a.txt"})
	f := waitTree(t, c, time.Second)
	if got := dirsOf(f); len(got) != 1 || got[0] != "unseen/folder" {
		t.Fatalf("got %v", got)
	}
}

// The size refresher announces a "modify" on every ancestor ~2 s after each
// change. A mirror must not re-list the whole chain for it — but explorers
// still need it for their size column.
func TestWatchIgnoresDerivedSizeRefreshes(t *testing.T) {
	h := fastHub()
	w := NewClient(1, "engine", 16)
	h.Watch(w, []WatchRoot{{StorageID: 1, Dir: "proj", Display: "docs://proj"}})
	viewer := NewClient(2, "Ayşe", 16)
	h.Subscribe(viewer, 1, "proj", "docs://proj")
	drainAll(viewer)

	h.EmitChange(1, "proj", ChangeEvent{Action: "modify", Derived: true})

	if n, _ := countChanges(viewer); n != 1 {
		t.Fatalf("the explorer must still get the size refresh, got %d frames", n)
	}
	time.Sleep(60 * time.Millisecond)
	if fr := treeFrames(w); len(fr) != 0 {
		t.Fatalf("a derived event reached the watcher: %v", fr)
	}
}

// A watcher is not a viewer: it must never appear in a presence roster, and
// never be sent one.
func TestWatcherIsInvisibleInPresence(t *testing.T) {
	h := fastHub()
	w := NewClient(1, "Burak (filex desktop)", 16)
	h.Watch(w, []WatchRoot{{StorageID: 1, Dir: "proj", Display: "docs://proj"}})
	viewer := NewClient(2, "Ayşe", 16)
	h.Subscribe(viewer, 1, "proj", "docs://proj")

	if roster := h.Presence(1, "proj"); len(roster) != 1 || roster[0].Name != "Ayşe" {
		t.Fatalf("roster must hold only the viewer: %+v", roster)
	}
	for {
		select {
		case raw := <-w.Send:
			t.Fatalf("the watcher received a frame it should not: %s", raw)
		default:
			return
		}
	}
}

// An autosaving editor or an extraction is many changes; the watcher gets
// the first at once, then one merged frame per window — and the LAST frame
// of the burst names every folder the burst touched.
func TestWatchBurstCoalescesAndLosesNoFolder(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "engine", 64)
	h.Watch(c, []WatchRoot{{StorageID: 1, Dir: "", Display: "docs://"}})

	for i := 0; i < 500; i++ {
		h.EmitChange(1, fmt.Sprintf("d%d", i%10), ChangeEvent{Action: "upload"})
	}
	time.Sleep(300 * time.Millisecond)
	frames := treeFrames(c)
	if len(frames) < 2 || len(frames) > 6 {
		t.Fatalf("500 changes should cost a handful of frames, got %d", len(frames))
	}
	seen := map[string]bool{}
	total := 0
	for _, f := range frames {
		for _, d := range dirsOf(f) {
			seen[d] = true
		}
		if n, ok := f["count"].(float64); ok {
			total += int(n)
		} else {
			total++
		}
	}
	if len(seen) != 10 {
		t.Fatalf("every touched folder must be named across the frames, got %v", seen)
	}
	if total != 500 {
		t.Fatalf("counts must add up to the changes made: %d", total)
	}
}

// Past watchMaxDirs distinct folders the frame stops listing them and says
// "overflow" — the mirror does a full pass instead.
func TestWatchOverflowsInsteadOfGrowingWithoutBound(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "engine", 64)
	h.Watch(c, []WatchRoot{{StorageID: 1, Dir: "", Display: "docs://"}})
	for i := 0; i < watchMaxDirs+50; i++ {
		h.EmitChange(1, fmt.Sprintf("f%04d", i), ChangeEvent{Action: "upload"})
	}
	time.Sleep(200 * time.Millisecond)
	frames := treeFrames(c)
	last := frames[len(frames)-1]
	if last["overflow"] != true {
		t.Fatalf("expected an overflow frame, got %#v", last)
	}
	if _, has := last["dirs"]; has {
		t.Fatalf("an overflow frame must not carry a partial list: %#v", last)
	}
}

// ⚠ A room drops a frame when the client's queue is full; a watch must not —
// a lost frame would be a lost edit until the next poll. Fill the queue, emit,
// drain, and the change still arrives.
func TestWatchRetriesAFullQueueInsteadOfDropping(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "engine", 1)
	h.Watch(c, []WatchRoot{{StorageID: 1, Dir: "", Display: "docs://"}})
	c.Send <- []byte(`{"type":"pong"}`) // queue now full

	h.EmitChange(1, "busy", ChangeEvent{Action: "upload", Name: "late.txt"})
	time.Sleep(50 * time.Millisecond)
	<-c.Send // the pong; room for one frame again

	f := waitTree(t, c, time.Second)
	if got := dirsOf(f); len(got) != 1 || got[0] != "busy" {
		t.Fatalf("the change that met a full queue was lost: %#v", f)
	}
}

// Re-watching REPLACES the set, and disconnecting removes it.
func TestWatchReplaceAndUnsubscribe(t *testing.T) {
	h := fastHub()
	c := NewClient(1, "engine", 16)
	h.Watch(c, []WatchRoot{{StorageID: 1, Dir: "a", Display: "docs://a"}, {StorageID: 1, Dir: "b", Display: "docs://b"}})
	if n := h.Watching(c); n != 2 {
		t.Fatalf("want 2 roots, got %d", n)
	}
	h.Watch(c, []WatchRoot{{StorageID: 1, Dir: "b", Display: "docs://b"}})
	h.EmitChange(1, "a", ChangeEvent{Action: "upload"})
	time.Sleep(40 * time.Millisecond)
	if fr := treeFrames(c); len(fr) != 0 {
		t.Fatalf("a dropped root still delivered: %v", fr)
	}
	h.Unsubscribe(c)
	if n := h.Watching(c); n != 0 {
		t.Fatalf("disconnect must drop every watch, %d left", n)
	}
	h.mu.Lock()
	left := len(h.watches)
	h.mu.Unlock()
	if left != 0 {
		t.Fatalf("hub still holds %d storage watch lists", left)
	}
}

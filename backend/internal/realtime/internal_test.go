package realtime

import (
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestHubNeverAnnouncesFilexOwnDirectories: no change frame is sent about the
// inside of filex's own directories, nor one that names them — while an
// ordinary change in the same rooms still goes out at once.
//
// Measured on the build before the filter: a viewer of the storage root got
// {"type":"change","action":"create","name":".filex-open"} when the desktop
// created its working area, and a client subscribed to `.filex-open` (the
// explorer did that before its listing call learned to land on the root)
// got every save of somebody's open document.
func TestHubNeverAnnouncesFilexOwnDirectories(t *testing.T) {
	h := NewHub()
	h.coalesceMin = 0 // every emit goes straight out; nothing is merged away
	root := NewClient(1, "Ayşe", 64)
	h.Subscribe(root, 7, "", "docs://")
	inside := map[string]*Client{}
	for i, d := range syspath.Dirs() {
		c := NewClient(int64(10+i), "Inside "+d, 64)
		h.Subscribe(c, 7, d, "docs://"+d)
		inside[d] = c
	}
	for _, c := range append([]*Client{root}, values(inside)...) {
		drainAll(c)
	}

	for _, d := range syspath.Dirs() {
		// Inside the directory: a save, a trash move, a snapshot.
		h.EmitChange(7, d, ChangeEvent{Action: "upload", Name: "a1b2c3d4e5f6-Plan.docx"})
		h.EmitChange(7, "/"+d+"/42", ChangeEvent{Action: "create", Name: "1"})
		// At the root, naming it.
		h.EmitChange(7, "", ChangeEvent{Action: "create", Name: d})
		h.EmitChange(7, "", ChangeEvent{Action: "rename", Name: "x", NewName: d})
	}
	h.EmitChange(7, "", ChangeEvent{Action: "create", Name: syspath.KeepMarker})

	for d, c := range inside {
		if got := drainAll(c); got["change"] != nil {
			t.Errorf("a client inside %s was told %v", d, got["change"])
		}
	}
	if got := drainAll(root); got["change"] != nil {
		t.Errorf("the storage root was told %v", got["change"])
	}

	// The filter drops only filex's own: an ordinary change still arrives.
	h.EmitChange(7, "", ChangeEvent{Action: "create", Name: "Rapor.docx"})
	if m := drain(t, root); m["type"] != "change" || m["name"] != "Rapor.docx" {
		t.Fatalf("an ordinary change was not delivered: %#v", m)
	}
}

// TestWatchNeverHearsFilexOwnDirectories: the recursive watch the desktop sync
// engine subscribes to is the second delivery EmitChange fans out to, and the
// internal-path filter covers it too. A watch on a storage root hears nothing
// about the trash, version history, thumbnails or the open-with working area —
// at the root or deep inside a folder, inside them or naming them — and still
// hears an ordinary change at once.
//
// ⚠ Merge seam (feat/043-internal × feat/043-sync): the filter is the first
// statement of EmitChange, the watch fan-out the first statement under its
// lock. A filter placed in the room path alone would tell a mirror about
// every trash move and version snapshot as a change to sync.
func TestWatchNeverHearsFilexOwnDirectories(t *testing.T) {
	h := NewHub()
	h.coalesceMin = 0 // every emit goes straight out; nothing is merged away
	h.coalesceMax = 0
	root := NewClient(1, "engine", 64)
	sub := NewClient(2, "engine-docs", 64)
	h.Watch(root, []WatchRoot{{StorageID: 7, Dir: "", Display: "docs://"}})
	h.Watch(sub, []WatchRoot{{StorageID: 7, Dir: "docs", Display: "docs://docs"}})

	for _, d := range syspath.Dirs() {
		for _, parent := range []string{"", "docs", "docs/deep"} {
			inside := d
			if parent != "" {
				inside = parent + "/" + d
			}
			// Inside the directory: a save, a trash move, a snapshot.
			h.EmitChange(7, inside, ChangeEvent{Action: "upload", Name: "a1b2c3d4e5f6-Plan.docx"})
			h.EmitChange(7, inside+"/42", ChangeEvent{Action: "create", Name: "1"})
			h.EmitChange(7, "/"+inside+"/42", ChangeEvent{Action: "delete", Name: "1"})
			// In its parent, naming it.
			h.EmitChange(7, parent, ChangeEvent{Action: "create", Name: d})
			h.EmitChange(7, parent, ChangeEvent{Action: "rename", Name: "x", NewName: d})
			h.EmitChange(7, parent, ChangeEvent{Action: "delete", Name: d})
		}
	}
	h.EmitChange(7, "", ChangeEvent{Action: "create", Name: syspath.KeepMarker})
	h.EmitChange(7, "docs/new", ChangeEvent{Action: "create", Name: syspath.KeepMarker})

	time.Sleep(50 * time.Millisecond) // any trailing flush would have fired by now
	if fr := treeFrames(root); len(fr) != 0 {
		t.Errorf("a watch on the storage root was told %d time(s), first: %v", len(fr), fr[0])
	}
	if fr := treeFrames(sub); len(fr) != 0 {
		t.Errorf("a watch on docs was told %d time(s), first: %v", len(fr), fr[0])
	}

	// The filter drops only filex's own: an ordinary change still arrives,
	// to both watches, relative to each one's root.
	h.EmitChange(7, "docs", ChangeEvent{Action: "upload", Name: "Rapor.docx"})
	if f := waitTree(t, root, time.Second); f["name"] != "Rapor.docx" || len(dirsOf(f)) != 1 || dirsOf(f)[0] != "docs" {
		t.Fatalf("the root watch missed an ordinary change: %#v", f)
	}
	if f := waitTree(t, sub, time.Second); f["name"] != "Rapor.docx" || len(dirsOf(f)) != 1 || dirsOf(f)[0] != "" {
		t.Fatalf("the docs watch missed an ordinary change: %#v", f)
	}
}

func values(m map[string]*Client) []*Client {
	out := make([]*Client, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	return out
}

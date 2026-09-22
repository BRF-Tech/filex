package filesync

import (
	"fmt"
	"testing"
	"time"
)

// staleMirror seeds the shape the field report describes: the server holds the
// real tree, and this machine holds that tree PLUS many files the server no
// longer has (they were cleaned up there), with no baseline to say so.
func staleMirror(t *testing.T, extras int) *rig {
	t.Helper()
	r := newRig(t)
	for i := 0; i < 5; i++ {
		body := fmt.Sprintf("shared %d", i)
		r.writeRemote(fmt.Sprintf("real/f%d.txt", i), body)
		r.writeLocal(fmt.Sprintf("real/f%d.txt", i), body)
	}
	for i := 0; i < extras; i++ {
		r.writeLocal(fmt.Sprintf("copies/c%03d.txt", i), fmt.Sprintf("tidied away on the server %d", i))
	}
	return r
}

func (r *rig) pair() Pair {
	r.t.Helper()
	pairs, err := r.engine.Store.LoadPairs()
	if err != nil {
		r.t.Fatal(err)
	}
	for _, p := range pairs {
		if p.ID == r.engine.Pair.ID {
			return p
		}
	}
	r.t.Fatalf("pair %s not stored", r.engine.Pair.ID)
	return Pair{}
}

// registerPair stores the rig's pair so the engine can persist its hold.
func (r *rig) registerPair() {
	r.t.Helper()
	if err := r.engine.Store.SavePairs([]Pair{r.engine.Pair}); err != nil {
		r.t.Fatal(err)
	}
}

// runStored runs with the pair as stored (the CLI re-reads pairs.json).
func (r *rig) runStored() Result {
	r.t.Helper()
	r.engine.Pair = r.pair()
	return r.run()
}

// H3: a client whose baseline did not know 9,665 files that had been cleaned
// up on the server uploaded every one of them again. A first run that would
// push far more local-only files into a server folder that already has
// content now holds them and asks.
func TestAStaleMirrorHoldsItsExtraFilesInsteadOfUploadingThem(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+50)
	r.registerPair()

	res := r.runStored()

	if res.Uploaded != 0 {
		t.Fatalf("uploaded %d file(s) from a stale mirror", res.Uploaded)
	}
	if res.Held != MassUploadThreshold+50+1 { // the files and the one folder holding them
		t.Fatalf("Held = %d, want %d", res.Held, MassUploadThreshold+51)
	}
	if n := len(r.srv.files); n != 5 {
		t.Fatalf("the server holds %d files, want the 5 it had", n)
	}
	p := r.pair()
	if !p.HoldNew || p.Held != res.Held {
		t.Fatalf("the hold must be recorded on the pair: %+v", p)
	}

	// The next run is not a first run any more, and still holds them.
	res = r.runStored()
	if res.Uploaded != 0 || res.Held != MassUploadThreshold+51 {
		t.Fatalf("second run: %+v", res)
	}
}

func TestConfirmingAHoldUploadsTheFiles(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	n, err := r.engine.Store.ConfirmHeld(r.engine.Pair.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != MassUploadThreshold+2 {
		t.Fatalf("ConfirmHeld reported %d", n)
	}
	res := r.runStored()
	if res.Uploaded != MassUploadThreshold+1 || res.Held != 0 {
		t.Fatalf("after confirm: %+v", res)
	}
	if p := r.pair(); p.HoldNew || p.Held != 0 {
		t.Fatalf("the hold must be cleared: %+v", p)
	}
}

func TestDiscardingAHoldTrashesTheFilesHere(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	moved, kept, err := r.engine.Store.DiscardHeld(r.pair(), r.clock)
	if err != nil {
		t.Fatal(err)
	}
	if moved != MassUploadThreshold+1 || kept != 0 {
		t.Fatalf("DiscardHeld moved %d, kept %d", moved, kept)
	}
	if r.localExists("copies/c000.txt") || r.localExists("copies") {
		t.Fatal("the held files (and the folder that held only them) must be gone from the mirror")
	}
	items, _ := r.engine.Store.ListTrash(r.engine.Pair.ID)
	if len(items) != MassUploadThreshold+1 {
		t.Fatalf("they must be recoverable from the local sync trash: %d item(s)", len(items))
	}
	res := r.runStored()
	if res.Uploaded != 0 || res.Planned != 0 {
		t.Fatalf("after discard the pair is in step: %+v", res)
	}
}

// A file edited after the hold was recorded is not what the person decided
// about: discard leaves it alone.
func TestDiscardLeavesAFileEditedSinceAlone(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	r.touchLocal("copies/c007.txt", "edited after the hold", r.clock.Add(time.Hour))
	moved, kept, err := r.engine.Store.DiscardHeld(r.pair(), r.clock)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 1 || moved != MassUploadThreshold {
		t.Fatalf("moved %d kept %d", moved, kept)
	}
	if got := r.readLocal("copies/c007.txt"); got != "edited after the hold" {
		t.Fatalf("an edited file was touched: %q", got)
	}
}

// While a hold is on, a file that differs between a stale mirror and the
// server is not "resolved" by pushing the stale version over the server's —
// and discarding the hold lets the server's version come down.
func TestAHoldAlsoHoldsFirstRunConflictsAndDiscardLetsTheServerWin(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.writeRemote("real/report.txt", "the server's current version")
	r.writeLocal("real/report.txt", "an old version from the stale mirror")
	r.registerPair()

	res := r.runStored()
	if res.Conflicts != 0 || res.Uploaded != 0 {
		t.Fatalf("a held conflict must touch nothing: %+v", res)
	}
	if got := string(r.srv.files["real/report.txt"]); got != "the server's current version" {
		t.Fatalf("server copy changed: %q", got)
	}
	// Not adopted as settled either: the next run still sees it.
	if res := r.runStored(); res.Held != MassUploadThreshold+3 {
		t.Fatalf("held count after a second run: %+v", res)
	}

	if _, _, err := r.engine.Store.DiscardHeld(r.pair(), r.clock); err != nil {
		t.Fatal(err)
	}
	r.runStored()
	if got := r.readLocal("real/report.txt"); got != "the server's current version" {
		t.Fatalf("after discard the server's version must come down, got %q", got)
	}
}

// Pairing a folder with files to an EMPTY server folder is the obvious intent:
// upload it. No hold.
func TestAFirstRunIntoAnEmptyServerFolderUploadsWithoutAHold(t *testing.T) {
	r := newRig(t)
	for i := 0; i < MassUploadThreshold+50; i++ {
		r.writeLocal(fmt.Sprintf("photos/p%03d.jpg", i), fmt.Sprintf("photo %d", i))
	}
	r.registerPair()

	res := r.runStored()
	if res.Held != 0 || res.Uploaded != MassUploadThreshold+50 {
		t.Fatalf("want every file uploaded and nothing held: %+v", res)
	}
}

// A few local-only files on a first run are ordinary: no hold.
func TestAFewNewFilesOnAFirstRunAreNotHeld(t *testing.T) {
	r := staleMirror(t, 10)
	r.registerPair()

	res := r.runStored()
	if res.Held != 0 || res.Uploaded != 10 {
		t.Fatalf("want 10 uploads, nothing held: %+v", res)
	}
}

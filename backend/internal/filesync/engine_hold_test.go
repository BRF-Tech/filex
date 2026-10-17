package filesync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// The server's trash holds every held file (the server cleaned them up):
// all of them are deleted here, and the folder that held only them.
func TestDiscardingAHoldDeletesTheFilesHere(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), trashOfStaleMirror(MassUploadThreshold+1))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != MassUploadThreshold+1 || len(res.Kept) != 0 || res.StillHeld != 0 {
		t.Fatalf("DiscardHeld deleted %d, kept %d, still held %d", len(res.Deleted), len(res.Kept), res.StillHeld)
	}
	if p := r.pair(); p.HoldNew {
		t.Fatalf("the hold must be over: %+v", p)
	}
	if r.localExists("copies/c000.txt") || r.localExists("copies") {
		t.Fatal("the held files (and the folder that held only them) must be gone from the mirror")
	}
	// Deleted, not moved: no local trash (task #259).
	if _, err := os.Stat(filepath.Join(r.engine.Store.Dir, "trash")); !os.IsNotExist(err) {
		t.Fatalf("a local trash was written: %v", err)
	}
	after := r.runStored()
	if after.Uploaded != 0 || after.Planned != 0 {
		t.Fatalf("after discard the pair is in step: %+v", after)
	}
}

// A file edited after the hold was recorded is not what the person decided
// about: discard leaves it alone - even when the server holds its old bytes
// - and it stays held, so the next run does not upload it either.
func TestDiscardLeavesAFileEditedSinceAlone(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.registerPair()
	r.runStored()

	r.touchLocal("copies/c007.txt", "edited after the hold", r.clock.Add(time.Hour))
	find := trashOfStaleMirror(MassUploadThreshold + 1)
	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), find)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 || len(res.Deleted) != MassUploadThreshold {
		t.Fatalf("deleted %d kept %d", len(res.Deleted), len(res.Kept))
	}
	if res.Kept[0].Rel != "copies/c007.txt" || res.Kept[0].Reason != KeptChanged {
		t.Fatalf("%+v", res.Kept[0])
	}
	for _, rel := range find.asked {
		if rel == "copies/c007.txt" {
			t.Fatal("a file that changed since it was held is not compared")
		}
	}
	if got := r.readLocal("copies/c007.txt"); got != "edited after the hold" {
		t.Fatalf("an edited file was touched: %q", got)
	}
	if !r.pair().HoldNew {
		t.Fatal("the edited file must stay held")
	}
	if res := r.runStored(); res.Uploaded != 0 {
		t.Fatalf("the next run uploaded a held file: %+v", res)
	}
}

// While a hold is on, a file that differs between a stale mirror and the
// server is not "resolved" by pushing the stale version over the server's -
// and discarding the hold lets the server's version come down, when the
// server still has the stale version too (here: in its trash).
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

	find := trashOfStaleMirror(MassUploadThreshold + 1)
	find.at[hexSum("an old version from the stale mirror")] = "trash: docs://real/report.txt"
	if _, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), find); err != nil {
		t.Fatal(err)
	}
	r.runStored()
	if got := r.readLocal("real/report.txt"); got != "the server's current version" {
		t.Fatalf("after discard the server's version must come down, got %q", got)
	}
}

// The same held conflict when the server does NOT have the stale version:
// it is the only copy of those bytes, so it stays - held, the server's
// version not brought down over it - until the person decides otherwise.
func TestADiscardKeepsAHeldConflictTheServerHasNoCopyOf(t *testing.T) {
	r := staleMirror(t, MassUploadThreshold+1)
	r.writeRemote("real/report.txt", "the server's current version")
	r.writeLocal("real/report.txt", "an old version from the stale mirror")
	r.registerPair()
	r.runStored()

	res, err := r.engine.Store.DiscardHeld(context.Background(), r.pair(), trashOfStaleMirror(MassUploadThreshold+1))
	if err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(res, "real/report.txt"); got != KeptNoCopy {
		t.Fatalf("reason = %q", got)
	}
	r.runStored()
	if got := r.readLocal("real/report.txt"); got != "an old version from the stale mirror" {
		t.Fatalf("the only copy of the stale version was replaced: %q", got)
	}
	if got := string(r.srv.files["real/report.txt"]); got != "the server's current version" {
		t.Fatalf("server copy changed: %q", got)
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

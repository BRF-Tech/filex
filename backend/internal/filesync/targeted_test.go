package filesync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func (r *rig) runDirs(dirs ...string) Result {
	r.t.Helper()
	res, err := r.engine.RunDirs(context.Background(), dirs)
	if err != nil {
		r.t.Fatalf("RunDirs(%v): %v", dirs, err)
	}
	if len(res.Errors) > 0 {
		r.t.Logf("RunDirs reported errors: %v", res.Errors)
	}
	return res
}

// deepRig is a pair with a few folders already in step, so a targeted pass has
// something NOT to look at.
func deepRig(t *testing.T) *rig {
	r := newRig(t)
	for _, rel := range []string{"a/b/one.txt", "a/two.txt", "c/three.txt", "c/d/e/four.txt"} {
		r.writeRemote(rel, "seed "+rel)
	}
	r.run()
	return r
}

// The point of a targeted pass: a browser edit three folders down is
// reconciled with ONE server listing, not a walk of the whole tree.
func TestRunDirsReconcilesOneFolderWithOneListing(t *testing.T) {
	r := deepRig(t)
	r.writeRemote("a/b/one.txt", "edited in the browser")

	before := r.srv.lists
	res := r.runDirs("a/b")
	if res.Downloaded != 1 {
		t.Fatalf("want the edited file downloaded, got %+v", res)
	}
	if got := r.readLocal("a/b/one.txt"); got != "edited in the browser" {
		t.Fatalf("local = %q", got)
	}
	if n := r.srv.lists - before; n != 1 {
		t.Fatalf("a one-folder change must cost one listing, cost %d", n)
	}
	if full := r.run(); full.Planned != 0 {
		t.Fatalf("a full pass afterwards must find the pair in step: %+v", full)
	}
}

// Echo: the engine's own upload is announced back by the server. Answering
// that announcement must transfer nothing — the upload's result is already
// what the baseline records.
func TestTheEnginesOwnUploadEchoesBackAsNothing(t *testing.T) {
	r := deepRig(t)
	r.writeLocal("c/three.txt", "saved on the laptop")
	if res := r.runDirs("c"); res.Uploaded != 1 {
		t.Fatalf("seed: %+v", res)
	}
	up, down := r.srv.uploads, r.srv.downloads
	echo := r.runDirs("c") // the tree_change the upload produced
	if echo.Planned != 0 || r.srv.uploads != up || r.srv.downloads != down {
		t.Fatalf("the echo of our own upload moved bytes: %+v (uploads %d→%d, downloads %d→%d)",
			echo, up, r.srv.uploads, down, r.srv.downloads)
	}
}

// A new folder created in the browser (with files in it) is a folder-level
// change: one listing cannot see what is inside, so the pass goes recursive
// for that subtree — and only that subtree.
func TestRunDirsWidensToTheSubtreeForANewFolder(t *testing.T) {
	r := deepRig(t)
	r.writeRemote("a/new/inner/x.txt", "x")
	r.writeRemote("a/new/y.txt", "y")

	res := r.runDirs("a")
	if res.Downloaded != 2 {
		t.Fatalf("the new folder's contents must come down: %+v", res)
	}
	if r.readLocal("a/new/inner/x.txt") != "x" || r.readLocal("a/new/y.txt") != "y" {
		t.Fatal("contents wrong")
	}
	if full := r.run(); full.Planned != 0 {
		t.Fatalf("full pass afterwards: %+v", full)
	}
}

// The announcement named a folder that does not exist here yet: widen to its
// parent, where the new folder shows up as an entry.
func TestRunDirsWidensToTheParentForAFolderMissingHere(t *testing.T) {
	r := deepRig(t)
	r.writeRemote("c/fresh/z.txt", "z")
	res := r.runDirs("c/fresh")
	if res.Downloaded != 1 || r.readLocal("c/fresh/z.txt") != "z" {
		t.Fatalf("got %+v", res)
	}
}

// A folder deleted in the browser: the targeted pass on its parent goes
// recursive and trashes it here, exactly as a full pass would.
func TestRunDirsCarriesAFolderDelete(t *testing.T) {
	r := deepRig(t)
	delete(r.srv.files, "c/d/e/four.txt")
	delete(r.srv.dirs, "c/d/e")
	delete(r.srv.dirs, "c/d")

	res := r.runDirs("c")
	if r.localExists("c/d") {
		t.Fatalf("the deleted folder is still here: %+v", res)
	}
	items, _ := r.engine.Store.ListTrash("p1")
	if len(items) == 0 {
		t.Fatal("the local copy must be in the sync trash, not gone")
	}
}

// Without history a targeted pass is not enough: the first run merges both
// trees and must see all of them.
func TestRunDirsWithoutHistoryIsAFullMerge(t *testing.T) {
	r := newRig(t)
	r.writeRemote("far/away.txt", "x")
	r.writeLocal("local.txt", "y")
	res := r.runDirs("elsewhere")
	if !res.FirstRun || res.Downloaded != 1 || res.Uploaded != 1 {
		t.Fatalf("want a first-run merge, got %+v", res)
	}
}

// The engine's own downloads raise file-system events; they must be
// recognised from the disk alone, without a server round-trip.
func TestLocalDirChangedIgnoresTheEnginesOwnWrites(t *testing.T) {
	r := deepRig(t)
	r.writeRemote("a/b/one.txt", "new")
	r.runDirs("a/b")

	changed, err := r.engine.LocalDirChanged("a/b")
	if err != nil || changed {
		t.Fatalf("the engine's own download must not read as a local change: %v %v", changed, err)
	}
	r.writeLocal("a/b/one.txt", "a person's edit")
	if changed, _ := r.engine.LocalDirChanged("a/b"); !changed {
		t.Fatal("a genuine edit must read as a change")
	}
	if err := os.Remove(filepath.Join(r.dir, "a", "two.txt")); err != nil {
		t.Fatal(err)
	}
	if changed, _ := r.engine.LocalDirChanged("a"); !changed {
		t.Fatal("a local delete must read as a change")
	}
}

// Names the engine never syncs are dropped before any work is done.
func TestRunDirsIgnoresTheStateFolder(t *testing.T) {
	r := deepRig(t)
	before := r.srv.lists
	r.runDirs(".filex-sync/baseline", "c/$RECYCLE.BIN/x")
	if r.srv.lists != before {
		t.Fatalf("a path the engine never syncs cost %d listing(s)", r.srv.lists-before)
	}
}

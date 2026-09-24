package filesync

import (
	"path"
	"testing"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestFilexReservedNamesAreNeverSynced: a synced folder that happens to hold
// something named like filex's own directories (`.versions`, `.filex-trash`,
// `.thumbs`, `.filex-open`) or its keep marker keeps it on this computer. The
// engine neither uploads it nor creates the folder on the server.
//
// ⚠ Merge seam (feat/043-sync × feat/043-internal): the engine predates
// syspath, and the server now refuses every write into those names
// (writegate). Before the engine adopted syspath the local `.versions` below
// was uploaded on every pass and answered 403 every time — an error under the
// folder the person could never clear.
func TestFilexReservedNamesAreNeverSynced(t *testing.T) {
	r := newRig(t)
	var attempted []string
	r.srv.beforeUpload = func(rel string) { attempted = append(attempted, rel) }

	var reserved []string
	for _, d := range syspath.Dirs() {
		reserved = append(reserved, d+"/inside.txt", "docs/"+d+"/nested.txt")
	}
	reserved = append(reserved, syspath.KeepMarker, "docs/"+syspath.KeepMarker)
	for _, rel := range reserved {
		r.writeLocal(rel, "not sync material")
	}
	r.writeLocal("real.txt", "user file")
	r.writeLocal("docs/a.txt", "user file")

	res := r.run()
	if len(res.Errors) > 0 {
		t.Fatalf("a pass over reserved names reported errors: %v", res.Errors)
	}
	for _, rel := range attempted {
		if syspath.Hidden(rel) {
			t.Errorf("the engine tried to upload %s", rel)
		}
	}
	for rel := range r.srv.files {
		if syspath.Hidden(rel) {
			t.Errorf("%s reached the server", rel)
		}
	}
	for rel := range r.srv.dirs {
		if rel != "" && syspath.InDir(rel) {
			t.Errorf("the engine created the folder %s on the server", rel)
		}
	}
	for _, rel := range []string{"real.txt", "docs/a.txt"} {
		if _, ok := r.srv.files[rel]; !ok {
			t.Errorf("%s next to them should still have synced", rel)
		}
	}

	// The watcher's and the targeted pass's questions give the same answer.
	for _, d := range append(syspath.Dirs(), syspath.KeepMarker) {
		if !IgnoredName(d) {
			t.Errorf("IgnoredName(%q) = false", d)
		}
		if syncable(path.Join("docs", d, "x")) {
			t.Errorf("syncable accepted a path through %s", d)
		}
	}
	if IgnoredName("versions") || IgnoredName("my.thumbs.txt") || !syncable("docs/.versionsX/a") {
		t.Error("an ordinary name that merely resembles a reserved one was ignored")
	}
}

package filesync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests are about the world moving WHILE a pass runs. With changes
// announced the moment they happen, a pass is usually still running when the
// next change lands — a browser autosave, a desktop editor saving the same
// file in the same second. Every test here was red on the engine as it was
// before the ledger (engine.go/ledger.go): the settle walk folded whatever it
// found into the baseline as "agreed".

// sideCopies lists the "(server copy …)" files next to rel.
func (r *rig) sideCopies(rel string) []string {
	r.t.Helper()
	dir := filepath.Join(r.dir, filepath.FromSlash(filepath.Dir(rel)))
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	m, _ := filepath.Glob(filepath.Join(dir, stem+" (server copy*"))
	return m
}

// A browser save that lands while a pass is already running must reach the
// disk — it used to be written into the baseline as agreed and never
// downloaded.
func TestABrowserEditDuringARunIsStillDownloaded(t *testing.T) {
	r := newRig(t)
	r.writeRemote("a.txt", "a1")
	r.writeRemote("b.txt", "b1")
	r.run()

	r.writeRemote("a.txt", "a2 — the edit this pass is for")
	r.srv.afterTransfer = func() {
		// While a.txt comes down, somebody saves b.txt in the browser.
		r.srv.files["b.txt"] = []byte("b2 — saved during the pass")
		r.srv.clock += 1000
		r.srv.mod["b.txt"] = r.srv.clock
		r.srv.afterTransfer = nil
	}
	r.run()
	r.run()

	if got := r.readLocal("b.txt"); got != "b2 — saved during the pass" {
		t.Fatalf("the edit made during a pass never arrived: local b.txt = %q", got)
	}
}

// Same shape, local side: a save on this computer during a pass must go up.
func TestALocalEditDuringARunIsStillUploaded(t *testing.T) {
	r := newRig(t)
	r.writeRemote("a.txt", "a1")
	r.writeLocal("c.txt", "c1")
	r.run()

	r.writeRemote("a.txt", "a2")
	r.srv.afterTransfer = func() {
		r.srv.afterTransfer = nil
		r.writeLocal("c.txt", "c2 — saved on the laptop during the pass")
	}
	r.run()
	r.run()

	if got := string(r.srv.files["c.txt"]); got != "c2 — saved on the laptop during the pass" {
		t.Fatalf("the local edit made during a pass never went up: server c.txt = %q", got)
	}
}

// A download that FAILED for a changed file must be retried, not recorded.
func TestAFailedDownloadOfAChangedFileIsRetried(t *testing.T) {
	r := newRig(t)
	r.writeRemote("x.txt", "v1")
	r.run()

	r.writeRemote("x.txt", "v2")
	r.srv.fail["x.txt"] = errors.New("connection reset")
	first := r.run()
	if len(first.Errors) != 1 {
		t.Fatalf("the failure must be reported: %+v", first)
	}
	r.run()
	if got := r.readLocal("x.txt"); got != "v2" {
		t.Fatalf("a failed download was recorded as done: local = %q", got)
	}
}

// ⚠⚠ The race the owner will hit: the browser save triggers a pass, the
// pass downloads, and in that same second a desktop editor saves the local
// file. Replacing it would erase the local edit without a trace. The download
// must stand down, and the next pass keeps both versions.
func TestAnInstantPullNeverOverwritesAnUnsyncedLocalEdit(t *testing.T) {
	r := newRig(t)
	r.writeRemote("note.txt", "v1")
	r.run()

	r.writeRemote("note.txt", "v2 from the browser")
	r.engine.beforeReplace = func(dest string) {
		r.engine.beforeReplace = nil
		r.writeLocal("note.txt", "v2 from the desktop editor")
	}
	first := r.run()
	if got := r.readLocal("note.txt"); got != "v2 from the desktop editor" {
		t.Fatalf("the pull overwrote an unsynced local edit: %q", got)
	}
	if len(first.Raced) != 1 || !strings.Contains(first.Raced[0], ErrLocalChanged.Error()) || len(first.Errors) != 0 {
		t.Fatalf("the stand-down must be reported as a race, not an error: raced=%v errors=%v", first.Raced, first.Errors)
	}

	second := r.run()
	if second.Conflicts != 1 {
		t.Fatalf("both edits must be kept as a conflict next pass: %+v", second)
	}
	if got := r.readLocal("note.txt"); got != "v2 from the desktop editor" {
		t.Fatalf("the local edit must keep its name: %q", got)
	}
	side := r.sideCopies("note.txt")
	if len(side) != 1 {
		t.Fatalf("want the browser's version beside it, got %v", side)
	}
	if b, _ := os.ReadFile(side[0]); string(b) != "v2 from the browser" {
		t.Fatalf("the side copy must hold the browser's version: %q", b)
	}
	if got := string(r.srv.files["note.txt"]); got != "v2 from the desktop editor" {
		t.Fatalf("server = %q", got)
	}
}

// The mirror image: the local save is uploaded, and the browser saves the
// same file between the pass's listing and the upload. The upload carries the
// signature it planned from; the server refuses it; nothing is overwritten;
// the next pass keeps both.
func TestAnUploadNeverOverwritesABrowserSaveItDidNotSee(t *testing.T) {
	r := newRig(t)
	r.writeRemote("note.txt", "v1")
	r.run()

	r.writeLocal("note.txt", "desktop edit")
	r.srv.beforeUpload = func(rel string) {
		r.srv.beforeUpload = nil
		r.srv.files[rel] = []byte("browser edit, same second")
		r.srv.clock += 1000
		r.srv.mod[rel] = r.srv.clock
	}
	first := r.run()
	if r.srv.refused != 1 {
		t.Fatalf("the stale upload must be refused by the server, refused=%d", r.srv.refused)
	}
	if got := string(r.srv.files["note.txt"]); got != "browser edit, same second" {
		t.Fatalf("the upload overwrote the browser's save: %q", got)
	}
	if len(first.Raced) != 1 || len(first.Errors) != 0 {
		t.Fatalf("the refusal must be reported as a race: raced=%v errors=%v", first.Raced, first.Errors)
	}

	second := r.run()
	if second.Conflicts != 1 {
		t.Fatalf("the next pass must keep both: %+v", second)
	}
	side := r.sideCopies("note.txt")
	if len(side) != 1 {
		t.Fatalf("the browser's edit must survive beside the local file, got %v", side)
	}
	if b, _ := os.ReadFile(side[0]); string(b) != "browser edit, same second" {
		t.Fatalf("side copy = %q", b)
	}
}

// ⭐ A delete never beats an edit — also when the edit lands after the plan.
func TestALocalEditRescuesAFileFromAPlannedDelete(t *testing.T) {
	r := newRig(t)
	r.writeRemote("d.txt", "doomed")
	r.writeRemote("z.txt", "z1")
	r.run()

	delete(r.srv.files, "d.txt")
	r.writeRemote("z.txt", "z2")
	r.srv.afterTransfer = func() {
		// z.txt's download runs before the deletes; the person edits d.txt now.
		r.srv.afterTransfer = nil
		r.writeLocal("d.txt", "rescued by an edit")
	}
	r.run()
	if !r.localExists("d.txt") {
		t.Fatal("a file edited after the plan was moved to the trash")
	}
	r.run()
	if got := string(r.srv.files["d.txt"]); got != "rescued by an edit" {
		t.Fatalf("the rescued edit must go back up: %q", got)
	}
}

// A folder deleted on the server is not trashed on this computer if a file
// was saved into it after the plan — the folder would take the file with it.
func TestAFolderDeleteSparesAFileSavedIntoItMeanwhile(t *testing.T) {
	r := newRig(t)
	r.writeRemote("old/a.txt", "a")
	r.writeRemote("z.txt", "z1")
	r.run()

	delete(r.srv.files, "old/a.txt")
	delete(r.srv.dirs, "old")
	r.writeRemote("z.txt", "z2")
	r.srv.afterTransfer = func() {
		r.srv.afterTransfer = nil
		r.writeLocal("old/new.txt", "saved into the folder meanwhile")
	}
	r.run()
	if !r.localExists("old/new.txt") {
		t.Fatal("the folder delete took a freshly saved file with it")
	}
	r.run()
	if got := string(r.srv.files["old/new.txt"]); got != "saved into the folder meanwhile" {
		t.Fatalf("the new file must reach the server: %q", got)
	}
}

// Two conflicts on one file inside the same minute get two side copies;
// the second must not overwrite the first one's rescued version.
func TestTwoConflictsInOneMinuteKeepEveryVersion(t *testing.T) {
	r := newRig(t)
	r.writeRemote("n.txt", "v1")
	r.run()
	fixed := r.clock

	for i := 1; i <= 2; i++ {
		lp := filepath.Join(r.dir, "n.txt")
		if err := os.WriteFile(lp, []byte(fmt.Sprintf("local %d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := fixed.Add(time.Duration(i) * time.Hour)
		_ = os.Chtimes(lp, stamp, stamp)
		r.clock = fixed // both conflicts stamped with the same minute
		r.writeRemote("n.txt", fmt.Sprintf("server %d", i))
		if res := r.run(); res.Conflicts != 1 {
			t.Fatalf("round %d: want a conflict, got %+v", i, res)
		}
	}
	side := r.sideCopies("n.txt")
	if len(side) != 2 {
		t.Fatalf("want both rescued server versions, got %v", side)
	}
	got := map[string]bool{}
	for _, p := range side {
		b, _ := os.ReadFile(p)
		got[string(b)] = true
	}
	if !got["server 1"] || !got["server 2"] {
		t.Fatalf("a side copy was overwritten: %v", got)
	}
}

// A server whose upload answer carries no listing (an older one, or the staged
// path) still ends in step: the folder is listed once for the signature, and
// the next pass has nothing to do — no re-upload, no conflict with itself.
func TestAnUploadWithoutAnAnswerIsResolvedByOneListing(t *testing.T) {
	r := newRig(t)
	r.srv.noUploadEcho = true
	r.writeLocal("docs/a.txt", "a")
	r.run()
	res := r.run()
	if res.Planned != 0 {
		t.Fatalf("pair must be in step after the listing resolved the upload: %+v", res)
	}
}

// Uploading an edited file into a folder the server already has must not
// cost a mkdir request first (it used to: one ignored mkdir per upload).
func TestUploadIntoAKnownFolderIssuesNoMkdir(t *testing.T) {
	r := newRig(t)
	r.writeRemote("docs/a.txt", "a1")
	r.run()
	r.writeLocal("docs/a.txt", "a2")
	before := r.srv.mkdirs
	r.run()
	if n := r.srv.mkdirs - before; n != 0 {
		t.Fatalf("an upload into an existing folder issued %d mkdir request(s)", n)
	}
	if got := string(r.srv.files["docs/a.txt"]); got != "a2" {
		t.Fatalf("server = %q", got)
	}
}

// An older server ignores the precondition and overwrites — the pre-v0.43
// behaviour, pinned so the fake's "old server" mode stays honest.
func TestAnOlderServerStillAcceptsTheUpload(t *testing.T) {
	r := newRig(t)
	r.srv.ignoreExpect = true
	r.writeRemote("a.txt", "v1")
	r.run()
	r.writeLocal("a.txt", "v2")
	r.run()
	if got := string(r.srv.files["a.txt"]); got != "v2" {
		t.Fatalf("server = %q", got)
	}
}

package filesync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// preparingJSON is, byte for byte in shape, what 45 files on a real deployment
// held under their own names after a v0.20–v0.42 server answered a download of
// a big file on a slow storage with 202 and the client wrote the answer to disk.
const preparingJSON = `{"name":"poster v02.psd","percent":0,"ready":false,"size":151983227,"state":"preparing"}`

// leftovers lists the engine's temporary download files in the pair folder.
func (r *rig) leftovers() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".filex-part-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// H1: the listing is the contract. A body of any other length is never
// installed under the file's name — so a later run cannot mistake it for a
// local edit and upload it over the real file.
func TestABodyThatIsNotTheListedFileIsNeverInstalled(t *testing.T) {
	r := newRig(t)
	real := strings.Repeat("8BPS", 250)
	r.writeRemote("poster.psd", real)
	r.srv.served = map[string][]byte{"poster.psd": []byte(preparingJSON)}

	res := r.run()

	if r.localExists("poster.psd") {
		t.Fatalf("a %d-byte body was installed for a file listed at %d bytes", len(preparingJSON), len(real))
	}
	if len(res.Errors) == 0 {
		t.Fatal("the refused download must be reported, not swallowed")
	}
	if left := r.leftovers(); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}

	// Nothing is left for the next run to upload over the server's copy.
	res = r.run()
	if res.Uploaded != 0 {
		t.Fatalf("uploaded %d file(s) after a refused download", res.Uploaded)
	}
	if got := string(r.srv.files["poster.psd"]); got != real {
		t.Fatalf("the server's copy changed: %q", got)
	}

	// Once the server sends the file, it arrives.
	delete(r.srv.served, "poster.psd")
	r.run()
	if got := r.readLocal("poster.psd"); got != real {
		t.Fatalf("after the server recovered: got %d bytes, want %d", len(got), len(real))
	}
}

// H1, the conflict half of the chain: a conflict downloads the server's copy
// first. If that body is wrong, the local file must NOT go up over the server's.
func TestAConflictWhoseServerCopyIsWrongUploadsNothing(t *testing.T) {
	r := newRig(t)
	r.writeLocal("report.txt", "v1")
	r.run()

	r.writeLocal("report.txt", "my edit")
	r.writeRemote("report.txt", "their edit")
	r.srv.served = map[string][]byte{"report.txt": []byte(preparingJSON)}
	res := r.run()

	if got := string(r.srv.files["report.txt"]); got != "their edit" {
		t.Fatalf("the server's copy was overwritten although its download failed: %q", got)
	}
	if res.Uploaded != 0 {
		t.Fatalf("uploaded %d file(s)", res.Uploaded)
	}
	if got := r.readLocal("report.txt"); got != "my edit" {
		t.Fatalf("the local file changed: %q", got)
	}
}

// ─────────────────────────── H2: conflicts compare content ───────────────────────────

// localNames lists the files in the pair folder that contain marker.
func (r *rig) localNames(marker string) []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.Contains(e.Name(), marker) {
			out = append(out, e.Name())
		}
	}
	return out
}

// touchLocal writes rel WITHOUT moving the rig's clock, so the engine's "now"
// (and with it the minute in a conflict copy's name) stays where it was.
func (r *rig) touchLocal(rel, body string, mod time.Time) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	_ = os.Chtimes(p, mod, mod)
}

// No history, the same bytes on both sides, clocks far apart: the shape a
// reinstalled client or a lost baseline leaves. It used to be a conflict copy
// per file per round; it is simply the same file.
func TestIdenticalTwinsAreAdoptedNotConflicted(t *testing.T) {
	r := newRig(t)
	r.writeLocal("budget.xlsx", "the same bytes")
	r.writeRemote("budget.xlsx", "the same bytes")

	res := r.run()

	if res.Conflicts != 0 || res.Identical != 1 || res.Uploaded != 0 {
		t.Fatalf("want 0 conflicts, 1 identical, 0 uploads; got %+v", res)
	}
	if copies := r.localNames("copy"); len(copies) != 0 {
		t.Fatalf("identical files produced conflict copies: %v", copies)
	}
	if n := len(r.srv.files); n != 1 {
		t.Fatalf("the server holds %d files, want 1", n)
	}
	if again := r.run(); again.Planned != 0 {
		t.Fatalf("an adopted twin must be settled for good: %+v", again)
	}
}

func TestBothSidesChangedToTheSameBytesIsNotAConflict(t *testing.T) {
	r := newRig(t)
	r.writeLocal("a.txt", "v1")
	r.run()

	r.writeLocal("a.txt", "v2")
	r.writeRemote("a.txt", "v2")
	res := r.run()

	if res.Conflicts != 0 || res.Identical != 1 || res.Uploaded != 0 {
		t.Fatalf("want 0 conflicts, 1 identical, 0 uploads; got %+v", res)
	}
	if again := r.run(); again.Planned != 0 {
		t.Fatalf("must be settled: %+v", again)
	}
}

// A conflict on a conflict copy used to add a second marker to the name —
// `X (server copy A) (server copy B)` — and one busy file grew 14,724 of them.
func TestAConflictOnAConflictCopyIsNotNested(t *testing.T) {
	r := newRig(t)
	name := "sheet (server copy 2026-08-01 09-00).xlsx"
	r.writeLocal(name, "v1")
	r.run()

	r.writeLocal(name, "mine")
	r.writeRemote(name, "theirs")
	res := r.run()

	if res.Conflicts != 1 {
		t.Fatalf("want one conflict, got %+v", res)
	}
	for _, n := range r.localNames("(server copy") {
		if c := strings.Count(n, "(server copy"); c > 1 {
			t.Fatalf("nested conflict copy name: %q", n)
		}
	}
	if len(r.localNames("sheet (server copy")) != 2 {
		t.Fatalf("want the original and one fresh copy, have %v", r.localNames("sheet"))
	}
}

// Two conflicts on one file inside the same minute: the second copy must not
// land on the first one's name.
func TestTwoConflictsInOneMinuteKeepBothServerCopies(t *testing.T) {
	r := newRig(t)
	r.writeLocal("r.txt", "v1")
	r.run()

	r.writeLocal("r.txt", "mine 1")
	r.writeRemote("r.txt", "theirs 1")
	r.run()

	r.touchLocal("r.txt", "mine 2", r.clock.Add(10*time.Second))
	r.writeRemote("r.txt", "theirs 2")
	r.run()

	got := map[string]bool{}
	for _, n := range r.localNames("r (server copy") {
		got[r.readLocal(n)] = true
	}
	if !got["theirs 1"] || !got["theirs 2"] {
		t.Fatalf("both server versions must survive as copies; have %v", r.localNames("r ("))
	}
}

// ─────────────────────────── H3: a copy is on both sides and remembered ───────────────────────────

func TestAConflictCopyIsOnBothSidesAndRemembered(t *testing.T) {
	r := newRig(t)
	r.writeLocal("report.txt", "v1")
	r.run()
	r.writeLocal("report.txt", "my edit")
	r.writeRemote("report.txt", "their edit")
	r.run()

	sides := r.localNames("report (server copy")
	if len(sides) != 1 {
		t.Fatalf("want one conflict copy, have %v", sides)
	}
	side := sides[0]
	if got := string(r.srv.files[side]); got != "their edit" {
		t.Fatalf("the copy must reach the server in the same action, server has %q", got)
	}
	rows, _, _ := r.engine.Store.LoadBaseline("p1")
	if _, ok := rows[side]; !ok {
		t.Fatalf("the copy must be in the baseline at once; rows=%v", rows)
	}

	// Somebody tidies the copy away on the server. It must stay gone.
	delete(r.srv.files, side)
	res := r.run()
	if res.Uploaded != 0 {
		t.Fatalf("a copy deleted on the server came back: %+v", res)
	}
	if r.localExists(side) {
		t.Fatal("the server's delete must be followed here (into the local sync trash)")
	}
}

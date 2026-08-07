package filesync

import (
	"testing"
	"time"
)

func file(rel string, size, mod int64) Node {
	return Node{Rel: rel, Size: size, ModMillis: mod}
}

func dir(rel string) Node { return Node{Rel: rel, IsDir: true} }

func snap(nodes ...Node) Snapshot {
	s := Snapshot{}
	for _, n := range nodes {
		s[n.Rel] = n
	}
	return s
}

// find returns the action for rel, or a zero Action.
func find(acts []Action, rel string) Action {
	for _, a := range acts {
		if a.Rel == rel {
			return a
		}
	}
	return Action{}
}

func TestPlanCopiesNewFilesBothWays(t *testing.T) {
	local := snap(file("mine.txt", 10, 1000))
	remote := snap(file("theirs.txt", 20, 2000))

	acts := Plan(local, remote, Baseline{}, Options{FirstRun: true})

	if got := find(acts, "mine.txt").Kind; got != ActionUpload {
		t.Errorf("mine.txt: want upload, got %q", got)
	}
	if got := find(acts, "theirs.txt").Kind; got != ActionDownload {
		t.Errorf("theirs.txt: want download, got %q", got)
	}
}

func TestPlanIsQuietWhenNothingMoved(t *testing.T) {
	l := file("a.txt", 10, 1000)
	r := file("a.txt", 10, 5555) // different mtime — that is NORMAL after upload
	base := Baseline{"a.txt": {Local: l.Signature(), Remote: r.Signature()}}

	acts := Plan(snap(l), snap(r), base, Options{})

	if len(acts) != 0 {
		t.Fatalf("expected no work, got %+v", acts)
	}
}

// The single most important property: mtimes across the two sides are never
// compared to each other, only to that side's own baseline.
func TestPlanNeverComparesClocksAcrossSides(t *testing.T) {
	// Remote mtime is a decade ahead; nothing changed since the baseline.
	l := file("a.txt", 10, 1_000)
	r := file("a.txt", 10, 9_999_999_999)
	base := Baseline{"a.txt": {Local: l.Signature(), Remote: r.Signature()}}

	if acts := Plan(snap(l), snap(r), base, Options{}); len(acts) != 0 {
		t.Fatalf("clock skew must not create work, got %+v", acts)
	}
}

func TestPlanPushesOneSidedChanges(t *testing.T) {
	base := Baseline{
		"up.txt":   {Local: "10:1000", Remote: "10:2000"},
		"down.txt": {Local: "10:1000", Remote: "10:2000"},
	}
	local := snap(file("up.txt", 99, 7000), file("down.txt", 10, 1000))
	remote := snap(file("up.txt", 10, 2000), file("down.txt", 77, 8000))

	acts := Plan(local, remote, base, Options{})

	if got := find(acts, "up.txt").Kind; got != ActionUpload {
		t.Errorf("up.txt: want upload, got %q", got)
	}
	if got := find(acts, "down.txt").Kind; got != ActionDownload {
		t.Errorf("down.txt: want download, got %q", got)
	}
}

func TestPlanKeepsBothCopiesOnAConflict(t *testing.T) {
	base := Baseline{"report.xlsx": {Local: "10:1000", Remote: "10:2000"}}
	local := snap(file("report.xlsx", 50, 7000))
	remote := snap(file("report.xlsx", 60, 8000))

	a := find(Plan(local, remote, base, Options{Now: time.Date(2026, 8, 7, 14, 5, 0, 0, time.UTC)}), "report.xlsx")

	if a.Kind != ActionConflict {
		t.Fatalf("want conflict, got %q", a.Kind)
	}
	if want := "report (server copy 2026-08-07 14-05).xlsx"; a.ConflictName != want {
		t.Errorf("conflict name = %q, want %q", a.ConflictName, want)
	}
}

// Same size on both sides is not evidence of same content.
func TestPlanConflictsEvenWhenSizesMatch(t *testing.T) {
	base := Baseline{"a.bin": {Local: "10:1000", Remote: "10:2000"}}
	local := snap(file("a.bin", 42, 7000))
	remote := snap(file("a.bin", 42, 8000))

	if got := find(Plan(local, remote, base, Options{}), "a.bin").Kind; got != ActionConflict {
		t.Errorf("want conflict, got %q", got)
	}
}

func TestPlanPropagatesDeletes(t *testing.T) {
	base := Baseline{
		"gone-remote.txt": {Local: "10:1000", Remote: "10:2000"},
		"gone-local.txt":  {Local: "10:1000", Remote: "10:2000"},
	}
	local := snap(file("gone-local.txt", 10, 1000))
	remote := snap(file("gone-remote.txt", 10, 2000))

	acts := Plan(local, remote, base, Options{})

	if got := find(acts, "gone-local.txt").Kind; got != ActionDeleteLocal {
		t.Errorf("removed on the server → delete locally, got %q", got)
	}
	if got := find(acts, "gone-remote.txt").Kind; got != ActionDeleteRemot {
		t.Errorf("removed locally → delete on the server, got %q", got)
	}
}

// ⚠ The rule that stops sync from eating someone's work.
func TestPlanNeverLetsADeleteBeatAnEdit(t *testing.T) {
	base := Baseline{
		"edited-here.txt":  {Local: "10:1000", Remote: "10:2000"},
		"edited-there.txt": {Local: "10:1000", Remote: "10:2000"},
	}
	// Deleted on the server, but edited locally since the baseline.
	local := snap(file("edited-here.txt", 999, 7000))
	// Deleted locally, but edited on the server since the baseline.
	remote := snap(file("edited-there.txt", 999, 8000))

	acts := Plan(local, remote, base, Options{})

	if got := find(acts, "edited-here.txt").Kind; got != ActionUpload {
		t.Errorf("local edit must survive a remote delete, got %q", got)
	}
	if got := find(acts, "edited-there.txt").Kind; got != ActionDownload {
		t.Errorf("remote edit must survive a local delete, got %q", got)
	}
}

// A first run has no baseline, so "missing here" is indistinguishable from
// "you deleted it". Guessing wrong empties a folder; the first run merges.
func TestFirstRunNeverDeletesAnything(t *testing.T) {
	base := Baseline{"a.txt": {Local: "10:1000", Remote: "10:2000"}}
	local := snap(file("a.txt", 10, 1000))
	remote := Snapshot{}

	for _, a := range Plan(local, remote, base, Options{FirstRun: true}) {
		if isDelete(a.Kind) {
			t.Fatalf("first run produced %s on %s", a.Kind, a.Rel)
		}
	}
}

func TestPlanCreatesFoldersBeforeTheirContents(t *testing.T) {
	local := snap(dir("docs"), dir("docs/2026"), file("docs/2026/a.txt", 10, 1000))
	acts := Plan(local, Snapshot{}, Baseline{}, Options{FirstRun: true})

	pos := map[string]int{}
	for i, a := range acts {
		pos[a.Rel] = i
	}
	if pos["docs"] > pos["docs/2026"] || pos["docs/2026"] > pos["docs/2026/a.txt"] {
		t.Fatalf("wrong order: %v", acts)
	}
}

// Removing a folder fails while its children are still there.
func TestPlanDeletesDeepestFirst(t *testing.T) {
	base := Baseline{
		"docs":            {Local: "dir", Remote: "dir", IsDir: true},
		"docs/2026":       {Local: "dir", Remote: "dir", IsDir: true},
		"docs/2026/a.txt": {Local: "10:1000", Remote: "10:2000"},
	}
	remote := snap(dir("docs"), dir("docs/2026"), file("docs/2026/a.txt", 10, 2000))

	acts := Plan(Snapshot{}, remote, base, Options{})

	var order []string
	for _, a := range acts {
		if isDelete(a.Kind) {
			order = append(order, a.Rel)
		}
	}
	if len(order) != 3 || order[0] != "docs/2026/a.txt" || order[2] != "docs" {
		t.Fatalf("deletes must run deepest-first, got %v", order)
	}
}

// A file where the other side has a folder is the collision the server-side
// kind guard refuses. Neither side wins here either.
func TestPlanRefusesAFileOverAFolder(t *testing.T) {
	local := snap(file("thing", 10, 1000))
	remote := snap(dir("thing"))

	a := find(Plan(local, remote, Baseline{}, Options{}), "thing")
	if a.Kind != ActionConflict {
		t.Fatalf("want conflict, got %q", a.Kind)
	}
}

func TestNextBaselineOmitsUnsettledPaths(t *testing.T) {
	local := snap(file("ok.txt", 10, 1000), file("only-here.txt", 10, 1000))
	remote := snap(file("ok.txt", 10, 2000))

	b := NextBaseline(local, remote)

	if _, ok := b["ok.txt"]; !ok {
		t.Error("a path present on both sides should be recorded")
	}
	if _, ok := b["only-here.txt"]; ok {
		t.Error("a one-sided path must NOT be recorded as settled")
	}
}

func TestNextBaselineRoundTripsToNoWork(t *testing.T) {
	local := snap(file("a.txt", 10, 1000), dir("d"))
	remote := snap(file("a.txt", 10, 9999), dir("d"))

	if acts := Plan(local, remote, NextBaseline(local, remote), Options{}); len(acts) != 0 {
		t.Fatalf("a baseline taken from these snapshots must produce no work, got %+v", acts)
	}
}

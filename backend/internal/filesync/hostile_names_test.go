package filesync

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/localname"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// The hostile names (task #238) on a pair, under the name rules of each
// operating system the desktop app runs on (Engine.GOOS: the suite runs on
// Linux, and the Windows and macOS rules are what break):
//
//   - every server item arrives here: under its own name when this computer
//     holds it, under an alias (localname.Alias, a " (2)" for the second of
//     two twins) when it does not - reported, and the same on every pass;
//   - nothing on the server is deleted, uploaded, created or renamed because
//     of a name, on any pass;
//   - a change here to an alias reaches the server's ORIGINAL name, and a
//     change on the server reaches the alias.
//
// Up to 0.56, on the Windows rules: `report.` came down as `report` and the
// next pass deleted the server's `report.` and uploaded `report`; of the case
// twins the second was fetched again on every pass, for ever.

// serverState is the fake server's content, for "nothing changed" checks.
type serverState struct {
	files map[string]string
	dirs  map[string]bool
}

func stateOf(f *fakeServer) serverState {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := serverState{files: map[string]string{}, dirs: map[string]bool{}}
	for k, v := range f.files {
		s.files[k] = string(v)
	}
	for k, v := range f.dirs {
		s.dirs[k] = v
	}
	return s
}

// hostileOnServer is every hostile name a server can list: all of them but
// `..`, which is not a name, and the NUL one, which no file system stores.
// The other names the server now refuses to CREATE (a control character, a
// backslash, `...`) are still listed: an older server took them, and so does
// a disk an administrator copies into a storage.
func hostileOnServer() []hostilenames.Name {
	var out []hostilenames.Name
	for _, n := range hostilenames.All() {
		if n.ID == "dot-dot" || n.ID == "nul" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// aliasedOn says whether goos's rules give n a name of its own here, from the
// list's own flags (not from localname, which is what is being tested), and
// for the second of two twins which twin keeps the name.
func aliasedOn(n hostilenames.Name, goos string) (aliased bool, why string) {
	switch {
	case goos == "windows" && n.WindowsInvalid:
		return true, "windows"
	case goos != "windows" && n.Bytes() > localname.MaxLen:
		return true, localname.TooLong
	}
	twins := goos == "darwin" || (goos == "windows" && n.TwinKind == hostilenames.TwinCase)
	if n.Twin != "" && twins && hostilenames.Get(n.Twin).Name < n.Name {
		// A first pass gives the name to the twin first in byte order.
		return true, WhyTwin
	}
	return false, ""
}

// splitNTFSOnly takes out of names, on the Windows rules, the ones only a
// Windows disk holds: over the 255 bytes ext4 holds, within the 255 UTF-16
// units NTFS does (bytes-256, chars-under-bytes-over). The Windows rules
// keep their names, so the engine writes them as they are, and on the Linux
// disk this test runs on that is ENAMETOOLONG - the test machine's disk, not
// a rule of the engine's.
func splitNTFSOnly(names []hostilenames.Name, goos string) (disk, ntfsOnly []hostilenames.Name) {
	for _, n := range names {
		if goos == "windows" && runtime.GOOS != "windows" && n.Bytes() > localname.MaxLen {
			ntfsOnly = append(ntfsOnly, n)
			continue
		}
		disk = append(disk, n)
	}
	return disk, ntfsOnly
}

func namesOf(reports []NameReport) map[string]NameReport {
	m := map[string]NameReport{}
	for _, nr := range reports {
		m[nr.Rel] = nr
	}
	return m
}

func TestHostileNames_EveryServerItemArrivesAndTheServerIsNeverTouched(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("writes every name the Linux rules allow onto this machine's disk")
	}
	for _, goos := range []string{"linux", "windows", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			r := newRig(t)
			r.engine.GOOS = goos
			names, ntfsOnly := splitNTFSOnly(hostileOnServer(), goos)
			// On Windows these arrive under their own names; that is checked
			// on the mapping, since this Linux disk cannot hold them.
			for _, n := range ntfsOnly {
				remote := Snapshot{n.Name: {Rel: n.Name, Size: 1}}
				_, mapped, _, reports := (&Engine{GOOS: goos}).mapNames(Snapshot{}, remote, Baseline{}, true)
				if _, own := mapped[n.Name]; !own || len(reports) != 0 {
					t.Errorf("%s (%d bytes) does not arrive under its own name: %v %+v", n.ID, n.Bytes(), mapped, reports)
				}
			}
			for _, n := range names {
				r.writeRemote(n.Name, "body of "+n.ID)
			}
			deep := hostilenames.Deep()
			r.writeRemote(deep.Path, "deep")
			before := stateOf(r.srv)

			var first map[string]NameReport
			for pass := 1; pass <= 3; pass++ {
				downloads := r.srv.downloads
				res := r.run()
				if len(res.Errors) > 0 {
					t.Fatalf("pass %d: errors %v", pass, res.Errors)
				}
				if r.srv.uploads != 0 || r.srv.mkdirs != 0 || !reflect.DeepEqual(before, stateOf(r.srv)) {
					t.Fatalf("pass %d changed the server: %d upload(s), %d folder(s)", pass, r.srv.uploads, r.srv.mkdirs)
				}
				got := namesOf(res.Names)
				if pass == 1 {
					first = got
				} else {
					if !reflect.DeepEqual(got, first) {
						t.Errorf("pass %d named things otherwise: %+v, first %+v", pass, res.Names, first)
					}
					if r.srv.downloads != downloads {
						t.Errorf("pass %d downloaded %d file(s) again", pass, r.srv.downloads-downloads)
					}
				}
				seen := map[string]string{}
				for _, n := range names {
					aliased, why := aliasedOn(n, goos)
					nr, reported := got[n.Name]
					here := n.Name
					switch {
					case !aliased && reported:
						t.Errorf("pass %d: %s (%q) was renamed here: %+v", pass, n.ID, n.Name, nr)
						continue
					case aliased && (!reported || nr.Reason != ReasonSyncedAs):
						t.Errorf("pass %d: %s (%q) is not one this computer can name, and was not synced as another name: %+v", pass, n.ID, n.Name, nr)
						continue
					case aliased:
						here = nr.Other
						if localname.Problem(here, goos) != "" || here == n.Name {
							t.Errorf("pass %d: %s came down as %q", pass, n.ID, here)
						}
						if why != "windows" && nr.Why != why {
							t.Errorf("pass %d: %s synced as %+v, want why %s", pass, n.ID, nr, why)
						}
					}
					if body := r.readLocal(here); body != "body of "+n.ID {
						t.Errorf("pass %d: %s: %q holds %q", pass, n.ID, here, body)
					}
					// Two items, two files: one name here per item.
					k := localname.Key(here, goos)
					if other, dup := seen[k]; dup {
						t.Errorf("pass %d: %s and %s are one name here (%q)", pass, n.ID, other, here)
					}
					seen[k] = n.ID
				}
				if got := r.readLocal(deep.Path); got != "deep" {
					t.Errorf("pass %d: the deep path holds %q", pass, got)
				}
			}
		})
	}
}

// A server item Windows cannot name lives here under its alias: a change
// here goes to the server's ORIGINAL name, a change on the server comes down
// to the alias, and a delete follows the delete rules of any item - here
// deleted, deleted on the server (to its trash).
func TestHostileNames_AnAliasCarriesChangesBothWays(t *testing.T) {
	r := newRig(t)
	r.engine.GOOS = "windows"
	r.writeRemote("rapor:2024.txt", "v1")
	res := r.run()
	if got := namesOf(res.Names)["rapor:2024.txt"]; got.Reason != ReasonSyncedAs || got.Other != "rapor_2024.txt" || got.Why != localname.Forbidden {
		t.Fatalf("reported %+v", res.Names)
	}
	if r.readLocal("rapor_2024.txt") != "v1" {
		t.Fatal("the alias did not come down")
	}

	r.writeLocal("rapor_2024.txt", "edited here")
	r.run()
	if got := string(r.srv.files["rapor:2024.txt"]); got != "edited here" {
		t.Fatalf("the server's original holds %q", got)
	}
	if _, twin := r.srv.files["rapor_2024.txt"]; twin {
		t.Fatal("the alias was uploaded as a second file")
	}

	r.writeRemote("rapor:2024.txt", "edited on the server")
	r.run()
	if got := r.readLocal("rapor_2024.txt"); got != "edited on the server" {
		t.Fatalf("the alias holds %q", got)
	}

	if err := os.Remove(filepath.Join(r.dir, "rapor_2024.txt")); err != nil {
		t.Fatal(err)
	}
	r.run()
	if _, still := r.srv.files["rapor:2024.txt"]; still {
		t.Fatal("deleting the alias here did not delete the server's item")
	}
}

// The pairing is kept: an alias is not given afresh, and a second item that
// would take the same alias gets the next one.
func TestHostileNames_ThePairingIsKept(t *testing.T) {
	r := newRig(t)
	r.engine.GOOS = "windows"
	r.writeRemote("a*b.txt", "star")
	r.run()
	saved, err := r.engine.Store.LoadNames("p1")
	if err != nil || saved["a*b.txt"] != "a_b.txt" {
		t.Fatalf("the pairing kept is %v (%v)", saved, err)
	}
	r.writeRemote("a|b.txt", "pipe")
	res := r.run()
	if got := namesOf(res.Names)["a|b.txt"]; got.Other != "a_b (2).txt" {
		t.Fatalf("the second item is named %+v", got)
	}
	if r.readLocal("a_b.txt") != "star" || r.readLocal("a_b (2).txt") != "pipe" {
		t.Fatal("the two items' bytes are not under their own aliases")
	}
	// Forgotten with the item: deleted on the server, its pairing goes.
	delete(r.srv.files, "a*b.txt")
	r.run()
	if saved, _ := r.engine.Store.LoadNames("p1"); saved["a*b.txt"] != "" || saved["a|b.txt"] != "a_b (2).txt" {
		t.Fatalf("the pairings are %v", saved)
	}
}

// The alias is the name of a file here that is not this item: the server's
// item is not downloaded over it, and that is said. The file here is the
// person's own - a new file, uploaded as one - and once the server holds it
// the server's item takes the next name.
func TestHostileNames_ATakenAliasIsNotDownloaded(t *testing.T) {
	r := newRig(t)
	r.engine.GOOS = "windows"
	r.writeRemote("rapor:2024.txt", "server")
	r.writeLocal("rapor_2024.txt", "mine")
	res := r.run()
	if got := namesOf(res.Names)["rapor:2024.txt"]; got.Reason != ReasonTaken || got.Other != "rapor_2024.txt" {
		t.Fatalf("reported %+v", res.Names)
	}
	if r.readLocal("rapor_2024.txt") != "mine" {
		t.Fatal("the server's item was downloaded over the file here")
	}
	if got := string(r.srv.files["rapor:2024.txt"]); got != "server" {
		t.Fatalf("the server's item holds %q", got)
	}
	r.run()
	if r.readLocal("rapor_2024 (2).txt") != "server" || r.readLocal("rapor_2024.txt") != "mine" {
		t.Fatal("the server's item did not come down under the next name")
	}
}

// What an earlier version left on a Windows disk: `report.` downloaded as
// `report`, the folder `notes.` made as `notes`, `back\slash.txt` written as
// the folder `back`, with baseline rows in the server's names. The images of
// the same kind ARE those items now (compared by bytes: equal, nothing is
// sent); the folder `back` cannot be the file `back\slash.txt`, which comes
// down as `back_slash.txt`, and `back` stays here unsynced. Up to 0.56 this
// pass deleted `report.`, `notes.` and `back\slash.txt` on the server and
// uploaded `report`, `notes/a.txt` and `back/slash.txt` as new files.
func TestHostileNames_AWindowsLeftoverChangesNothingOnTheServer(t *testing.T) {
	r := newRig(t)
	r.engine.GOOS = "windows"
	bs := hostilenames.Get("backslash").Name
	r.writeRemote("report.", "R")
	r.writeRemote("notes./a.txt", "A")
	r.writeRemote(bs, "B")
	r.writeLocal("report", "R")
	r.writeLocal("notes/a.txt", "A")
	r.writeLocal("back/slash.txt", "B")
	sig := func(rel string) string {
		return Node{Size: int64(len(r.srv.files[rel])), ModMillis: r.srv.mod[rel]}.Signature()
	}
	if err := r.engine.Store.SaveBaseline("p1", Baseline{
		"report.":      {Local: "1:1", Remote: sig("report.")},
		"notes.":       {Local: "dir", Remote: "dir", IsDir: true},
		"notes./a.txt": {Local: "1:1", Remote: sig("notes./a.txt")},
		bs:             {Local: "1:1", Remote: sig(bs)},
	}); err != nil {
		t.Fatal(err)
	}
	before := stateOf(r.srv)

	for pass := 1; pass <= 2; pass++ {
		res := r.run()
		if r.srv.uploads != 0 || r.srv.mkdirs != 0 || !reflect.DeepEqual(before, stateOf(r.srv)) {
			t.Fatalf("pass %d changed the server: %d upload(s), %d folder(s) made", pass, r.srv.uploads, r.srv.mkdirs)
		}
		want := map[string]NameReport{
			"report.": {Rel: "report.", Reason: ReasonSyncedAs, Other: "report", Why: localname.Trailing},
			"notes.":  {Rel: "notes.", Reason: ReasonSyncedAs, Other: "notes", Why: localname.Trailing},
			bs:        {Rel: bs, Reason: ReasonSyncedAs, Other: "back_slash.txt", Why: localname.Forbidden},
			"back":    {Rel: "back", Reason: ReasonImage, Other: bs},
		}
		if got := namesOf(res.Names); !reflect.DeepEqual(got, want) {
			t.Fatalf("pass %d reported %+v, want %+v", pass, res.Names, want)
		}
		if r.readLocal("back_slash.txt") != "B" || r.readLocal("report") != "R" || r.readLocal("notes/a.txt") != "A" || r.readLocal("back/slash.txt") != "B" {
			t.Fatalf("pass %d: this computer's files are not what they were", pass)
		}
	}
}

// Two folders that are one folder on Windows: the second comes down as
// `docs (2)`, its files never land in the first, and a change in it reaches
// the server's `docs`. Up to 0.56 both folders' files merged into one here,
// and the next pass deleted the second folder on the server.
func TestHostileNames_FolderTwinsAreTwoFoldersHere(t *testing.T) {
	r := newRig(t)
	r.engine.GOOS = "windows"
	r.writeRemote("Docs/a.txt", "A")
	r.writeRemote("docs/b.txt", "B")
	before := stateOf(r.srv)
	for pass := 1; pass <= 2; pass++ {
		res := r.run()
		if r.srv.uploads != 0 || r.srv.mkdirs != 0 || !reflect.DeepEqual(before, stateOf(r.srv)) {
			t.Fatalf("pass %d changed the server", pass)
		}
		want := []NameReport{{Rel: "docs", Reason: ReasonSyncedAs, Other: "docs (2)", Why: WhyTwin}}
		if !reflect.DeepEqual(res.Names, want) {
			t.Fatalf("pass %d reported %+v", pass, res.Names)
		}
		if r.readLocal("Docs/a.txt") != "A" || r.readLocal("docs (2)/b.txt") != "B" || r.localExists("Docs/b.txt") {
			t.Fatalf("pass %d: the twins merged or the wrong one came down", pass)
		}
	}

	r.writeLocal("docs (2)/b.txt", "B2")
	r.run()
	if got := string(r.srv.files["docs/b.txt"]); got != "B2" {
		t.Fatalf("the server's docs/b.txt holds %q", got)
	}
	if _, wrong := r.srv.files["docs (2)/b.txt"]; wrong {
		t.Fatal("the alias folder was created on the server")
	}

	// A change the server announces for the twin is reconciled from the
	// parent: never as the contents of `Docs`.
	r.writeRemote("docs/c.txt", "C")
	if _, err := r.engine.RunDirs(context.Background(), []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.engine.RunDirs(context.Background(), []string{"docs (2)"}); err != nil {
		t.Fatal(err)
	}
	if _, wrong := r.srv.files["Docs/c.txt"]; wrong {
		t.Fatal("a targeted pass put the twin's file into Docs")
	}
	r.run()
	if r.readLocal("docs (2)/c.txt") != "C" || r.localExists("Docs/c.txt") {
		t.Fatal("the twin's new file did not come down into its own folder")
	}
}

// A first pairing where this computer holds `rapor.docx` and the server
// `Rapor.docx` - one name on Windows and macOS (and `Bütçe.txt` composed here,
// decomposed there, on macOS): one item, judged by the first-run rules. Equal
// bytes are adopted; different bytes keep both through a conflict copy. The
// file here is never uploaded as a second, twin, file.
func TestHostileNames_AFirstPairingMeetsItsOtherSpelling(t *testing.T) {
	type pairCase struct{ here, server string }
	cases := map[string][]pairCase{
		"windows": {{"rapor.docx", "Rapor.docx"}},
		"darwin":  {{"rapor.docx", "Rapor.docx"}, {norm.NFC.String("Bütçe.txt"), norm.NFD.String("Bütçe.txt")}},
	}
	for goos, pcs := range cases {
		for _, pc := range pcs {
			for _, same := range []bool{true, false} {
				t.Run(goos+"/"+pc.server, func(t *testing.T) {
					r := newRig(t)
					r.engine.GOOS = goos
					r.writeLocal(pc.here, "mine")
					theirs := "mine"
					if !same {
						theirs = "theirs"
					}
					r.writeRemote(pc.server, theirs)
					res := r.run()
					if len(res.Errors) > 0 || len(res.Names) > 0 {
						t.Fatalf("errors %v, names %+v", res.Errors, res.Names)
					}
					if _, twin := r.srv.files[pc.here]; twin {
						t.Fatalf("%q was uploaded as a second file beside %q", pc.here, pc.server)
					}
					switch {
					case same && (r.srv.uploads != 0 || res.Identical+res.Applied == 0 && res.Planned != 0):
						t.Fatalf("equal bytes: %d upload(s), %+v", r.srv.uploads, res)
					case !same && (res.Conflicts != 1 || string(r.srv.files[pc.server]) != "mine"):
						t.Fatalf("different bytes: conflicts %d, the server's item holds %q", res.Conflicts, r.srv.files[pc.server])
					}
					if r.readLocal(pc.here) != "mine" {
						t.Fatal("the file here changed")
					}
					saved, _ := r.engine.Store.LoadNames("p1")
					if saved[pc.server] != pc.here {
						t.Fatalf("the pairing kept is %v", saved)
					}
					// The next pass is in step.
					uploads := r.srv.uploads
					if res := r.run(); res.Planned != 0 || r.srv.uploads != uploads {
						t.Fatalf("the second pass planned %d", res.Planned)
					}
				})
			}
		}
	}
}

// A name made HERE that the server does not accept (a control character, a
// backslash, `...`; internal/leafname) is not uploaded to fail on every pass:
// it stays here and is said once a pass. An item the server holds under such
// a name from before keeps coming down, but a change here to it is not sent.
func TestHostileNames_ANameTheServerRefusesIsNotUploaded(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("writes control characters into names on this machine's disk")
	}
	r := newRig(t)
	r.engine.GOOS = "linux"
	refused := []string{hostilenames.Get("control-tab").Name, hostilenames.Get("backslash").Name, hostilenames.Get("three-dots").Name}
	for _, n := range refused {
		r.writeLocal(n, "here")
	}
	r.writeLocal("ok.txt", "fine")
	legacy := hostilenames.Get("control-newline").Name
	r.writeRemote(legacy, "v1")

	for pass := 1; pass <= 2; pass++ {
		res := r.run()
		if len(res.Errors) > 0 {
			t.Fatalf("pass %d: errors %v", pass, res.Errors)
		}
		got := namesOf(res.Names)
		for _, n := range refused {
			if got[n].Reason != ReasonServerRefuses {
				t.Errorf("pass %d: %q not reported: %+v", pass, n, res.Names)
			}
			if _, up := r.srv.files[n]; up {
				t.Errorf("pass %d: %q reached the server", pass, n)
			}
		}
	}
	if _, ok := r.srv.files["ok.txt"]; !ok {
		t.Fatal("the ordinary file beside them did not go up")
	}
	if r.readLocal(legacy) != "v1" {
		t.Fatal("the server's item with an old name did not come down")
	}
	r.writeLocal(legacy, "edited here")
	res := r.run()
	if got := namesOf(res.Names)[legacy]; got.Reason != ReasonServerRefuses {
		t.Fatalf("the change to the old name was not reported: %+v", res.Names)
	}
	if string(r.srv.files[legacy]) != "v1" {
		t.Fatal("the change was sent to a name the server refuses")
	}
}

func TestLocalSpelled(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		rel, goos string
		want      bool
	}{
		{"Docs/sub", "windows", true},
		{"docs/sub", "windows", false},
		{"docs/sub", "darwin", false},
		{"docs/sub", "linux", true},
		{"missing/x", "windows", true},
		{"report.", "windows", false},
		{"report.", "linux", true},
	} {
		if got := localSpelled(root, c.rel, c.goos); got != c.want {
			t.Errorf("localSpelled(%q, %s) = %v, want %v", c.rel, c.goos, got, c.want)
		}
	}
}

func TestNameMap_RemoteRelFollowsTheDeepestPairing(t *testing.T) {
	m := newNameMap(map[string]string{"report.": "report_", "report./CON": "report_/_CON"})
	for here, want := range map[string]string{
		"report_":        "report.",
		"report_/a.txt":  "report./a.txt",
		"report_/_CON":   "report./CON",
		"report_/_CON/x": "report./CON/x",
		"other/report_":  "other/report_",
		"report_x":       "report_x",
	} {
		if got := m.remoteRel(here); got != want {
			t.Errorf("remoteRel(%q) = %q, want %q", here, got, want)
		}
	}
	if !m.covers("report.") || !m.covers("report_/a") || m.covers("report") {
		t.Error("covers")
	}
}

// The 255-byte hostile name changed on both sides: its copy's name
// ("… (server copy …).txt") is past 255 bytes. freeSideName read every Lstat
// error as "taken", so ENAMETOOLONG on every candidate made the pass loop for
// ever with the pair's lock held. Now the copy's name is shortened to fit.
func TestHostileNames_AConflictOnALongNameEnds(t *testing.T) {
	r := newRig(t)
	name := hostilenames.Get("bytes-255").Name
	r.writeRemote(name, "v1")
	r.run()
	r.writeLocal(name, "mine")
	r.writeRemote(name, "theirs")

	done := make(chan Result, 1)
	go func() {
		res, _ := r.engine.Run(context.Background())
		done <- res
	}()
	var res Result
	select {
	case res = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the conflict pass on a 255-byte name never returned")
	}
	if res.Conflicts != 1 {
		t.Fatalf("conflicts %d, errors %v", res.Conflicts, res.Errors)
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, e := range entries {
		if e.Name() == name {
			continue
		}
		copies++
		if len(e.Name()) > localname.MaxLen || !strings.HasSuffix(e.Name(), ").txt") || !strings.Contains(e.Name(), "(server copy") {
			t.Errorf("the copy is named %q (%d bytes)", e.Name(), len(e.Name()))
		}
	}
	if copies != 1 {
		t.Fatalf("%d copies beside the file", copies)
	}
}

func TestFreeSideName_ADiskErrorIsAnError(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "file")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// ENOTDIR: neither "there" nor "not there". (Windows answers a path
	// through a file with "not found".)
	if runtime.GOOS != "windows" {
		if _, err := freeSideName(notADir, "", "x (server copy).txt", Snapshot{}); err == nil {
			t.Fatal("a folder that is a file gave a free name")
		}
	}
	got, err := freeSideName(dir, "", "file", Snapshot{"file (2)": {}})
	if err != nil || got != "file (3)" {
		t.Fatalf("got %q, %v; want file (3)", got, err)
	}
}

func TestSkipName_AnyLetterCase(t *testing.T) {
	for _, n := range []string{"Thumbs.db", "THUMBS.DB", "Desktop.ini", ".ds_store", "$Recycle.Bin"} {
		if !skipName(n) {
			t.Errorf("%s is synced", n)
		}
	}
	if skipName("thumbs.dbx") {
		t.Error("thumbs.dbx is skipped")
	}
}

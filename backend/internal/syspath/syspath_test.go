package syspath

import (
	"errors"
	"testing"
)

func TestHidden(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"", false},
		{"Documents/report.pdf", false},
		// A name that merely CONTAINS an internal name is a person's file. The
		// old manager filter was `strings.Contains(o.Path, ".thumbs")`, which
		// hid `my.thumbs.txt` and `notes.versions/` from their owner.
		{"my.thumbs.txt", false},
		{"notes.versions/a.txt", false},
		{".filex-openers.md", false},
		{".versions-notes.txt", false},
		{".filex-trash", true},
		{".filex-trash/1726-abcdef__report.pdf", true},
		{"/.filex-trash/1726-abcdef__report.pdf", true},
		{".versions/7/1", true},
		{".thumbs/a.jpg", true},
		{".filex-open", true},
		{".filex-open/a1b2c3d4e5f6-Bütçe.xlsx", true},
		{"docs://.filex-open/a1b2c3d4e5f6-Bütçe.xlsx", true},
		{`.filex-open\a1b2c3d4e5f6-x.docx`, true},
		// Any depth: a shared sub-folder is judged relative to itself.
		{"projects/.versions/1/1", true},
		// The keep marker is hidden as a LEAF only.
		{"Photos/.keepdir", true},
		{".keepdir", true},
		// A traversal that lands inside one is still inside one.
		{"a/../.filex-trash/x", true},
	}
	for _, c := range cases {
		if got := Hidden(c.rel); got != c.want {
			t.Errorf("Hidden(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestInDirLeavesTheKeepMarkerAlone(t *testing.T) {
	// The protocol servers refuse every path InDir answers true for. The keep
	// marker is a FILE a WebDAV/S3 client must be able to delete when it
	// removes the folder that holds it, so InDir must not claim it.
	if InDir("Photos/.keepdir") {
		t.Fatal("InDir claimed a keep marker")
	}
	for _, d := range Dirs() {
		if !InDir(d) || !InDir(d+"/x") || !InDir("a/"+d+"/x") {
			t.Fatalf("InDir missed %s", d)
		}
	}
}

func TestSealedExcludesTheOpenWithWorkingArea(t *testing.T) {
	for _, rel := range []string{".filex-trash/x", ".versions/7/1", ".thumbs/a.jpg", ".filex-open/.versions/1"} {
		if !Sealed(rel) {
			t.Errorf("Sealed(%q) = false", rel)
		}
	}
	// ⚠⚠ The desktop lists, downloads and uploads here by exact path; a
	// sealed .filex-open would make every open-with save vanish silently.
	for _, rel := range []string{".filex-open", ".filex-open/a1b2c3d4e5f6-x.docx", "Documents/a.txt"} {
		if Sealed(rel) {
			t.Errorf("Sealed(%q) = true", rel)
		}
	}
}

func TestOpenWithOriginal(t *testing.T) {
	cases := []struct {
		rel, name string
		ok        bool
	}{
		// The shape desktop/src/openwith.ts scratchBasename produces: 12 hex,
		// a dash, the original stem and a lower-cased extension.
		{".filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx", "Bütçe Özeti.xlsx", true},
		{"/.filex-open/0123456789ab-Rapor 2026.docx", "Rapor 2026.docx", true},
		{"docs://.filex-open/0123456789ab-a-b-c.docx", "a-b-c.docx", true},
		// The desktop's own test fixtures use shorter ids.
		{".filex-open/abc123-Bütçe.xlsx", "Bütçe.xlsx", true},
		{".filex-open/a1-a.docx", "a.docx", true},
		// Not a working copy.
		{".filex-open", "", false},
		{".filex-open/notes.txt", "", false},
		{".filex-open/abcdef-", "", false},
		{".filex-open/sub/abcdef-x.docx", "", false},
		{"Documents/abcdef-x.docx", "", false},
		{".filex-trash/abcdef-x.docx", "", false},
	}
	for _, c := range cases {
		name, ok := OpenWithOriginal(c.rel)
		if name != c.name || ok != c.ok {
			t.Errorf("OpenWithOriginal(%q) = (%q, %v), want (%q, %v)", c.rel, name, ok, c.name, c.ok)
		}
	}
}

// The notify read path hides old rows with SQL LIKE patterns built from these
// names (notify.hiddenBodies) and passes them unescaped. A name holding a LIKE
// wildcard would silently widen the filter to other people's rows.
func TestNamesAreSafeLikeLiterals(t *testing.T) {
	for _, n := range append(Dirs(), KeepMarker) {
		for _, c := range []string{"%", "_", `\`} {
			if contains(n, c) {
				t.Fatalf("%q contains %q — escape it before notify.hiddenBodies uses it", n, c)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDirsIsACopy(t *testing.T) {
	d := Dirs()
	d[0] = "mutated"
	if Dirs()[0] == "mutated" {
		t.Fatal("Dirs handed out the backing array")
	}
}

// TestRefused pins the write rule: every Hidden path is refused to every
// person-facing mutation, except the desktop's open-with round trip, and that
// exception is exactly the shapes the desktop sends (desktop/src/openwith.ts
// scratchRemoteDir / scratchRemotePath, openwith-io.ts ensureScratchDir,
// uploadFile, deleteRemote).
func TestRefused(t *testing.T) {
	const copyName = ".filex-open/a1b2c3d4e5f6-Bütçe Özeti.xlsx"
	cases := []struct {
		verb Verb
		rel  string
		want bool
	}{
		// Ordinary paths: never refused, by any verb.
		{Change, "Documents/report.txt", false},
		{Change, "my.thumbs.txt", false},
		{Change, "", false},
		{PutWorkCopy, "Documents/a1b2c3d4e5f6-report.docx", false},

		// Every internal directory, every depth, by the ordinary verbs.
		{Change, ".filex-trash", true},
		{Change, ".filex-trash/1726-ab__x.txt", true},
		{Change, "docs/.versions/7/1", true},
		{Change, ".thumbs/abc.jpg", true},
		{Change, ".filex-open", true},
		{Change, copyName, true},
		{Change, "a/.keepdir", true},
		{Change, "docs://.filex-trash/x", true},

		// The desktop's four shapes.
		{MakeWorkArea, ".filex-open", false},
		{MakeWorkArea, "docs://.filex-open", false},
		{PutWorkCopy, copyName, false},
		{PutWorkCopy, "docs://" + copyName, false},
		{DropWorkCopy, copyName, false},

		// …and nothing next to them.
		{MakeWorkArea, "Documents/.filex-open", true}, // only at the root
		{MakeWorkArea, ".filex-open/sub", true},       // no sub-folders
		{MakeWorkArea, ".filex-trash", true},          // only that one name
		{PutWorkCopy, ".filex-open/Plan.docx", true},  // no session prefix
		{PutWorkCopy, ".filex-open/a1b2c3-", true},    // prefix and nothing else
		{PutWorkCopy, ".filex-open/a1b2c3-.keepdir", true},
		{PutWorkCopy, ".filex-open/sub/a1b2c3-x.docx", true},
		{PutWorkCopy, "Documents/.filex-open/a1b2c3-x.docx", true},
		{PutWorkCopy, ".filex-trash/a1b2c3-x.docx", true},
		{PutWorkCopy, ".filex-open", true},
		{DropWorkCopy, ".filex-open", true}, // the area itself stays
		{DropWorkCopy, ".versions/7/1", true},

		// A protocol client: the directories, never the keep marker.
		{Mounted, "Documents/.keepdir", false},
		{Mounted, ".filex-trash/x", true},
		{Mounted, "docs/.versions", true},
		{Mounted, ".filex-open/a1b2c3-x.docx", true},
		{Mounted, "Documents/a.txt", false},
	}
	for _, c := range cases {
		if got := Refused(c.verb, c.rel); got != c.want {
			t.Errorf("Refused(%d, %q) = %v, want %v", c.verb, c.rel, got, c.want)
		}
	}
}

func TestReserved(t *testing.T) {
	for rel, want := range map[string]string{
		"Documents/report.txt":         "",
		".filex-trash/x":               Trash,
		"docs/.versions/7/1":           Versions,
		"a/.keepdir":                   KeepMarker,
		"docs://.filex-open/a1-x.docx": OpenWith,
	} {
		if got := Reserved(rel); got != want {
			t.Errorf("Reserved(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestCheckNamesTheRefusedPath(t *testing.T) {
	if err := Check(Change, "Documents/a.txt", "b/c.txt"); err != nil {
		t.Fatalf("ordinary paths refused: %v", err)
	}
	err := Check(Change, "Documents/a.txt", "docs/.versions/7/1")
	if !errors.Is(err, ErrReserved) {
		t.Fatalf("Check = %v, want ErrReserved", err)
	}
	var re *ReservedError
	if !errors.As(err, &re) || re.Rel != "docs/.versions/7/1" {
		t.Fatalf("the error does not carry the refused path: %#v", err)
	}
	if got := err.Error(); got != `".versions" is reserved for filex's own use` {
		t.Fatalf("message = %q", got)
	}
}

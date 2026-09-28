package syspath

import "testing"

// The drafts area (issue #71): one of filex's own names like the trash, and
// one person's alone. These pin the shapes every door relies on — the
// listings and the protocols through Hidden/InDir/IsName, the scanner through
// Sealed, the owner's editor through SealedFor/RefusedBy.

func TestDrafts_IsOneOfFilexOwnNames(t *testing.T) {
	found := false
	for _, d := range Dirs() {
		if d == Drafts {
			found = true
		}
	}
	if !found {
		t.Fatalf("Dirs() = %v, want it to hold %q", Dirs(), Drafts)
	}
	for _, rel := range []string{
		".filex-drafts",
		".filex-drafts/7/0123456789abcdef/notes.txt",
		"docs://.filex-drafts/7/0123456789abcdef/notes.txt",
	} {
		if !Hidden(rel) || !InDir(rel) || !Sealed(rel) {
			t.Errorf("%q: Hidden=%v InDir=%v Sealed=%v, want all true", rel, Hidden(rel), InDir(rel), Sealed(rel))
		}
	}
	if !IsName(".filex-drafts") {
		t.Error("IsName(.filex-drafts) = false: a listing would show the drafts area")
	}
	// A person's own names that merely contain it.
	for _, rel := range []string{".filex-drafts-old/a.txt", "my.filex-drafts.txt", "Drafts/a.txt"} {
		if Hidden(rel) || Sealed(rel) {
			t.Errorf("%q is a person's file, judged filex's own", rel)
		}
	}
}

func TestDraftOwner(t *testing.T) {
	cases := []struct {
		rel string
		id  int64
		ok  bool
	}{
		{".filex-drafts/7/0123456789abcdef/notes.txt", 7, true},
		{"/.filex-drafts/7/0123456789abcdef/notes.txt", 7, true},
		{"docs://.filex-drafts/12", 12, true},
		{".filex-drafts", 0, false},
		{".filex-drafts/abc/x", 0, false},
		{".filex-drafts/007/x", 0, false}, // not how an id is written
		{".filex-drafts/-3/x", 0, false},
		{"Documents/.filex-drafts/7/x", 0, false}, // anchored at the root
		{"Documents/notes.txt", 0, false},
	}
	for _, c := range cases {
		id, ok := DraftOwner(c.rel)
		if id != c.id || ok != c.ok {
			t.Errorf("DraftOwner(%q) = %d, %v; want %d, %v", c.rel, id, ok, c.id, c.ok)
		}
	}
}

func TestIsDraftOf_OnlyTheOwnersDraftFile(t *testing.T) {
	own := ".filex-drafts/7/0123456789abcdef/notes.txt"
	if !IsDraftOf(own, 7) {
		t.Fatalf("IsDraftOf(%q, 7) = false", own)
	}
	if !IsDraftOf("docs://"+own, 7) {
		t.Fatal("the wire form of the owner's draft was refused")
	}
	for _, c := range []struct {
		rel    string
		person int64
	}{
		{own, 8},                                // somebody else
		{own, 0},                                // nobody
		{".filex-drafts/7/0123456789abcdef", 7}, // the draft's folder
		{".filex-drafts/7", 7},                  // the person's area
		{".filex-drafts/7/NOTAKEY!/notes.txt", 7},   // not a key filex mints
		{".filex-drafts/7/0123456789abcdef/a/b", 7}, // deeper than a draft
		{".filex-drafts/7/0123456789abcdef/.keepdir", 7},
		{".filex-drafts/7/0123456789abcdef/.versions", 7},
		{"Documents/notes.txt", 7},
	} {
		if IsDraftOf(c.rel, c.person) {
			t.Errorf("IsDraftOf(%q, %d) = true", c.rel, c.person)
		}
	}
}

func TestSealedFor_OpensOnlyTheOwnersDraft(t *testing.T) {
	own := ".filex-drafts/7/0123456789abcdef/notes.txt"
	if SealedFor(own, 7) {
		t.Error("the owner's editor could not read the owner's draft")
	}
	if !SealedFor(own, 8) || !SealedFor(own, 0) {
		t.Error("somebody else's draft was served by path")
	}
	// The rest of the sealed trees stay sealed for everybody.
	for _, rel := range []string{".filex-trash/x", ".versions/1/1", ".filex-drafts/7", ".filex-drafts"} {
		if !SealedFor(rel, 7) {
			t.Errorf("SealedFor(%q, 7) = false", rel)
		}
	}
	if SealedFor("Documents/notes.txt", 7) {
		t.Error("an ordinary file was sealed")
	}
}

func TestRefusedBy_OwnDraftIsTheOnlyWayIn(t *testing.T) {
	own := ".filex-drafts/7/0123456789abcdef/notes.txt"
	if RefusedBy(OwnDraft, own, 7) {
		t.Error("the owner's editor could not save the owner's draft")
	}
	if !RefusedBy(OwnDraft, own, 8) {
		t.Error("another person saved into somebody's draft")
	}
	for _, v := range []Verb{Change, MakeWorkArea, PutWorkCopy, DropWorkCopy, Mounted} {
		if !RefusedBy(v, own, 7) {
			t.Errorf("verb %d wrote into a draft without claiming OwnDraft", v)
		}
	}
	// Claiming OwnDraft buys nothing anywhere else among filex's own…
	for _, rel := range []string{".filex-trash/x", ".filex-open/a1b2c3d4e5f6-x.docx", ".filex-drafts/7/0123456789abcdef"} {
		if !RefusedBy(OwnDraft, rel, 7) {
			t.Errorf("OwnDraft let %q through", rel)
		}
	}
	// …and changes nothing for an ordinary path.
	if RefusedBy(OwnDraft, "Documents/notes.txt", 7) {
		t.Error("an ordinary save was refused")
	}
	// Refused itself never lets a draft through: it does not know who asks.
	if !Refused(OwnDraft, own) {
		t.Error("Refused(OwnDraft) let a draft through with no person")
	}
}

func TestDraftDir(t *testing.T) {
	if got := DraftDir(7, "0123456789abcdef"); got != ".filex-drafts/7/0123456789abcdef" {
		t.Fatalf("DraftDir = %q", got)
	}
	if !IsDraftOf(DraftDir(7, "0123456789abcdef")+"/notes.txt", 7) {
		t.Fatal("a path DraftDir builds is not a draft")
	}
}

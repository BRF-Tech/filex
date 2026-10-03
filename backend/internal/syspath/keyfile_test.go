package syspath

import (
	"errors"
	"strings"
	"testing"
)

// An encrypted folder's key file is not filex's machinery: only listings
// leave it out (Unlisted) and only a surface with no key is refused writing
// it (Keyless). Everything else - the browser that writes it, a copy, a zip,
// a protocol client that mirrors the folder - treats it as a file.
func TestKeyFile_TwoRulesAndNoMore(t *testing.T) {
	for _, name := range []string{E2EKeyFile, ".filex-trash", ".versions", ".keepdir"} {
		if !Unlisted(name) {
			t.Errorf("Unlisted(%q) = false: a listing must leave it out", name)
		}
	}
	for _, name := range []string{"rapor.pdf", ".filex-e2e.json.bak", "x.filex-e2e.json", ""} {
		if Unlisted(name) {
			t.Errorf("Unlisted(%q) = true: a person's own name", name)
		}
	}
	// IsName is the transfer rule; the key file is not in it.
	if IsName(E2EKeyFile) {
		t.Error("IsName(key file) = true: a copy or zip of the folder would leave its keys behind")
	}

	for _, rel := range []string{"kasa/" + E2EKeyFile, E2EKeyFile, "/a/b/" + E2EKeyFile, "depo://kasa/" + E2EKeyFile} {
		if !IsKeyFile(rel) {
			t.Errorf("IsKeyFile(%q) = false", rel)
		}
		if !Refused(Keyless, rel) {
			t.Errorf("Refused(Keyless, %q) = false: a keyless surface would write the keys", rel)
		}
		for _, v := range []Verb{Change, Mounted, PutWorkCopy, DropWorkCopy, MakeWorkArea, OwnDraft} {
			if Refused(v, rel) {
				t.Errorf("Refused(%d, %q) = true: only a keyless write is refused", v, rel)
			}
		}
		if got := Reserved(rel); got != E2EKeyFile {
			t.Errorf("Reserved(%q) = %q", rel, got)
		}
	}
	for _, rel := range []string{"kasa", "kasa/rapor.pdf", "kasa/" + E2EKeyFile + "/x"} {
		if IsKeyFile(rel) || Refused(Keyless, rel) {
			t.Errorf("%q is not a key file", rel)
		}
	}
	// Keyless is Change on top: filex's own names stay refused.
	if !Refused(Keyless, ".filex-trash/x") || !Refused(Keyless, "a/.keepdir") {
		t.Error("Keyless must refuse what Change refuses")
	}
}

func TestKeyFile_TheRefusalSaysWhatItIs(t *testing.T) {
	err := Check(Keyless, "kasa/"+E2EKeyFile)
	if !errors.Is(err, ErrReserved) {
		t.Fatalf("Check = %v, want ErrReserved", err)
	}
	if !strings.Contains(err.Error(), "key file") || strings.Contains(err.Error(), "filex's own use") {
		t.Errorf("message %q should name the key file, not filex's own folders", err.Error())
	}
}

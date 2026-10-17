package zipstream

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// Every name a filex door may create is a member name as it is - but for a
// bidi control or a control character, which the copy saved outside filex
// carries as `_` (leafname.SavedRune) - and the deep path too (task #238).
func TestEntryName_HostileNamesSurvive(t *testing.T) {
	for _, n := range hostilenames.Creatable() {
		want := strings.Map(leafname.SavedRune, n.Name)
		if got, err := EntryName(n.Name); err != nil || got != want {
			t.Errorf("%s: EntryName(%q) = %q, %v; want %q", n.ID, n.Name, got, err, want)
		}
	}
	d := hostilenames.Deep()
	if got, err := EntryName(d.Path); err != nil || got != d.Path {
		t.Errorf("deep: EntryName = %q, %v", got, err)
	}
}

// A name that BEGINS with two dots is a name, and a colon in second place is
// a drive letter only after a letter. Both were dropped from the archive
// before 0.57 (`strings.HasPrefix(clean, "..")`, `clean[1] == ':'`).
func TestEntryName_DotsAndColonsThatAreNames(t *testing.T) {
	for _, ok := range []string{"..notes", "..hidden/a.txt", "1:1 notes.txt", "9:30 call.md"} {
		if got, err := EntryName(ok); err != nil || got != ok {
			t.Errorf("EntryName(%q) = %q, %v; want it kept", ok, got, err)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../../x", "C:x", `c:\x`, "Z:/x"} {
		if got, err := EntryName(bad); err == nil {
			t.Errorf("EntryName(%q) = %q, want a refusal", bad, got)
		}
	}
}

// The archive itself: every member is there under its own name, and every
// name that is not plain ASCII carries the UTF-8 flag (general purpose bit
// 11), without which an unzip reads it as CP437.
func TestWrite_HostileNamesKeepNamesAndUTF8Flag(t *testing.T) {
	var members []Member
	for _, n := range hostilenames.Creatable() {
		members = append(members, mem(n.Name, n.ID))
	}
	var buf bytes.Buffer
	skips, err := Write(context.Background(), &buf, members, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("skipped %v", skips)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	for _, n := range hostilenames.Creatable() {
		f, ok := byName[strings.Map(leafname.SavedRune, n.Name)]
		if !ok {
			t.Errorf("%s: no member named %q", n.ID, n.Name)
			continue
		}
		// The flag is the member's: a name `_` made ASCII (invoice_fdp.exe)
		// is plain ASCII in the archive, and needs none.
		ascii := len(f.Name) == utf8.RuneCountInString(f.Name)
		if !ascii && f.Flags&0x800 == 0 {
			t.Errorf("%s: %q has no UTF-8 flag", n.ID, f.Name)
		}
	}
}

// The RLO name unpacks with its true extension visible, and a control
// character becomes `_`: what lands on someone's disk carries neither (the
// owner's decision for 0.57). Before, the member was named exactly as the
// file, and a file manager showed `invoice` + U+202E + `fdp.exe` as
// `invoiceexe.pdf`.
func TestEntryName_SavedCopyLosesBidiAndControlCharacters(t *testing.T) {
	rlo := hostilenames.Get("rlo-extension").Name
	for in, want := range map[string]string{
		rlo:                                  "invoice_fdp.exe",
		"dir/" + rlo:                         "dir/invoice_fdp.exe",
		hostilenames.Get("control-tab").Name: "tab_name.txt",
		"plain name.txt":                     "plain name.txt",
	} {
		if got, err := EntryName(in); err != nil || got != want {
			t.Errorf("EntryName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

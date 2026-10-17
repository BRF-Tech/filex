package localname

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// Problem says "Windows cannot hold it" for exactly the hostile names the
// shared list marks windows-invalid, and for nothing else: one rule, checked
// against the list every surface is tested with (task #238).
func TestProblem_WindowsFollowsTheHostileNamesList(t *testing.T) {
	for _, n := range hostilenames.All() {
		got := Problem(n.Name, "windows")
		if (got != "") != n.WindowsInvalid {
			t.Errorf("%s (%q): Problem on windows = %q, the list says windows invalid = %v", n.ID, n.Name, got, n.WindowsInvalid)
		}
		// Safe changes a name exactly when Windows cannot hold it.
		if safe := Safe(n.Name, "windows"); (safe != n.Name) != (got != "") {
			t.Errorf("%s: Safe = %q with Problem %q", n.ID, safe, got)
		} else if got != "" && Problem(safe, "windows") != "" {
			t.Errorf("%s: Safe made %q, which Windows still refuses (%s)", n.ID, safe, Problem(safe, "windows"))
		}
	}
}

func TestProblem_LengthIsBytesOnLinuxAndMacUnitsOnWindows(t *testing.T) {
	for _, n := range hostilenames.All() {
		for _, goos := range []string{"linux", "darwin"} {
			if got, want := Problem(n.Name, goos) == TooLong, n.Bytes() > MaxLen; got != want {
				t.Errorf("%s on %s: too long = %v, %d bytes", n.ID, goos, got, n.Bytes())
			}
			if p := Problem(n.Name, goos); p != "" && p != TooLong {
				t.Errorf("%s on %s: %q - only the length limits a name there", n.ID, goos, p)
			}
		}
	}
	// 204 two-byte letters: 408 bytes, 204 UTF-16 units. Too long on ext4,
	// fine on NTFS.
	c := hostilenames.Get("chars-under-bytes-over").Name
	if Problem(c, "linux") != TooLong || Problem(c, "windows") != "" {
		t.Errorf("chars-under-bytes-over: linux %q, windows %q", Problem(c, "linux"), Problem(c, "windows"))
	}
	if Problem(strings.Repeat("a", 256), "windows") != TooLong {
		t.Error("256 units is too long on Windows")
	}
}

func TestProblem_Reasons(t *testing.T) {
	for name, want := range map[string]string{
		"CON":            Reserved,
		"aux.txt":        Reserved,
		"com1.log":       Reserved,
		"console.txt":    "",
		"a:b.txt":        Forbidden,
		"tab\tname.txt":  Forbidden,
		"report.":        Trailing,
		"trailing.txt ":  Trailing,
		" leading.txt":   "",
		"Bütçe ğüşıöç.x": "",
	} {
		if got := Problem(name, "windows"); got != want {
			t.Errorf("Problem(%q, windows) = %q, want %q", name, got, want)
		}
	}
}

func TestKey_TwinsAreOneNameWhereTheFileSystemFolds(t *testing.T) {
	for _, n := range hostilenames.All() {
		if n.Twin == "" {
			continue
		}
		o := hostilenames.Get(n.Twin)
		if Key(n.Name, "linux") == Key(o.Name, "linux") {
			t.Errorf("%s and %s are one name on linux", n.ID, o.ID)
		}
		if Key(n.Name, "darwin") != Key(o.Name, "darwin") {
			t.Errorf("%s and %s are two names on darwin", n.ID, o.ID)
		}
		// NTFS ignores case but keeps both normalisation forms.
		if same := Key(n.Name, "windows") == Key(o.Name, "windows"); same != (n.TwinKind == hostilenames.TwinCase) {
			t.Errorf("%s and %s (%s): one name on windows = %v", n.ID, o.ID, n.TwinKind, same)
		}
	}
	if !Folds("windows") || !Folds("darwin") || Folds("linux") {
		t.Error("Folds")
	}
}

// Every hostile name has an Alias each OS holds, and a name the OS holds is
// its own Alias: the sync engine writes the first and never renames the
// second.
func TestAlias_EveryNameHasOneThatFits(t *testing.T) {
	for _, n := range hostilenames.All() {
		for _, goos := range []string{"windows", "darwin", "linux"} {
			a := Alias(n.Name, goos)
			if p := Problem(a, goos); p != "" {
				t.Errorf("%s on %s: Alias %q still %s", n.ID, goos, a, p)
			}
			if Problem(n.Name, goos) == "" && a != n.Name {
				t.Errorf("%s on %s: a name the OS holds became %q", n.ID, goos, a)
			}
		}
	}
	long := hostilenames.Get("chars-under-bytes-over").Name
	if a := Alias(long, "linux"); len(a) > MaxLen || !strings.HasSuffix(a, ".txt") || !strings.HasPrefix(long, strings.TrimSuffix(a, ".txt")) {
		t.Errorf("the 404-byte name's alias is %q (%d bytes)", a, len(a))
	}
	if a := Alias(long, "windows"); a != long {
		t.Errorf("NTFS holds the 204-character name, alias %q", a)
	}
}

func TestNumbered_KeepsTheExtensionAndFits(t *testing.T) {
	if got := Numbered("rapor.docx", 2, "windows"); got != "rapor (2).docx" {
		t.Errorf("Numbered = %q", got)
	}
	if got := Numbered("Makefile", 3, "linux"); got != "Makefile (3)" {
		t.Errorf("Numbered = %q", got)
	}
	n := Numbered(hostilenames.Get("bytes-255").Name, 2, "linux")
	if len(n) > MaxLen || !strings.HasSuffix(n, " (2).txt") {
		t.Errorf("Numbered of the 255-byte name = %d bytes, %q", len(n), n[len(n)-12:])
	}
}

func TestImage_WhatWindowsWritesForAName(t *testing.T) {
	for name, want := range map[string]string{
		"report.":         "report",
		"trailing.txt ":   "trailing.txt",
		"back\\slash.txt": "back",
		"...":             "",
		"plain.txt":       "",
		"\\lead":          "",
	} {
		got, ok := Image(name, "windows")
		if ok != (want != "") || got != want {
			t.Errorf("Image(%q) = %q, %v; want %q", name, got, ok, want)
		}
		if _, ok := Image(name, "linux"); ok {
			t.Errorf("Image(%q) on linux", name)
		}
	}
}

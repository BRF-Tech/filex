package acl

import (
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// A blocked type is blocked under every spelling that becomes it once saved
// (task #238): Windows and a browser saving a download drop trailing dots and
// spaces, and an NTFS stream name writes into the file before its colon.
// Before 0.57 only the exact suffix was compared, so `setup.exe.` passed an
// upload, a rename and a file request and was saved as `setup.exe`.
func TestBlockedExtension_EverySavedForm(t *testing.T) {
	s := mkSet("user", false)
	s.perms = &perm.Result{Settings: model.PermRuleSettings{BlockedExtensions: []string{"exe", "tar.gz"}}}
	for _, name := range []string{
		"setup.exe", "Setup.EXE", "setup.exe.", "setup.exe ", "setup.exe. .",
		"setup.exe:payload", "setup.exe::$DATA", "dir/setup.exe..",
		"backup.tar.gz.",
	} {
		if s.BlockedExtension(name) == "" {
			t.Errorf("%q passed, it is saved as a blocked type", name)
		}
	}
	for _, name := range []string{"setup.exe.txt", "exe", "notes.md", "a:b.txt", "readme.exe-notes"} {
		if got := s.BlockedExtension(name); got != "" {
			t.Errorf("%q blocked as %q", name, got)
		}
	}
}

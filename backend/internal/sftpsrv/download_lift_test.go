package sftpsrv_test

// A rename over SFTP puts the source's bytes at another path. Where a role's
// part refuses files.download on the source, a rename or a posix-rename to a
// path where the same caller could download them is refused
// (protoperm.TreeDownloadLifted); one that stays where download is refused
// goes through.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestSFTP_RenameKeepsADownloadRule(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Locked/secret.txt", []byte("kept"))
	hz.writeFile(t, st, "Locked/inner/deep.txt", []byte("kept too"))
	hz.giveRole(t, &model.PermissionRule{
		Name:        "Locked stays",
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{st.ID}, Paths: []string{"Locked"}},
	})
	cl := hz.mustDial(t, "p@example.com", testPassword)
	defer cl.Close()
	root := hz.rootOf(t, st)

	if err := cl.Rename("/main/Locked/secret.txt", "/main/out.txt"); err == nil {
		t.Fatal("a rename out of Locked went through")
	}
	if err := cl.PosixRename("/main/Locked/secret.txt", "/main/out.txt"); err == nil {
		t.Fatal("a posix-rename out of Locked went through")
	}
	if err := cl.Rename("/main/Locked/inner", "/main/inner"); err == nil {
		t.Fatal("a folder renamed out of Locked went through")
	}
	if _, err := os.Stat(filepath.Join(root, "Locked", "secret.txt")); err != nil {
		t.Fatal("the file left Locked")
	}
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("the file landed outside Locked")
	}

	if err := cl.Rename("/main/Locked/secret.txt", "/main/Locked/secret-v2.txt"); err != nil {
		t.Fatalf("a rename inside Locked: %v", err)
	}
}

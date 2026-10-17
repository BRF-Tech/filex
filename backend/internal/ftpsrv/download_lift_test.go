package ftpsrv_test

// RNFR/RNTO puts the source's bytes at another path. Where a role's part
// refuses files.download on the source, a rename to a path where the same
// caller could download them is refused (protoperm.TreeDownloadLifted); one
// that stays where download is refused goes through.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestFTP_RenameKeepsADownloadRule(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Locked/secret.txt", []byte("kept"))
	hz.writeFile(t, st, "Locked/inner/deep.txt", []byte("kept too"))

	ctx := context.Background()
	rule, err := hz.store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Locked stays", Enabled: true,
		Permissions: perm.Standard.Strings(),
		Effects:     map[string]string{string(perm.FilesDownload): model.PermDeny},
		Conditions:  model.PermRuleConditions{StorageIDs: []int64{st.ID}, Paths: []string{"Locked"}},
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := hz.store.SetUserCustomRole(ctx, u.ID, rule.ID); err != nil {
		t.Fatalf("give the role: %v", err)
	}
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)

	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })
	root := hz.rootOf(t, st)

	if err := conn.Rename("/main/Locked/secret.txt", "/main/out.txt"); err == nil {
		t.Fatal("a rename out of Locked went through")
	}
	if err := conn.Rename("/main/Locked/inner", "/main/inner"); err == nil {
		t.Fatal("a folder renamed out of Locked went through")
	}
	if _, err := os.Stat(filepath.Join(root, "Locked", "secret.txt")); err != nil {
		t.Fatal("the file left Locked")
	}
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("the file landed outside Locked")
	}

	if err := conn.Rename("/main/Locked/secret.txt", "/main/Locked/secret-v2.txt"); err != nil {
		t.Fatalf("a rename inside Locked: %v", err)
	}
}

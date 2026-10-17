package nfssrv_test

// An NFS RENAME puts the source's bytes at another path. Where a role's part
// refuses files.download on the source, a rename to a path where the same
// caller could download them is refused with NFS3ERR_ACCES
// (protoperm.TreeDownloadLifted); one that stays where download is refused
// goes through.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	nfsclient "github.com/willscott/go-nfs-client/nfs"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

func TestNFS_RenameKeepsADownloadRule(t *testing.T) {
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

	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	root := hz.rootOf(t, st)

	if err := target.Rename("/Locked/secret.txt", "/out.txt"); nfsStatus(err) != nfsclient.NFS3ErrAcces {
		t.Fatalf("a rename out of Locked: %v, want NFS3ERR_ACCES", err)
	}
	if err := target.Rename("/Locked/inner", "/inner"); nfsStatus(err) != nfsclient.NFS3ErrAcces {
		t.Fatalf("a folder renamed out of Locked: %v, want NFS3ERR_ACCES", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Locked", "secret.txt")); err != nil {
		t.Fatal("the file left Locked")
	}
	if _, err := os.Stat(filepath.Join(root, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("the file landed outside Locked")
	}

	if err := target.Rename("/Locked/secret.txt", "/Locked/secret-v2.txt"); err != nil {
		t.Fatalf("a rename inside Locked: %v", err)
	}
}

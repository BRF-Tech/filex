package nfssrv_test

// Per-user permissions (internal/perm) over NFS: access.nfs at mount, and
// each operation on the action it performs.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

func (hz *harness) deny(t *testing.T, u *model.User, ps ...perm.Perm) {
	t.Helper()
	m := map[string]string{}
	for _, p := range ps {
		m[string(p)] = model.PermDeny
	}
	if err := hz.store.SetUserPermissionOverrides(context.Background(), u.ID, m, nil); err != nil {
		t.Fatalf("overrides: %v", err)
	}
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
}

func TestPerm_AccessNFSGatesTheMount(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	hz.storage(t, "main")
	path := hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"})
	hz.deny(t, u, perm.AccessNFS)
	if _, err := hz.mountOnce(t, path); err == nil {
		t.Fatal("an account without access.nfs mounted its export")
	}
}

func TestPerm_NFSOperations(t *testing.T) {
	setup := func(t *testing.T, p perm.Perm) (*harness, string, string) {
		hz := newHarness(t)
		u := hz.user(t, "p@example.com")
		st := hz.storage(t, "main")
		hz.writeFile(t, st, "report.txt", []byte("q3 numbers"))
		path := hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"})
		hz.deny(t, u, p)
		return hz, path, hz.rootOf(t, st)
	}
	t.Run("download", func(t *testing.T) {
		hz, path, _ := setup(t, perm.FilesDownload)
		target := hz.mustMount(t, path)
		if rd, err := target.Open("/report.txt"); err == nil {
			b, rerr := io.ReadAll(rd)
			rd.Close()
			if rerr == nil && string(b) == "q3 numbers" {
				t.Fatal("a read without files.download returned the bytes")
			}
		}
	})
	t.Run("delete", func(t *testing.T) {
		hz, path, root := setup(t, perm.FilesDelete)
		target := hz.mustMount(t, path)
		if err := target.Remove("/report.txt"); err == nil {
			t.Fatal("a delete without files.delete succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, "report.txt")); err != nil {
			t.Fatal("the file is gone")
		}
		if _, err := target.Mkdir("/newdir", 0o755); err != nil {
			t.Fatalf("denying delete stopped mkdir: %v", err)
		}
	})
	t.Run("create", func(t *testing.T) {
		hz, path, root := setup(t, perm.FilesCreate)
		target := hz.mustMount(t, path)
		if _, err := target.Mkdir("/nope", 0o755); err == nil {
			t.Fatal("mkdir without files.create succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, "nope")); !os.IsNotExist(err) {
			t.Fatal("the directory was created")
		}
	})
	t.Run("rename", func(t *testing.T) {
		hz, path, root := setup(t, perm.FilesRename)
		target := hz.mustMount(t, path)
		if err := target.Rename("/report.txt", "/moved.txt"); err == nil {
			t.Fatal("a rename without files.rename succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, "report.txt")); err != nil {
			t.Fatal("the file moved")
		}
	})
}

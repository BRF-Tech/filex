package ftpsrv_test

// Per-user permissions (internal/perm) over FTPS: access.ftp at login, and
// each command on the action it performs.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
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

func TestPerm_AccessFTPGatesTheLogin(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	hz.storage(t, "main")
	hz.deny(t, u, perm.AccessFTP)
	if c, err := hz.login(t, "p@example.com", testPassword); err == nil {
		_ = c.Quit()
		t.Fatal("an account without access.ftp logged in")
	}
}

func TestPerm_FTPCommands(t *testing.T) {
	setup := func(t *testing.T, p perm.Perm) (*goftp.ServerConn, string) {
		hz := newHarness(t)
		u := hz.user(t, "p@example.com")
		st := hz.storage(t, "main")
		hz.writeFile(t, st, "report.txt", []byte("q3 numbers"))
		hz.deny(t, u, p)
		c := hz.mustLogin(t, "p@example.com", testPassword)
		t.Cleanup(func() { _ = c.Quit() })
		return c, hz.rootOf(t, st)
	}
	t.Run("download", func(t *testing.T) {
		conn, _ := setup(t, perm.FilesDownload)
		r, err := conn.Retr("/main/report.txt")
		if err == nil {
			b, rerr := io.ReadAll(r)
			r.Close()
			if rerr == nil && string(b) == "q3 numbers" {
				t.Fatal("RETR without files.download returned the bytes")
			}
		}
	})
	t.Run("delete", func(t *testing.T) {
		conn, root := setup(t, perm.FilesDelete)
		if err := conn.Delete("/main/report.txt"); err == nil {
			t.Fatal("DELE without files.delete succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, "report.txt")); err != nil {
			t.Fatal("the file is gone")
		}
		if err := conn.MakeDir("/main/newdir"); err != nil {
			t.Fatalf("denying delete stopped MKD: %v", err)
		}
	})
	t.Run("create", func(t *testing.T) {
		conn, _ := setup(t, perm.FilesCreate)
		if err := conn.Stor("/main/new.txt", strings.NewReader("x")); err == nil {
			t.Fatal("STOR of a new file without files.create succeeded")
		}
		if err := conn.MakeDir("/main/nope"); err == nil {
			t.Fatal("MKD without files.create succeeded")
		}
		if err := conn.Stor("/main/report.txt", strings.NewReader("revised")); err != nil {
			t.Fatalf("overwriting is files.modify, which is held: %v", err)
		}
	})
	t.Run("modify", func(t *testing.T) {
		conn, root := setup(t, perm.FilesModify)
		if err := conn.Stor("/main/report.txt", strings.NewReader("tampered")); err == nil {
			t.Fatal("STOR over an existing file without files.modify succeeded")
		}
		if got, _ := os.ReadFile(filepath.Join(root, "report.txt")); string(got) != "q3 numbers" {
			t.Fatalf("file changed: %q", got)
		}
	})
	t.Run("rename", func(t *testing.T) {
		conn, root := setup(t, perm.FilesRename)
		if err := conn.Rename("/main/report.txt", "/main/moved.txt"); err == nil {
			t.Fatal("RNFR/RNTO without files.rename succeeded")
		}
		if _, err := os.Stat(filepath.Join(root, "report.txt")); err != nil {
			t.Fatal("the file moved")
		}
	})
}

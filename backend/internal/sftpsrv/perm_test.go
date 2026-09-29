package sftpsrv_test

// Per-user permissions (internal/perm) over SFTP: the protocol's access
// permission at login, and each file action on the verb that performs it.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// deny gives u exactly these overrides (and nothing else).
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

func TestPerm_AccessSFTPGatesTheLogin(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	hz.storage(t, "main")
	hz.deny(t, u, perm.AccessSFTP)
	if cl, err := hz.dial(t, "p@example.com", testPassword); err == nil {
		cl.Close()
		t.Fatal("an account without access.sftp logged in")
	}
	// Taking other protocols away does not touch SFTP.
	hz.deny(t, u, perm.AccessFTP, perm.AccessWebDAV)
	hz.mustDial(t, "p@example.com", testPassword).Close()
}

func TestPerm_SFTPFileActions(t *testing.T) {
	cases := []struct {
		name   string
		denied perm.Perm
		// refused performs the action the permission governs and reports
		// whether it was refused.
		refused func(t *testing.T, hz *harness, cl *sftp.Client, root string) bool
		// neighbour is an action a DIFFERENT permission governs; it must
		// still work.
		neighbour func(t *testing.T, cl *sftp.Client) error
	}{
		{
			name: "download", denied: perm.FilesDownload,
			refused: func(t *testing.T, _ *harness, cl *sftp.Client, _ string) bool {
				f, err := cl.Open("/main/report.txt")
				if err != nil {
					return true
				}
				defer f.Close()
				_, err = io.ReadAll(f)
				return err != nil
			},
			neighbour: func(t *testing.T, cl *sftp.Client) error { _, err := cl.Stat("/main/report.txt"); return err },
		},
		{
			name: "delete", denied: perm.FilesDelete,
			refused: func(t *testing.T, _ *harness, cl *sftp.Client, root string) bool {
				err := cl.Remove("/main/report.txt")
				_, statErr := os.Stat(filepath.Join(root, "report.txt"))
				return err != nil && statErr == nil
			},
			neighbour: func(t *testing.T, cl *sftp.Client) error { return cl.Mkdir("/main/newdir") },
		},
		{
			name: "create", denied: perm.FilesCreate,
			refused: func(t *testing.T, _ *harness, cl *sftp.Client, root string) bool {
				errMk := cl.Mkdir("/main/nope")
				errNew := writeAll(cl, "/main/new.txt", "x")
				_, statErr := os.Stat(filepath.Join(root, "new.txt"))
				return errMk != nil && errNew != nil && os.IsNotExist(statErr)
			},
			// Replacing an existing file is files.modify, which is held.
			neighbour: func(t *testing.T, cl *sftp.Client) error { return writeAll(cl, "/main/report.txt", "revised") },
		},
		{
			name: "modify", denied: perm.FilesModify,
			refused: func(t *testing.T, _ *harness, cl *sftp.Client, root string) bool {
				err := writeAll(cl, "/main/report.txt", "tampered")
				got, _ := os.ReadFile(filepath.Join(root, "report.txt"))
				return err != nil && string(got) == "q3 numbers"
			},
			neighbour: func(t *testing.T, cl *sftp.Client) error { return writeAll(cl, "/main/fresh.txt", "new") },
		},
		{
			name: "rename", denied: perm.FilesRename,
			refused: func(t *testing.T, _ *harness, cl *sftp.Client, root string) bool {
				err := cl.Rename("/main/report.txt", "/main/moved.txt")
				_, statErr := os.Stat(filepath.Join(root, "report.txt"))
				return err != nil && statErr == nil
			},
			neighbour: func(t *testing.T, cl *sftp.Client) error { return cl.Remove("/main/report.txt") },
		},
		{
			name: "move", denied: perm.FilesMove,
			refused: func(t *testing.T, _ *harness, cl *sftp.Client, root string) bool {
				if err := cl.Mkdir("/main/sub"); err != nil {
					t.Fatal(err)
				}
				err := cl.Rename("/main/report.txt", "/main/sub/report.txt")
				_, statErr := os.Stat(filepath.Join(root, "report.txt"))
				return err != nil && statErr == nil
			},
			// A new name in the same folder is not a move.
			neighbour: func(t *testing.T, cl *sftp.Client) error { return cl.Rename("/main/report.txt", "/main/renamed.txt") },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hz := newHarness(t)
			u := hz.user(t, "p@example.com")
			st := hz.storage(t, "main")
			hz.writeFile(t, st, "report.txt", []byte("q3 numbers"))
			hz.deny(t, u, c.denied)
			cl := hz.mustDial(t, "p@example.com", testPassword)
			defer cl.Close()

			if !c.refused(t, hz, cl, hz.rootOf(t, st)) {
				t.Fatalf("%s went through without %s", c.name, c.denied)
			}
			if err := c.neighbour(t, cl); err != nil {
				t.Fatalf("denying %s also stopped an unrelated action: %v", c.denied, err)
			}
		})
	}
}

func writeAll(cl *sftp.Client, p, body string) error {
	f, err := cl.Create(p)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(body)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// giveRole creates an enabled role and gives it to p@example.com, the
// account these tests sign in as.
func (hz *harness) giveRole(t *testing.T, r *model.PermissionRule) {
	t.Helper()
	ctx := context.Background()
	r.Enabled = true
	// Written as changes to the User role: its own list is the Standard
	// preset with the account-wide changes applied.
	if r.Permissions == nil {
		set := perm.Standard
		if r.Conditions.Empty() {
			for k, eff := range r.Effects {
				if eff == model.PermAllow {
					set = set.With(perm.Perm(k))
				} else {
					set = set.Without(perm.Perm(k))
				}
			}
			r.Effects = nil
		}
		r.Permissions = set.Strings()
	}
	created, err := hz.store.CreatePermissionRule(ctx, r)
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	u, err := hz.store.GetUserByEmail(ctx, "p@example.com")
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := hz.store.SetUserCustomRole(ctx, u.ID, created.ID); err != nil {
		t.Fatalf("role: %v", err)
	}
	perm.Invalidate()
	t.Cleanup(perm.Invalidate)
}

// rule gives the account a role carrying only these settings.
func (hz *harness) rule(t *testing.T, settings model.PermRuleSettings) {
	t.Helper()
	hz.giveRole(t, &model.PermissionRule{Name: "Policy", Settings: settings})
}

func TestPerm_SFTPRuleSettings(t *testing.T) {
	t.Run("blocked file type", func(t *testing.T) {
		hz := newHarness(t)
		hz.user(t, "p@example.com")
		st := hz.storage(t, "main")
		hz.writeFile(t, st, "report.txt", []byte("q3"))
		hz.rule(t, model.PermRuleSettings{BlockedExtensions: []string{"exe"}})
		cl := hz.mustDial(t, "p@example.com", testPassword)
		defer cl.Close()
		if err := writeAll(cl, "/main/setup.exe", "MZ"); err == nil {
			t.Fatal("an .exe was written over SFTP")
		}
		if err := cl.Rename("/main/report.txt", "/main/report.exe"); err == nil {
			t.Fatal("a rename into .exe went through")
		}
		if err := writeAll(cl, "/main/notes.txt", "fine"); err != nil {
			t.Fatalf("other types: %v", err)
		}
	})
	t.Run("max upload size", func(t *testing.T) {
		hz := newHarness(t)
		hz.user(t, "p@example.com")
		st := hz.storage(t, "main")
		max := int64(8)
		hz.rule(t, model.PermRuleSettings{MaxUploadBytes: &max})
		cl := hz.mustDial(t, "p@example.com", testPassword)
		defer cl.Close()
		if err := writeAll(cl, "/main/big.txt", "0123456789"); err == nil {
			t.Fatal("a file over the limit was accepted")
		}
		if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "big.txt")); !os.IsNotExist(err) {
			t.Fatal("the oversized file landed")
		}
		if err := writeAll(cl, "/main/small.txt", "tiny"); err != nil {
			t.Fatalf("a file under the limit: %v", err)
		}
	})
	t.Run("require 2FA refuses the password", func(t *testing.T) {
		hz := newHarness(t)
		hz.user(t, "p@example.com")
		hz.storage(t, "main")
		hz.rule(t, model.PermRuleSettings{Require2FA: true})
		if cl, err := hz.dial(t, "p@example.com", testPassword); err == nil {
			cl.Close()
			t.Fatal("a password login went round the 2FA rule")
		}
	})
}

func TestPerm_SFTPPathConditionedRule(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Locked/signed.pdf", []byte("signed"))
	hz.writeFile(t, st, "Work/draft.txt", []byte("draft"))
	hz.giveRole(t, &model.PermissionRule{
		Name:       "Locked",
		Effects:    map[string]string{"files.delete": "deny", "files.download": "deny"},
		Conditions: model.PermRuleConditions{StorageIDs: []int64{st.ID}, Paths: []string{"Locked/**"}},
	})
	cl := hz.mustDial(t, "p@example.com", testPassword)
	defer cl.Close()

	if err := cl.Remove("/main/Locked/signed.pdf"); err == nil {
		t.Fatal("deleted under the locked path")
	}
	if f, err := cl.Open("/main/Locked/signed.pdf"); err == nil {
		if b, rerr := io.ReadAll(f); rerr == nil && string(b) == "signed" {
			t.Fatal("downloaded under the locked path")
		}
		f.Close()
	}
	if err := cl.Remove("/main/Work/draft.txt"); err != nil {
		t.Fatalf("outside the path: %v", err)
	}
}

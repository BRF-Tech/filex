package sftpsrv_test

// Per-user permissions (internal/perm) over SFTP: the protocol's access
// permission at login, and each file action on the verb that performs it.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/sftpsrv"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
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

// Who may encrypt (internal/e2epolicy) over SFTP. With the policy off, an
// upload that would CREATE an encrypted folder's key file or a `.fxe` is
// refused at the open and nothing lands; rewriting a key file that is there —
// a password change — and an ordinary file still work.
func TestPerm_SFTPEncryptionPolicy(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Kasa/.filex-e2e.json", []byte(`{"v":2}`))
	hz.writeFile(t, st, "Acik/notes.txt", []byte("plain"))
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	cl := hz.mustDial(t, "p@example.com", testPassword)
	root := hz.rootOf(t, st)

	for _, rel := range []string{"Acik/.filex-e2e.json", "Acik/rapor.pdf.fxe"} {
		if err := writeAll(cl, "/main/"+rel, "x"); err == nil {
			t.Fatalf("%s was created with encryption off", rel)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s landed", rel)
		}
	}
	if err := writeAll(cl, "/main/Kasa/.filex-e2e.json", `{"v":2,"rewritten":true}`); err != nil {
		t.Fatalf("rewriting a key file that is there was refused: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Kasa", ".filex-e2e.json")); string(got) != `{"v":2,"rewritten":true}` {
		t.Fatalf("key file = %q", got)
	}
	if err := writeAll(cl, "/main/Acik/plain.txt", "fine"); err != nil {
		t.Fatalf("an ordinary file: %v", err)
	}
}

// A folder named like an encryption is not the file. An upload onto a folder
// with a key file's or a `.fxe`'s name creates that file — an object store
// keeps it beside the folder — so the rule is asked at the open and, with the
// policy off, refuses there: permission denied at the open, not the driver's
// failure at the close (a local folder cannot be written over).
func TestPerm_SFTPAFolderWithTheNameIsNotTheFile(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	rels := []string{"Acik/.filex-e2e.json", "Acik/x.fxe"}
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	cl := hz.mustDial(t, "p@example.com", testPassword)

	for _, rel := range rels {
		f, err := cl.Create("/main/" + rel)
		if err == nil {
			_ = f.Close()
			t.Fatalf("%s: an upload onto a folder with the name was opened", rel)
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("%s: %v, want the rule's permission denied at the open", rel, err)
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil || !fi.IsDir() {
			t.Fatalf("%s is no longer the folder: %v", rel, err)
		}
	}
}

// A rule that cannot be decided is the server's failure, not a refusal. With
// the policy unreadable (the store fails), the upload of a `.fxe` is refused
// at the open as SSH_FX_FAILURE — not PERMISSION_DENIED, which a client and an
// operator read as the policy doing its job — and nothing lands.
func TestPerm_SFTPUndecidedEncryptionIsAServerFailure(t *testing.T) {
	hz := newHarnessCfg(t, func(c *sftpsrv.Config) {
		c.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: dbtest.SettingFails(c.Store, model.SettingE2EPolicy, errors.New("database is locked"))})
	})
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	cl := hz.mustDial(t, "p@example.com", testPassword)

	f, err := cl.Create("/main/yeni.fxe")
	if err == nil {
		_ = f.Close()
		t.Fatal("an upload of a .fxe was opened with the rule undecidable")
	}
	var se *sftp.StatusError
	if !errors.As(err, &se) || se.FxCode() != sftp.ErrSSHFxFailure {
		t.Fatalf("%v, want SSH_FX_FAILURE", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "yeni.fxe")); !os.IsNotExist(err) {
		t.Fatal("yeni.fxe landed")
	}
	if err := writeAll(cl, "/main/notlar.txt", "plain"); err != nil {
		t.Fatalf("an ordinary file: %v", err)
	}
}

// A server built without the router's rule still asks one: New builds it from
// its store. Nil used to mean "not wired, allow", so losing the line in
// internal/server that hands the rule over switched it off for SFTP under
// every policy, and nothing said so.
func TestPerm_SFTPAsksTheRuleWhenNobodyWiredIt(t *testing.T) {
	hz := newHarnessCfg(t, func(c *sftpsrv.Config) { c.E2EPolicy = nil })
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	cl := hz.mustDial(t, "p@example.com", testPassword)
	if err := writeAll(cl, "/main/yeni.fxe", "x"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("a .fxe with the policy off and no rule handed over: %v, want permission denied", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "yeni.fxe")); !os.IsNotExist(err) {
		t.Fatal("yeni.fxe landed")
	}
}

// Who may encrypt lets through what it should, and spends an approval once.
// Under the default policy (permitted) a plain member uploads a `.fxe`: an
// upgrade changes nobody's access. Under the approval policy one approval for
// a folder lets exactly one key file be made there: the request turns used,
// the next key file — one folder down, which the same approval would have
// covered — is refused, and rewriting the key file that is there is not asked
// at all (were it asked, nothing is left to spend).
func TestPerm_SFTPEncryptionPolicyLetsThePermittedThrough(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	if err := os.MkdirAll(filepath.Join(root, "Acik", "Alt"), 0o755); err != nil {
		t.Fatal(err)
	}
	cl := hz.mustDial(t, "p@example.com", testPassword)
	if err := writeAll(cl, "/main/Acik/izinli.fxe", "x"); err != nil {
		t.Fatalf("a .fxe under the default policy: %v", err)
	}

	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyApproval); err != nil {
		t.Fatal(err)
	}
	r := dbtest.ApproveE2E(t, hz.store, u.ID, st.ID, "Acik", model.E2ERequestFolder)
	if err := writeAll(cl, "/main/Acik/.filex-e2e.json", `{"v":2}`); err != nil {
		t.Fatalf("the approved key file: %v", err)
	}
	if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestUsed {
		t.Fatalf("the approval was not spent (%s)", got)
	}
	if err := writeAll(cl, "/main/Acik/Alt/.filex-e2e.json", `{"v":2}`); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("a second key file on one approval: %v, want permission denied", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Acik", "Alt", ".filex-e2e.json")); !os.IsNotExist(err) {
		t.Fatal("the second key file landed")
	}
	if err := writeAll(cl, "/main/Acik/.filex-e2e.json", `{"v":2,"rewritten":true}`); err != nil {
		t.Fatalf("rewriting the key file that is there: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Acik", ".filex-e2e.json")); string(got) != `{"v":2,"rewritten":true}` {
		t.Fatalf("key file = %q", got)
	}
}

// renameCase is one rename a protocol test asks for, inside the storage.
type renameCase struct{ from, to string }

// layRenames writes what the rename tests need: plain files, a `.fxe` and an
// encrypted folder made before the policy was switched off, a folder to give
// a key file's name, and an empty folder for a key file to be moved into.
func (hz *harness) layRenames(t *testing.T, st *model.Storage) string {
	t.Helper()
	for rel, body := range map[string]string{
		"Acik/izinli.bin": "plain", "Acik/notlar.txt": "plain", "Acik/rapor.bin": "plain", "Acik/m.json": "{}",
		"Acik/a.fxe": "cipher", "Acik/c.fxe": "cipher", "Acik/Dosyalar/not.txt": "plain", "Acik/ek.bin": "plain",
		"Acik/yedek.bin": "plain", "Acik/eski.fxe": "cipher", "Kasa/.filex-e2e.json": `{"v":2}`,
	} {
		hz.writeFile(t, st, rel, []byte(body))
	}
	root := hz.rootOf(t, st)
	if err := os.MkdirAll(filepath.Join(root, "Klasör"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Who may encrypt at a rename (operator decision 2026-09-30). Giving a plain
// file a key file's or a `.fxe`'s name encrypts as surely as creating one, so
// a rename that does it is asked as a write of the file there would be. Under
// the default policy a plain member's goes through; with the policy off it is
// refused — permission denied, and nothing moves. So is a `.fxe` given a key
// file's name, and a key file moved into another folder (operator decision
// 2026-10-03): each encrypts a folder nobody was asked about. A `.fxe` that
// stays a `.fxe` is free, a folder with any name is not a key file, a file
// moved into an encrypted folder keeps its plain name, and a posix-rename onto
// a `.fxe` that is there replaces it unasked, as any overwrite is.
func TestPerm_SFTPRenameOntoAnEncryptionName(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.layRenames(t, st)
	at := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	cl := hz.mustDial(t, "p@example.com", testPassword)
	if err := cl.Rename("/main/Acik/izinli.bin", "/main/Acik/izinli.fxe"); err != nil {
		t.Fatalf("a .fxe by rename under the default policy: %v", err)
	}
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}

	for _, c := range []renameCase{
		{"Acik/rapor.bin", "Acik/rapor.bin.fxe"}, {"Acik/m.json", "Klasör/.filex-e2e.json"},
		{"Acik/c.fxe", "Acik/.filex-e2e.json"}, {"Kasa/.filex-e2e.json", "Klasör/.filex-e2e.json"},
	} {
		if err := cl.Rename("/main/"+c.from, "/main/"+c.to); !errors.Is(err, os.ErrPermission) {
			t.Errorf("%s → %s with encryption off: %v, want permission denied", c.from, c.to, err)
		}
		if _, err := os.Stat(at(c.from)); err != nil {
			t.Errorf("%s moved: %v", c.from, err)
		}
		if _, err := os.Stat(at(c.to)); !os.IsNotExist(err) {
			t.Errorf("%s landed", c.to)
		}
	}
	for _, c := range []renameCase{
		{"Acik/notlar.txt", "Acik/notlar-2.txt"}, {"Acik/a.fxe", "Acik/b.fxe"},
		{"Acik/ek.bin", "Kasa/ek.bin"}, {"Acik/Dosyalar", "Acik/.filex-e2e.json"},
	} {
		if err := cl.Rename("/main/"+c.from, "/main/"+c.to); err != nil {
			t.Errorf("%s → %s: %v", c.from, c.to, err)
		}
		if _, err := os.Stat(at(c.to)); err != nil {
			t.Errorf("%s did not arrive: %v", c.to, err)
		}
	}
	if fi, err := os.Stat(at("Acik/.filex-e2e.json")); err != nil || !fi.IsDir() {
		t.Errorf("Acik/.filex-e2e.json is not the folder: %v", err)
	}
	if err := cl.PosixRename("/main/Acik/yedek.bin", "/main/Acik/eski.fxe"); err != nil {
		t.Fatalf("posix-rename onto a .fxe that is there: %v", err)
	}
	if got, _ := os.ReadFile(at("Acik/eski.fxe")); string(got) != "plain" {
		t.Fatalf("eski.fxe = %q, want it replaced", got)
	}
}

// A rule that cannot be decided is the server's failure at a rename too:
// SSH_FX_FAILURE, not PERMISSION_DENIED, and nothing moves.
func TestPerm_SFTPUndecidedRenameIsAServerFailure(t *testing.T) {
	hz := newHarnessCfg(t, func(c *sftpsrv.Config) {
		c.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: dbtest.SettingFails(c.Store, model.SettingE2EPolicy, errors.New("database is locked"))})
	})
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "rapor.bin", []byte("plain"))
	cl := hz.mustDial(t, "p@example.com", testPassword)

	err := cl.Rename("/main/rapor.bin", "/main/rapor.bin.fxe")
	var se *sftp.StatusError
	if !errors.As(err, &se) || se.FxCode() != sftp.ErrSSHFxFailure {
		t.Fatalf("%v, want SSH_FX_FAILURE", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "rapor.bin")); err != nil {
		t.Fatalf("rapor.bin moved: %v", err)
	}
}

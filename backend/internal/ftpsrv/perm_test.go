package ftpsrv_test

// Per-user permissions (internal/perm) over FTPS: access.ftp at login, and
// each command on the action it performs.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/ftpsrv"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
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

// Who may encrypt (internal/e2epolicy) over FTPS. With the policy off, a STOR
// that would CREATE an encrypted folder's key file or a `.fxe` is refused and
// nothing lands; rewriting a key file that is there — a password change — and
// an ordinary file still work.
func TestPerm_FTPEncryptionPolicy(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Kasa/.filex-e2e.json", []byte(`{"v":2}`))
	hz.writeFile(t, st, "Acik/notes.txt", []byte("plain"))
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })
	root := hz.rootOf(t, st)

	for _, rel := range []string{"Acik/.filex-e2e.json", "Acik/rapor.pdf.fxe"} {
		if err := conn.Stor("/main/"+rel, strings.NewReader("x")); err == nil {
			t.Fatalf("%s was created with encryption off", rel)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s landed", rel)
		}
	}
	if err := conn.Stor("/main/Kasa/.filex-e2e.json", strings.NewReader(`{"v":2,"rewritten":true}`)); err != nil {
		t.Fatalf("rewriting a key file that is there was refused: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Kasa", ".filex-e2e.json")); string(got) != `{"v":2,"rewritten":true}` {
		t.Fatalf("key file = %q", got)
	}
	if err := conn.Stor("/main/Acik/plain.txt", strings.NewReader("fine")); err != nil {
		t.Fatalf("an ordinary file: %v", err)
	}
}

// A folder named like an encryption is not the file. A STOR onto a folder
// with a key file's or a `.fxe`'s name creates that file — an object store
// keeps it beside the folder — so the rule is asked at the open and, with the
// policy off, refuses there ("permission denied"), not the driver at the
// close (a local folder cannot be written over, in words of its own).
func TestPerm_FTPAFolderWithTheNameIsNotTheFile(t *testing.T) {
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
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })

	for _, rel := range rels {
		err := conn.Stor("/main/"+rel, strings.NewReader("x"))
		if err == nil {
			t.Fatalf("%s: a STOR onto a folder with the name went through", rel)
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s: %v, want the rule's permission denied at the open", rel, err)
		}
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil || !fi.IsDir() {
			t.Fatalf("%s is no longer the folder: %v", rel, err)
		}
	}
}

// A rule that cannot be decided is the server's failure, not a refusal. With
// the policy unreadable (the store fails), a STOR of a `.fxe` is refused and
// nothing lands — and the reply says the check failed, not "permission
// denied". The code stays 550: ftpserverlib answers every error opening an
// upload with 550 (552 and 553 are for quota and names), so 451 cannot be
// reached from here; the words are what tell the two apart.
func TestPerm_FTPUndecidedEncryptionIsAServerFailure(t *testing.T) {
	hz := newHarnessCfg(t, func(c *ftpsrv.Config) {
		c.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: dbtest.SettingFails(c.Store, model.SettingE2EPolicy, errors.New("database is locked"))})
	})
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })

	err := conn.Stor("/main/yeni.fxe", strings.NewReader("x"))
	if err == nil {
		t.Fatal("a STOR of a .fxe went through with the rule undecidable")
	}
	if !strings.Contains(err.Error(), "could not check the encryption policy") || strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("%v, want the check's failure rather than a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "yeni.fxe")); !os.IsNotExist(err) {
		t.Fatal("yeni.fxe landed")
	}
	if err := conn.Stor("/main/notlar.txt", strings.NewReader("plain")); err != nil {
		t.Fatalf("an ordinary file: %v", err)
	}
}

// A server built without the router's rule still asks one: New builds it from
// its store. Nil used to mean "not wired, allow", so losing the line in
// internal/server that hands the rule over switched it off for FTPS under
// every policy, and nothing said so.
func TestPerm_FTPAsksTheRuleWhenNobodyWiredIt(t *testing.T) {
	hz := newHarnessCfg(t, func(c *ftpsrv.Config) { c.E2EPolicy = nil })
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })
	if err := conn.Stor("/main/yeni.fxe", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("a .fxe with the policy off and no rule handed over: %v, want permission denied", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "yeni.fxe")); !os.IsNotExist(err) {
		t.Fatal("yeni.fxe landed")
	}
}

// Who may encrypt lets through what it should, and spends an approval once.
// Under the default policy (permitted) a plain member STORs a `.fxe`: an
// upgrade changes nobody's access. Under the approval policy one approval for
// a folder lets exactly one key file be made there: the request turns used,
// the next key file — one folder down, which the same approval would have
// covered — is refused, and rewriting the key file that is there is not asked
// at all (were it asked, nothing is left to spend).
func TestPerm_FTPEncryptionPolicyLetsThePermittedThrough(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	if err := os.MkdirAll(filepath.Join(root, "Acik", "Alt"), 0o755); err != nil {
		t.Fatal(err)
	}
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })
	if err := conn.Stor("/main/Acik/izinli.fxe", strings.NewReader("x")); err != nil {
		t.Fatalf("a .fxe under the default policy: %v", err)
	}

	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyApproval); err != nil {
		t.Fatal(err)
	}
	r := dbtest.ApproveE2E(t, hz.store, u.ID, st.ID, "Acik", model.E2ERequestFolder)
	if err := conn.Stor("/main/Acik/.filex-e2e.json", strings.NewReader(`{"v":2}`)); err != nil {
		t.Fatalf("the approved key file: %v", err)
	}
	if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestUsed {
		t.Fatalf("the approval was not spent (%s)", got)
	}
	if err := conn.Stor("/main/Acik/Alt/.filex-e2e.json", strings.NewReader(`{"v":2}`)); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("a second key file on one approval: %v, want permission denied", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Acik", "Alt", ".filex-e2e.json")); !os.IsNotExist(err) {
		t.Fatal("the second key file landed")
	}
	if err := conn.Stor("/main/Acik/.filex-e2e.json", strings.NewReader(`{"v":2,"rewritten":true}`)); err != nil {
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
// an RNFR/RNTO that does it is asked as a STOR of the file there would be.
// Under the default policy a plain member's goes through; with the policy off
// it is refused — "permission denied", and nothing moves. So is a `.fxe`
// given a key file's name, and a key file moved into another folder (operator
// decision 2026-10-03): each encrypts a folder nobody was asked about. A
// `.fxe` that stays a `.fxe` is free, a folder with any name is not a key
// file, a file moved into an encrypted folder keeps its plain name, and a
// rename onto a `.fxe` that is there replaces it unasked, as any overwrite is.
func TestPerm_FTPRenameOntoAnEncryptionName(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.layRenames(t, st)
	at := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })
	if err := conn.Rename("/main/Acik/izinli.bin", "/main/Acik/izinli.fxe"); err != nil {
		t.Fatalf("a .fxe by rename under the default policy: %v", err)
	}
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}

	for _, c := range []renameCase{
		{"Acik/rapor.bin", "Acik/rapor.bin.fxe"}, {"Acik/m.json", "Klasör/.filex-e2e.json"},
		{"Acik/c.fxe", "Acik/.filex-e2e.json"}, {"Kasa/.filex-e2e.json", "Klasör/.filex-e2e.json"},
	} {
		if err := conn.Rename("/main/"+c.from, "/main/"+c.to); err == nil || !strings.Contains(err.Error(), "permission denied") {
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
		{"Acik/yedek.bin", "Acik/eski.fxe"},
	} {
		if err := conn.Rename("/main/"+c.from, "/main/"+c.to); err != nil {
			t.Errorf("%s → %s: %v", c.from, c.to, err)
		}
		if _, err := os.Stat(at(c.to)); err != nil {
			t.Errorf("%s did not arrive: %v", c.to, err)
		}
	}
	if fi, err := os.Stat(at("Acik/.filex-e2e.json")); err != nil || !fi.IsDir() {
		t.Errorf("Acik/.filex-e2e.json is not the folder: %v", err)
	}
	if got, _ := os.ReadFile(at("Acik/eski.fxe")); string(got) != "plain" {
		t.Errorf("eski.fxe = %q, want it replaced", got)
	}
}

// A rule that cannot be decided is the server's failure at a rename too: the
// reply says the check failed, not "permission denied" (the code stays 550,
// ftpserverlib's for any failed RNTO), and nothing moves.
func TestPerm_FTPUndecidedRenameIsAServerFailure(t *testing.T) {
	hz := newHarnessCfg(t, func(c *ftpsrv.Config) {
		c.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: dbtest.SettingFails(c.Store, model.SettingE2EPolicy, errors.New("database is locked"))})
	})
	hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "rapor.bin", []byte("plain"))
	conn := hz.mustLogin(t, "p@example.com", testPassword)
	t.Cleanup(func() { _ = conn.Quit() })

	err := conn.Rename("/main/rapor.bin", "/main/rapor.bin.fxe")
	if err == nil || !strings.Contains(err.Error(), "could not check the encryption policy") || strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("%v, want the check's failure rather than a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "rapor.bin")); err != nil {
		t.Fatalf("rapor.bin moved: %v", err)
	}
}

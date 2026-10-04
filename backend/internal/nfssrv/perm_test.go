package nfssrv_test

// Per-user permissions (internal/perm) over NFS: access.nfs at mount, and
// each operation on the action it performs.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	nfsclient "github.com/willscott/go-nfs-client/nfs"

	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/nfssrv"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
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

// Who may encrypt (internal/e2epolicy) over NFS. With the policy off, the
// CREATE of an encrypted folder's key file or a `.fxe` is refused and nothing
// lands; rewriting a key file that is there (WRITEs, no CREATE) and an
// ordinary file still work.
func TestPerm_NFSEncryptionPolicy(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "Kasa/.filex-e2e.json", []byte(`{"v":2}`))
	hz.writeFile(t, st, "Acik/notes.txt", []byte("plain"))
	path := hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"})
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	target := hz.mustMount(t, path)
	root := hz.rootOf(t, st)

	for _, rel := range []string{"Acik/.filex-e2e.json", "Acik/rapor.pdf.fxe"} {
		if wr, err := target.OpenFile("/"+rel, 0o644); err == nil {
			_, _ = wr.Write([]byte("x"))
			_ = wr.Close()
			t.Fatalf("%s was created with encryption off", rel)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s landed", rel)
		}
	}
	wr, err := target.OpenFile("/Kasa/.filex-e2e.json", 0o644)
	if err != nil {
		t.Fatalf("opening a key file that is there: %v", err)
	}
	if _, err := wr.Write([]byte(`{"v":2,"rewritten":true}`)); err != nil {
		t.Fatalf("rewriting a key file that is there: %v", err)
	}
	if err := wr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Kasa", ".filex-e2e.json")); string(got) != `{"v":2,"rewritten":true}` {
		t.Fatalf("key file = %q", got)
	}
	wr, err = target.OpenFile("/Acik/plain.txt", 0o644)
	if err != nil {
		t.Fatalf("an ordinary file: %v", err)
	}
	_, _ = wr.Write([]byte("fine"))
	if err := wr.Close(); err != nil {
		t.Fatalf("an ordinary file: %v", err)
	}
}

// A server built without the router's rule still asks one: New builds it from
// its store. Nil used to mean "not wired, allow", so losing the line in
// internal/server that hands the rule over switched it off for NFS under every
// policy, and nothing said so.
func TestPerm_NFSAsksTheRuleWhenNobodyWiredIt(t *testing.T) {
	hz := newHarnessCfg(t, func(c *nfssrv.Config) { c.E2EPolicy = nil })
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	path := hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"})
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}
	target := hz.mustMount(t, path)
	if wr, err := target.OpenFile("/yeni.fxe", 0o644); err == nil {
		_, _ = wr.Write([]byte("x"))
		_ = wr.Close()
		t.Fatal("a .fxe was created with the policy off and no rule handed over")
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "yeni.fxe")); !os.IsNotExist(err) {
		t.Fatal("yeni.fxe landed")
	}
}

// write creates or opens p over the mount and writes body at its start.
func write(target *nfsclient.Target, p, body string) error {
	wr, err := target.OpenFile(p, 0o644)
	if err != nil {
		return err
	}
	if _, err := wr.Write([]byte(body)); err != nil {
		_ = wr.Close()
		return err
	}
	return wr.Close()
}

// Who may encrypt lets through what it should, and spends an approval once.
// Under the default policy (permitted) a plain member creates a `.fxe`: an
// upgrade changes nobody's access. Under the approval policy one approval for
// a folder lets exactly one key file be made there — its CREATE spends it,
// and the WRITEs that follow find the file there and are not asked — the
// request turns used, the next key file (one folder down, which the same
// approval would have covered) is refused, and rewriting the key file that is
// there is not asked at all (were it asked, nothing is left to spend).
func TestPerm_NFSEncryptionPolicyLetsThePermittedThrough(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	if err := os.MkdirAll(filepath.Join(root, "Acik", "Alt"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	if err := write(target, "/Acik/izinli.fxe", "x"); err != nil {
		t.Fatalf("a .fxe under the default policy: %v", err)
	}

	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyApproval); err != nil {
		t.Fatal(err)
	}
	r := dbtest.ApproveE2E(t, hz.store, u.ID, st.ID, "Acik", model.E2ERequestFolder)
	if err := write(target, "/Acik/.filex-e2e.json", `{"v":2}`); err != nil {
		t.Fatalf("the approved key file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "Acik", ".filex-e2e.json")); string(got) != `{"v":2}` {
		t.Fatalf("key file = %q: the WRITEs after the CREATE did not land", got)
	}
	if got := dbtest.E2EStatus(t, hz.store, r.ID); got != model.E2ERequestUsed {
		t.Fatalf("the approval was not spent (%s)", got)
	}
	if err := write(target, "/Acik/Alt/.filex-e2e.json", `{"v":2}`); err == nil {
		t.Fatal("a second key file was created on one approval")
	}
	if _, err := os.Stat(filepath.Join(root, "Acik", "Alt", ".filex-e2e.json")); !os.IsNotExist(err) {
		t.Fatal("the second key file landed")
	}
	if err := write(target, "/Acik/.filex-e2e.json", `{"v":2,"rewritten":true}`); err != nil {
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

// nfsStatus is the NFS3 status a refused call answered, 0 for none.
func nfsStatus(err error) uint32 {
	var ne *nfsclient.Error
	if errors.As(err, &ne) {
		return ne.ErrorNum
	}
	return 0
}

// Who may encrypt at a rename (operator decision 2026-09-30). Giving a plain
// file a key file's or a `.fxe`'s name encrypts as surely as creating one, so
// a RENAME that does it is asked as the CREATE of the file there would be.
// Under the default policy a plain member's goes through; with the policy off
// it is refused — NFS3ERR_ACCES, and nothing moves. So is a `.fxe` given a
// key file's name, and a key file moved into another folder (operator
// decision 2026-10-03): each encrypts a folder nobody was asked about. A
// `.fxe` that stays a `.fxe` is free, a folder with any name is not a key
// file, a file moved into an encrypted folder keeps its plain name, and a
// RENAME onto a `.fxe` that is there replaces it unasked, as any overwrite is.
func TestPerm_NFSRenameOntoAnEncryptionName(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	root := hz.layRenames(t, st)
	at := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	if err := target.Rename("/Acik/izinli.bin", "/Acik/izinli.fxe"); err != nil {
		t.Fatalf("a .fxe by rename under the default policy: %v", err)
	}
	if err := hz.store.UpsertSetting(context.Background(), model.SettingE2EPolicy, model.E2EPolicyOff); err != nil {
		t.Fatal(err)
	}

	for _, c := range []renameCase{
		{"Acik/rapor.bin", "Acik/rapor.bin.fxe"}, {"Acik/m.json", "Klasör/.filex-e2e.json"},
		{"Acik/c.fxe", "Acik/.filex-e2e.json"}, {"Kasa/.filex-e2e.json", "Klasör/.filex-e2e.json"},
	} {
		if err := target.Rename("/"+c.from, "/"+c.to); nfsStatus(err) != nfsclient.NFS3ErrAcces {
			t.Errorf("%s → %s with encryption off: %v, want NFS3ERR_ACCES", c.from, c.to, err)
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
		if err := target.Rename("/"+c.from, "/"+c.to); err != nil {
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

// A rule that cannot be decided is the server's failure at a rename:
// NFS3ERR_IO, not the NFS3ERR_ACCES of a refusal, and nothing moves.
func TestPerm_NFSUndecidedRenameIsAServerFailure(t *testing.T) {
	hz := newHarnessCfg(t, func(c *nfssrv.Config) {
		c.E2EPolicy = e2epolicy.New(e2epolicy.Options{Store: dbtest.SettingFails(c.Store, model.SettingE2EPolicy, errors.New("database is locked"))})
	})
	u := hz.user(t, "p@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "rapor.bin", []byte("plain"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	if err := target.Rename("/rapor.bin", "/rapor.bin.fxe"); nfsStatus(err) != nfsclient.NFS3ErrIO {
		t.Fatalf("%v, want NFS3ERR_IO", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "rapor.bin")); err != nil {
		t.Fatalf("rapor.bin moved: %v", err)
	}
}

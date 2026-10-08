package ftpsrv_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/ftpsrv"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
)

// TestVaultInsidesAreNotWrittenOverFTP: inside a vault folder only the vault
// API writes (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else): FTPS
// answers 550 to every change in it, and the vault folder itself stays an
// ordinary folder.
func TestVaultInsidesAreNotWrittenOverFTP(t *testing.T) {
	hz := newHarnessCfg(t, func(c *ftpsrv.Config) {
		c.ACL.AttachVaults(vaultlock.NewFinder(c.Store, c.Resolver))
	})
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	seed := dbtest.SeedVault(t, hz.store, st.ID, root, "Kasa")
	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	before := map[string][]byte{}
	for _, p := range []string{seed.KeyFile, seed.Index, seed.Pack} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		before[p] = b
	}

	if err := c.Stor("/main/Kasa/v/idx/0000000000000002.fxi", strings.NewReader("filexvlt")); err == nil {
		t.Error("an index file was written into the vault over FTP")
	}
	if err := c.Stor("/main/"+seed.Pack, strings.NewReader("replaced")); err == nil {
		t.Error("a pack was replaced over FTP")
	}
	if err := c.MakeDir("/main/Kasa/v/p/zz"); err == nil {
		t.Error("a folder was made inside the vault")
	}
	if err := c.Delete("/main/" + seed.Index); err == nil {
		t.Error("an index file was deleted")
	}
	if err := c.Rename("/main/"+seed.KeyFile, "/main/Kasa/kf.json"); err == nil {
		t.Error("the key file was moved on its own")
	}
	if err := c.Delete("/main/" + seed.KeyFile); err == nil {
		t.Error("the key file was deleted on its own")
	}
	if _, err := os.Stat(filepath.Join(root, "Kasa", "v", "idx", "0000000000000002.fxi")); err == nil {
		t.Error("the refused index file is on the disk")
	}
	for p, want := range before {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("%s was changed: %q (%v)", p, b, err)
		}
	}
	if err := c.Rename("/main/Kasa", "/main/Kasa-2"); err != nil {
		t.Fatalf("renaming the vault folder: %v", err)
	}
}

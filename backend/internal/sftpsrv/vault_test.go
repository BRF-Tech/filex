package sftpsrv_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/sftpsrv"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
)

// TestVaultInsidesAreNotWrittenOverSFTP: inside a vault folder only the vault
// API writes (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else). Over
// SFTP nothing is created, replaced, renamed or deleted in it, and the key
// file stays where it is - while the vault folder itself is an ordinary
// folder: it can be renamed.
func TestVaultInsidesAreNotWrittenOverSFTP(t *testing.T) {
	hz := newHarnessCfg(t, func(c *sftpsrv.Config) {
		c.ACL.AttachVaults(vaultlock.NewFinder(c.Store, c.Resolver))
	})
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	seed := dbtest.SeedVault(t, hz.store, st.ID, root, "Kasa")
	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	before := map[string][]byte{}
	for _, p := range []string{seed.KeyFile, seed.Index, seed.Pack} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		before[p] = b
	}

	if w, err := cl.Create("/main/Kasa/v/p/aa/aa000000000000000000000000000000.fxp"); err == nil {
		_, _ = w.Write([]byte("filexvlt"))
		_ = w.Close()
	}
	if _, err := os.Stat(filepath.Join(root, "Kasa", "v", "p", "aa", "aa000000000000000000000000000000.fxp")); err == nil {
		t.Error("a pack was written into the vault over SFTP")
	}
	if w, err := cl.Create("/main/" + seed.Pack); err == nil {
		_, _ = w.Write([]byte("replaced"))
		_ = w.Close()
	}
	if err := cl.Mkdir("/main/Kasa/v/p/zz"); err == nil {
		t.Error("a folder was made inside the vault")
	}
	if err := cl.Remove("/main/" + seed.Index); err == nil {
		t.Error("an index file was deleted")
	}
	if err := cl.Rename("/main/"+seed.Pack, "/main/Kasa/v/p/b0/moved.fxp"); err == nil {
		t.Error("a pack was renamed")
	}
	if err := cl.Rename("/main/"+seed.KeyFile, "/main/Kasa/kf.json"); err == nil {
		t.Error("the key file was moved on its own")
	}
	if err := cl.Remove("/main/" + seed.KeyFile); err == nil {
		t.Error("the key file was deleted on its own")
	}
	for p, want := range before {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("%s was changed: %q (%v)", p, b, err)
		}
	}

	// The folder itself is not inside the vault.
	if err := cl.Rename("/main/Kasa", "/main/Kasa-2"); err != nil {
		t.Fatalf("renaming the vault folder: %v", err)
	}
}

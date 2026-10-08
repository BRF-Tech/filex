package nfssrv_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/nfssrv"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
)

// TestVaultInsidesAreNotWrittenOverNFS: inside a vault folder only the vault
// API writes (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else): an NFS
// mount gets NFS3ERR_ACCES for every change in it, and the vault folder
// itself stays an ordinary folder.
func TestVaultInsidesAreNotWrittenOverNFS(t *testing.T) {
	hz := newHarnessCfg(t, func(c *nfssrv.Config) {
		c.ACL.AttachVaults(vaultlock.NewFinder(c.Store, c.Resolver))
	})
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	seed := dbtest.SeedVault(t, hz.store, st.ID, root, "Kasa")
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	before := map[string][]byte{}
	for _, p := range []string{seed.KeyFile, seed.Index, seed.Pack} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		before[p] = b
	}

	if wr, err := target.OpenFile("/Kasa/v/idx/0000000000000002.fxi", 0o644); err == nil {
		_, _ = wr.Write([]byte("filexvlt"))
		_ = wr.Close()
	}
	if _, err := os.Stat(filepath.Join(root, "Kasa", "v", "idx", "0000000000000002.fxi")); err == nil {
		t.Error("an index file was written into the vault over NFS")
	}
	if wr, err := target.OpenFile("/"+seed.Pack, 0o644); err == nil {
		_, _ = wr.Write([]byte("replaced"))
		_ = wr.Close()
	}
	if _, err := target.Mkdir("/Kasa/v/p/zz", 0o755); err == nil {
		t.Error("a folder was made inside the vault")
	}
	if err := target.Remove("/" + seed.Index); err == nil {
		t.Error("an index file was deleted")
	}
	if err := target.Rename("/"+seed.KeyFile, "/Kasa/kf.json"); err == nil {
		t.Error("the key file was moved on its own")
	}
	for p, want := range before {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("%s was changed: %q (%v)", p, b, err)
		}
	}
	if err := target.Rename("/Kasa", "/Kasa-2"); err != nil {
		t.Fatalf("renaming the vault folder: %v", err)
	}
}

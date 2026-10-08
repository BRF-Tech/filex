package s3api_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
)

// TestVaultInsidesAreNotWrittenOverS3: inside a vault folder only the vault
// API writes (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else). The S3
// gateway answers AccessDenied to a PUT, a copy or a DELETE of anything in
// it, the key file included; an object beside the vault is written as ever.
func TestVaultInsidesAreNotWrittenOverS3(t *testing.T) {
	hz := newHarnessCfg(t, false, func(c *s3api.Config) {
		c.ACL.AttachVaults(vaultlock.NewFinder(c.Store, c.Resolver))
	})
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	seed := dbtest.SeedVault(t, hz.store, st.ID, root, "Kasa")
	hz.writeFile(t, st, "other.txt", []byte("x"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	before := map[string][]byte{}
	for _, p := range []string{seed.KeyFile, seed.Index, seed.Pack} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		before[p] = b
	}

	if rec := hz.put(t, key, "https://s3.filex.test/main/Kasa/v/idx/0000000000000002.fxi", []byte("filexvlt")); rec.Code != http.StatusForbidden {
		t.Errorf("a PUT into the vault = %d, want 403", rec.Code)
	}
	if rec := hz.put(t, key, "https://s3.filex.test/main/"+seed.Pack, []byte("replaced")); rec.Code != http.StatusForbidden {
		t.Errorf("a PUT over a pack = %d, want 403", rec.Code)
	}
	if rec := hz.put(t, key, "https://s3.filex.test/main/"+seed.KeyFile, []byte(`{"v":3}`)); rec.Code != http.StatusForbidden {
		t.Errorf("a PUT over the key file = %d, want 403", rec.Code)
	}
	if rec := hz.copy(t, key, "https://s3.filex.test/main/Kasa/other.txt", "/main/other.txt"); rec.Code != http.StatusForbidden {
		t.Errorf("a copy into the vault = %d, want 403", rec.Code)
	}
	for _, p := range []string{seed.Index, seed.Pack, seed.KeyFile} {
		if rec := hz.do(t, key, http.MethodDelete, "https://s3.filex.test/main/"+p); rec.Code != http.StatusForbidden {
			t.Errorf("a DELETE of %s = %d, want 403", p, rec.Code)
		}
	}
	for p, want := range before {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s was changed: %q (%v)", p, got, err)
		}
	}
	for _, p := range []string{"Kasa/v/idx/0000000000000002.fxi", "Kasa/other.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			t.Errorf("%s was written into the vault", p)
		}
	}
	// Beside the vault, the gateway writes as it always did.
	if rec := hz.put(t, key, "https://s3.filex.test/main/Kasa-notes.txt", []byte("beside")); rec.Code != http.StatusOK {
		t.Errorf("a PUT beside the vault = %d, want 200", rec.Code)
	}
}

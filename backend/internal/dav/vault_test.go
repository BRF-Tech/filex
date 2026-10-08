package dav

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
)

// TestVaultInsidesAreNotWrittenOverWebDAV: inside a vault folder only the
// vault API writes (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else). A
// mapped drive gets 403 for every change in it - a PUT, a MKCOL, a DELETE, a
// MOVE out of it or into it, a COPY into it - and the key file stays where it
// is. The vault folder itself is an ordinary folder: it moves.
func TestVaultInsidesAreNotWrittenOverWebDAV(t *testing.T) {
	ha := newHarnessWith(t, func(s db.Store) db.Store { return s }, func(c *Config) {
		c.ACL.AttachVaults(vaultlock.NewFinder(c.Store, c.Resolver))
	})
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	seed := dbtest.SeedVault(t, ha.store, st.ID, root, "Kasa")
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside.txt"), []byte("plain"), 0o644))
	before := map[string][]byte{}
	for _, p := range []string{seed.KeyFile, seed.Index, seed.Pack} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		require.NoError(t, err)
		before[p] = b
	}

	for _, c := range []struct {
		method, path, body string
		hdr                map[string]string
	}{
		{http.MethodPut, "/dav/depo/Kasa/v/idx/0000000000000002.fxi", "filexvlt", nil},
		{http.MethodPut, "/dav/depo/" + seed.Pack, "replaced", nil},
		{"MKCOL", "/dav/depo/Kasa/v/p/zz", "", nil},
		{"DELETE", "/dav/depo/" + seed.Index, "", nil},
		{"DELETE", "/dav/depo/Kasa/v", "", nil},
		{"MOVE", "/dav/depo/" + seed.Pack, "", map[string]string{"Destination": "/dav/depo/pack.fxp"}},
		{"MOVE", "/dav/depo/outside.txt", "", map[string]string{"Destination": "/dav/depo/Kasa/v/outside.txt"}},
		{"COPY", "/dav/depo/outside.txt", "", map[string]string{"Destination": "/dav/depo/Kasa/outside.txt"}},
		{"MOVE", "/dav/depo/" + seed.KeyFile, "", map[string]string{"Destination": "/dav/depo/Kasa/kf.json"}},
		{"DELETE", "/dav/depo/" + seed.KeyFile, "", nil},
	} {
		resp := ha.req(t, c.method, c.path, ha.adminEmail, ha.adminPass, c.body, c.hdr)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403: a write inside the vault was not refused", c.method, c.path, resp.StatusCode)
		}
	}
	for p, want := range before {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		require.NoError(t, err, "%s is gone", p)
		require.True(t, bytes.Equal(want, got), "%s was changed", p)
	}
	for _, p := range []string{"Kasa/v/idx/0000000000000002.fxi", "Kasa/outside.txt", "Kasa/v/outside.txt"} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)))
		require.True(t, os.IsNotExist(err), "%s was written into the vault", p)
	}

	resp := ha.req(t, "MOVE", "/dav/depo/Kasa", ha.adminEmail, ha.adminPass, "", map[string]string{"Destination": "/dav/depo/Kasa-2"})
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "the vault folder itself moves")
}

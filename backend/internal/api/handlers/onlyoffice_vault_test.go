package handlers_test

// The document server's save is one more door into a vault folder, and the
// vault rule (docs/E2E-VAULT-FORMAT.md → Writes from anywhere else: inside a
// vault only the vault API writes) has to hold there as at every other. The
// rule lives on the router's ACL resolver (AttachVaults); the save gates built
// their own (acl.New), which knows no vault. Through the whole router, so the
// wiring is what is tested: red on the old code, where the save below lands.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestOnlyOfficeSave_NeverWritesInsideAVault(t *testing.T) {
	root := t.TempDir()
	srv, store := editorFixtureWith(t, root, func(c *config.Config) { c.E2EVault = true })
	ctx := context.Background()
	st, err := store.GetStorageByName(ctx, "oo")
	require.NoError(t, err)
	dbtest.SeedVault(t, store, st.ID, root, "Kasa")

	// A document planted inside the vault folder behind filex's back, and one
	// outside it.
	const inside, outside = "Kasa/rapor.docx", "Documents/Rapor.docx"
	seedServerFile(t, store, "oo", inside, "PLANTED")
	seedServerFile(t, store, "oo", outside, "OUTSIDE")
	nodeOf := func(rel string) *model.Node {
		n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+rel))
		require.NoError(t, err, rel)
		require.NotNil(t, n, rel)
		return n
	}
	onDisk := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		return string(b)
	}

	status, body := editorSave(t, srv.URL, nodeOf(inside).ID, "EDITED IN THE BROWSER")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":1`, "the editor's save inside the vault was accepted: %s", body)
	assert.Contains(t, body, "only the vault API writes", body)
	assert.NotContains(t, body, "rapor.docx", "the answer names no path")
	assert.Equal(t, "PLANTED", onDisk(inside), "the editor wrote inside the vault")

	// The rule is the vault's, not the editor's.
	status, body = editorSave(t, srv.URL, nodeOf(outside).ID, "EDITED IN THE BROWSER")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":0`, "a save outside the vault was refused: %s", body)
	assert.Equal(t, "EDITED IN THE BROWSER", onDisk(outside))
}

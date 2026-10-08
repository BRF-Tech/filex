package onlyoffice

// Inside a vault folder only the vault API writes (docs/E2E-VAULT-FORMAT.md →
// Writes from anywhere else) - and the document server's save is a write like
// any other. The vault rule lives on the ONE ACL resolver every door shares
// (acl.Resolver.AttachVaults); the save gates here built a fresh acl.New, which
// knows no vault, so a document planted inside a vault folder was saved over
// from the editor, and a save in another format was written beside it, inside
// the vault.
//
// ⚠ Red on the old code: there the Service has no AttachACL, and with the
// resolver handed in any other way the save landed (the bytes on disk below).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// vaultHarness is a document harness whose storage holds a vault at "Kasa",
// with the service wired to a resolver that knows it - as BuildRouter wires
// the router's.
func vaultHarness(t *testing.T) *csvHarness {
	t.Helper()
	h := newDocHarness(t, "outside.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "OUTSIDE")
	dbtest.SeedVault(t, h.store, h.node.StorageID, h.root, "Kasa")
	r := acl.New(h.store)
	r.AttachVaults(vaultlock.NewFinder(h.store, func(int64) (storage.Driver, error) { return h.drv, nil }))
	h.svc.AttachACL(r)
	return h
}

// plant puts a document inside the vault folder behind filex's back (on the
// storage and in the catalogue) and makes it the harness's document.
func (h *csvHarness) plant(t *testing.T, rel, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(h.root, filepath.FromSlash(rel)), []byte(content), 0o644))
	n, err := h.store.CreateNode(context.Background(), &model.Node{
		StorageID: h.node.StorageID, Name: filepath.Base(rel), Path: "/" + rel,
		PathHash: pathkey.Hash(h.node.StorageID, "/"+rel), StorageKey: "/" + rel,
		Type: model.NodeTypeFile, Size: int64(len(content)), SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	h.node = n
}

func (h *csvHarness) onDisk(t *testing.T, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return "", false
	}
	require.NoError(t, err)
	return string(b), true
}

func TestCallback_NeverSavesOverADocumentInsideAVault(t *testing.T) {
	h := vaultHarness(t)
	h.plant(t, "Kasa/rapor.docx", "PLANTED")

	resp := h.save(t, docxBytes, "docx")
	assert.Equal(t, 1, resp["error"], "the save inside the vault was accepted: %v", resp)
	assert.Equal(t, writegate.ErrVaultPath.Error(), resp["message"], "a constant, never the path")
	got, _ := h.onDisk(t, "Kasa/rapor.docx")
	assert.Equal(t, "PLANTED", got, "the editor wrote inside the vault")
}

func TestCallback_NeverSavesBesideADocumentInsideAVault(t *testing.T) {
	h := vaultHarness(t)
	h.plant(t, "Kasa/eski.doc", "THE OLD .DOC")
	ada := h.user(t, "ada@example.com", "admin")
	h.open(t, ada)

	resp := h.save(t, docxBytes, "docx", idStrings(ada)...)
	assert.Equal(t, 1, resp["error"], "the save beside a document inside the vault was accepted: %v", resp)
	assert.Equal(t, writegate.ErrVaultPath.Error(), resp["message"])
	_, written := h.onDisk(t, "Kasa/eski.docx")
	assert.False(t, written, "a new file was written inside the vault")
	got, _ := h.onDisk(t, "Kasa/eski.doc")
	assert.Equal(t, "THE OLD .DOC", got)
}

// The rule is the vault's, not the editor's: with the same resolver a
// document outside the vault saves as it always has.
func TestCallback_AVaultLeavesTheDocumentsOutsideItAlone(t *testing.T) {
	h := vaultHarness(t)

	resp := h.save(t, docxBytes, "docx")
	assert.Equal(t, 0, resp["error"], "%v", resp)
	got, _ := h.onDisk(t, "outside.docx")
	assert.Equal(t, docxBytes, got)
	h.sink.wait(t)
}

package handlers_test

// An app's freeze holds at EVERY door.
//
// While a signature request is open the signing app locks the document
// (`files:lock`) and the screen promises "Dosya … dondurulmuş; imzalar
// toplanırken kimse değiştiremez". That promise held for the explorer's own
// verbs (aclLockWithin in manager/ops/save-text/upload/staged) and nowhere
// else. These tests walk the other doors against one frozen document and
// require it unchanged afterwards.

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// freeze locks rel on storage st the way the signing app does
// (wasmplugin hfFileLock): one row, the holder's id and name, a reason.
func freeze(t *testing.T, store db.Store, st *model.Storage, rel string, pluginID int64) {
	t.Helper()
	until := time.Now().Add(24 * time.Hour)
	require.NoError(t, store.PutAppPluginLock(context.Background(), &model.AppPluginLock{
		StorageID: st.ID, PathHash: pathkey.Hash(st.ID, "/"+rel), Rel: rel,
		PluginID: pluginID, PluginName: "sign", Reason: "imzalar toplanıyor", Until: &until,
	}))
}

// assertLocked: a refused write answers 423 and names the app.
func assertLocked(t *testing.T, what string, status int, body string) {
	t.Helper()
	assert.Equal(t, http.StatusLocked, status, "%s was let through: %s", what, body)
	assert.Contains(t, body, "sign", "%s: %s", what, body)
}

func TestLocks_EveryHTTPDoorHonoursAFreeze(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	const frozen = "Sozlesmeler/NDA.docx"
	fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": "alpha://", "name": "Sozlesmeler"})
	fxUpload(t, f.URL, tok, "alpha://Sozlesmeler", "NDA.docx", "the document under signature")
	fxUpload(t, f.URL, tok, "alpha://", "other.txt", "another file")
	freeze(t, f.Store, f.StA, frozen, 991)

	// ── AI / MCP ─────────────────────────────────────────────────────────
	for _, c := range []struct {
		what, url string
		body      map[string]any
	}{
		{"AI write over the frozen file", "/api/ai/upload", map[string]any{"path": "alpha://" + frozen, "content": "changed by an agent"}},
		{"AI delete of its folder", "/api/ai/delete", map[string]any{"path": "alpha://Sozlesmeler"}},
		{"AI move of the frozen file", "/api/ai/move", map[string]any{"src": "alpha://" + frozen, "dst": "alpha://NDA-moved.docx"}},
		{"AI move onto the frozen file", "/api/ai/move", map[string]any{"src": "alpha://other.txt", "dst": "alpha://" + frozen}},
	} {
		resp := aiReq(t, http.DefaultClient, "POST", f.URL+c.url, tok, c.body)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		assertLocked(t, c.what, resp.StatusCode, string(b))
	}

	// ── archive extraction over it ───────────────────────────────────────
	zipBytes := buildZip(t, map[string]string{"Sozlesmeler/NDA.docx": "replaced by an archive", "fresh.txt": "fine"})
	fxUpload(t, f.URL, tok, "alpha://", "bundle.zip", string(zipBytes))
	status, body := fxPost(t, f.URL+"/api/files/archive/extract", tok, map[string]any{"storage_id": f.StA.ID, "path": "bundle.zip", "dest": ""})
	require.Equal(t, http.StatusAccepted, status, body)
	f.drainOps(t)
	assert.FileExists(t, filepath.Join(f.RootA, "fresh.txt"), "precondition: the other member was extracted")

	// ── the operations queue ─────────────────────────────────────────────
	// (A copy or move INTO its folder is allowed and de-collides — the queue
	// never writes over an existing file — so the move of the folder itself
	// is what a freeze has to stop here.)
	status, body = fxPost(t, f.URL+"/api/files/move", tok, map[string]any{"source": []string{"alpha://Sozlesmeler"}, "target": "alpha://Arsiv/"})
	assertLocked(t, "a queued move of the folder holding it", status, body)
	status, body = fxPost(t, f.URL+"/api/files/ops", tok, map[string]any{"kind": "delete", "storage_id": f.StA.ID, "sources": []string{frozen}})
	assertLocked(t, "a queued delete through the unified endpoint", status, body)

	// ── trash restore onto a frozen path ─────────────────────────────────
	fxUpload(t, f.URL, tok, "alpha://Sozlesmeler", "Ek.docx", "an attachment")
	ek, err := f.Store.GetNodeByPath(ctx, f.StA.ID, pathkey.Hash(f.StA.ID, "/Sozlesmeler/Ek.docx"))
	require.NoError(t, err)
	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{"path": "alpha://Sozlesmeler", "items": []map[string]string{{"path": "alpha://Sozlesmeler/Ek.docx"}}})
	require.Equal(t, http.StatusOK, status, body)
	freeze(t, f.Store, f.StA, "Sozlesmeler/Ek.docx", 991)
	status, body = fxPost(t, f.URL+"/api/files/manager/restore", tok, map[string]any{"node_id": ek.ID})
	assertLocked(t, "a trash restore onto a frozen path", status, body)

	// ── the explorer's own verbs (these always held — pinned here too) ───
	status, body = fxUploadStatus(t, f.URL, tok, "alpha://Sozlesmeler", "NDA.docx", "uploaded over it")
	assertLocked(t, "an upload over the frozen file", status, body)
	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{"path": "alpha://", "items": []map[string]string{{"path": "alpha://Sozlesmeler"}}})
	assertLocked(t, "deleting its folder", status, body)

	got, err := os.ReadFile(filepath.Join(f.RootA, filepath.FromSlash(frozen)))
	require.NoError(t, err, "the frozen document is gone")
	assert.Equal(t, "the document under signature", string(got), "the frozen document was changed")
}

// The document editor: a document opened before the freeze and saved after it
// — the save callback is a write like any other.
func TestLocks_EditorSaveAndVersionRestoreHonourAFreeze(t *testing.T) {
	root := t.TempDir()
	srv, store := editorFixture(t, root)
	ctx := context.Background()
	st, err := store.GetStorageByName(ctx, "oo")
	require.NoError(t, err)
	adminID, _ := testutil.SeedAdminUser(t, store)
	admin := issueToken(t, store, adminID, fullScopes, nil)
	seedServerFile(t, store, "oo", "Documents/Rapor.docx", "the document under signature")
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/Documents/Rapor.docx"))
	require.NoError(t, err)
	v, err := store.CreateNodeVersion(ctx, &model.NodeVersion{NodeID: n.ID, VersionN: 1, StorageKey: ".versions/1/1", Size: 1})
	require.NoError(t, err)
	freeze(t, store, st, "Documents/Rapor.docx", 991)

	status, body := editorSave(t, srv.URL, n.ID, "EDITED AFTER THE FREEZE")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":1`, "the editor's save of a frozen document was accepted: %s", body)
	assert.Contains(t, body, "sign", "the refusal does not say who froze it: %s", body)

	status, body = fxPost(t, srv.URL+"/api/files/versions/restore", admin, map[string]any{"node_id": n.ID, "version_id": v.ID})
	assertLocked(t, "a version restore of a frozen document", status, body)

	got, err := os.ReadFile(filepath.Join(root, "Documents", "Rapor.docx"))
	require.NoError(t, err)
	assert.Equal(t, "the document under signature", string(got), "the frozen document was changed")
}

// An app's output: the app holding the lock writes the signed document over
// the file it froze; another app's output — or a write with no app behind it —
// does not.
func TestLocks_OnlyTheHolderAppWritesIntoAFrozenFile(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	resolver := func(id int64) (storage.Driver, error) {
		d := &local.Driver{}
		if err := d.Init(ctx, map[string]any{"root": f.RootA}); err != nil {
			return nil, err
		}
		return d, nil
	}
	require.NoError(t, os.MkdirAll(filepath.Join(f.RootA, "imza"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.RootA, "imza", "NDA.pdf"), []byte("unsigned"), 0o644))
	freeze(t, f.Store, f.StA, "imza/NDA.pdf", 991)
	h := handlers.NewAppPlugins(nil, f.Store, acl.New(f.Store), nil, resolver, nil, nil)

	err := h.CommitVersion(writegate.WithApp(ctx, 992), f.StA.ID, "imza/NDA.pdf", strings.NewReader("another app's output"), 20, nil)
	assert.ErrorIs(t, err, writegate.ErrLocked, "another app wrote into a frozen document")
	err = h.CommitVersion(ctx, f.StA.ID, "imza/NDA.pdf", strings.NewReader("no app at all"), 13, nil)
	assert.ErrorIs(t, err, writegate.ErrLocked, "a write with no app behind it went through")
	got, _ := os.ReadFile(filepath.Join(f.RootA, "imza", "NDA.pdf"))
	assert.Equal(t, "unsigned", string(got))

	// ⚠⚠ The holder must still write: that is what the freeze is for.
	require.NoError(t, h.CommitVersion(writegate.WithApp(ctx, 991), f.StA.ID, "imza/NDA.pdf", strings.NewReader("signed"), 6, nil))
	got, _ = os.ReadFile(filepath.Join(f.RootA, "imza", "NDA.pdf"))
	assert.Equal(t, "signed", string(got), "the app holding the lock could not write its signed output")
}

// A freeze taken while a large upload was still arriving stops the rest of
// it: every chunk and the commit ask writegate again (StagedUpload.authorize).
func TestLocks_AFreezeStopsAStagedUploadMidway(t *testing.T) {
	f := newStagedFixture(t)
	src := randomBytes(10000)
	total := int64(len(src))
	code, begun := f.begin(t, map[string]any{"path": "main://", "name": "NDA.docx", "size": total, "chunk_size": 4096})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id, _ := begun["id"].(string)
	code, put := f.putChunk(t, id, 0, 4096, total, src[:4096])
	require.Equal(t, http.StatusOK, code, "%v", put)

	freeze(t, f.store, f.storage, "NDA.docx", 991)

	code, put = f.putChunk(t, id, 4096, 4096, total, src[4096:8192])
	assert.Equal(t, http.StatusLocked, code, "a chunk after the freeze was accepted: %v", put)
	code, done := f.commit(t, id)
	assert.Equal(t, http.StatusLocked, code, "the commit after the freeze was accepted: %v", done)
	_, err := os.Stat(filepath.Join(f.rootDir, "NDA.docx"))
	assert.True(t, os.IsNotExist(err), "the upload landed on the frozen path")
}

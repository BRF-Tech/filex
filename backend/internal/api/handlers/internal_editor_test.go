package handlers_test

// The document editor (OnlyOffice) is the fourth way the desktop's open-with
// round trip writes into `.filex-open`: its editor window opens the working
// copy, and the editor's save is how the edit gets back. So that save must
// keep working — and nothing ELSE among filex's own may be opened for editing.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/versioning"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

func editorModeOf(t *testing.T, body string) string {
	t.Helper()
	var cfg struct {
		EditorConfig struct {
			Mode string `json:"mode"`
		} `json:"editorConfig"`
		Config struct {
			EditorConfig struct {
				Mode string `json:"mode"`
			} `json:"editorConfig"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &cfg), body)
	if cfg.EditorConfig.Mode != "" {
		return cfg.EditorConfig.Mode
	}
	return cfg.Config.EditorConfig.Mode
}

// ooSign is a document server's HS256 callback token.
func ooSign(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	p, err := json.Marshal(claims)
	require.NoError(t, err)
	head := enc.EncodeToString(h) + "." + enc.EncodeToString(p)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(head))
	return head + "." + enc.EncodeToString(mac.Sum(nil))
}

// editorFixture is a server with a local storage "oo" at root, versioning and
// an OnlyOffice service whose JWT secret is "onlyoffice-test-secret".
func editorFixture(t *testing.T, root string) (*httptest.Server, db.Store) {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.StorageResolver = func(id int64) (storage.Driver, error) {
			drv := &local.Driver{}
			if err := drv.Init(context.Background(), map[string]any{"root": root}); err != nil {
				return nil, err
			}
			return drv, nil
		}
		d.Versions = versioning.New(d.Store, d.StorageResolver)
		d.OnlyOffice = onlyoffice.New(d.Store, d.StorageResolver,
			"http://ds.test", "onlyoffice-test-secret", "http://test.local", time.Hour)
	})
	// ⚠ BuildRouter with a versioning service installs the process-wide
	// overwrite guard over THIS test's store. Left behind, every later test in
	// the binary that writes without building its own router snapshots through
	// a closed database ("versioning: lookup …: sql: database is closed" —
	// TestPublicDrop_* failed that way when run after this one).
	t.Cleanup(func() { writehook.ConfigureOverwriteGuard(nil) })
	cfgJSON, err := json.Marshal(map[string]any{"root": root})
	require.NoError(t, err)
	_, err = store.CreateStorage(context.Background(), &model.Storage{
		Name: "oo", Driver: "local", MountPath: "/oo", ConfigJSON: cfgJSON,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true, RBACEnabled: true,
	})
	require.NoError(t, err)
	return srv, store
}

// editorSave is the document server's save callback for nodeID, signed, with
// a stand-in document server serving content.
func editorSave(t *testing.T, base string, nodeID int64, content string) (int, string) {
	t.Helper()
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(content))
	}))
	t.Cleanup(ds.Close)
	claims := map[string]any{"key": "k", "status": 2, "url": ds.URL + "/saved.docx"}
	body := map[string]any{"key": "k", "status": 2, "url": ds.URL + "/saved.docx",
		"token": ooSign(t, "onlyoffice-test-secret", claims)}
	return fxPost(t, fmt.Sprintf("%s/api/files/onlyoffice/callback?node=%d", base, nodeID), "", body)
}

func TestInternalDirs_EditorSavesOnlyTheWorkingCopy(t *testing.T) {
	root := t.TempDir()
	srv, store := editorFixture(t, root)
	ctx := context.Background()
	st, err := store.GetStorageByName(ctx, "oo")
	require.NoError(t, err)
	adminID, _ := testutil.SeedAdminUser(t, store)
	admin := issueToken(t, store, adminID, fullScopes, nil)

	const workCopy = ".filex-open/a1b2c3d4e5f6-Bütçe.docx"
	for _, rel := range []string{"Documents/Rapor.docx", workCopy, ".versions/7/1.docx", ".filex-trash/1726-ab__Eski.docx"} {
		seedServerFile(t, store, "oo", rel, "PK not really a docx")
	}
	nodeOf := func(rel string) *model.Node {
		n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+rel))
		require.NoError(t, err, rel)
		return n
	}
	config := func(tok string, payload map[string]any) (int, string) {
		return fxPost(t, srv.URL+"/api/files/onlyoffice/config", tok, payload)
	}

	// ⚠⚠ The desktop's editor window: path shape, mode=edit (web/src/views/
	// Editor.vue). It must stay editable, or the edit never comes back.
	status, body := config(admin, map[string]any{"path": "oo://" + workCopy, "mode": "edit"})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "edit", editorModeOf(t, body), "the desktop's working copy opened read-only")

	// Anything else among filex's own opens read-only, whichever way it is asked.
	for _, rel := range []string{".versions/7/1.docx", ".filex-trash/1726-ab__Eski.docx"} {
		for _, payload := range []map[string]any{
			{"node_id": nodeOf(rel).ID, "mode": "edit"},
			{"node_id": nodeOf(rel).ID}, // no mode at all
		} {
			status, body := config(admin, payload)
			require.Equal(t, http.StatusOK, status, body)
			assert.Equal(t, "view", editorModeOf(t, body), "%s opened for editing (%v)", rel, payload)
		}
	}

	save := func(rel string) (int, string) {
		return editorSave(t, srv.URL, nodeOf(rel).ID, "EDITED IN THE BROWSER")
	}
	onDisk := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		return string(b)
	}

	// ⚠⚠ The desktop's working copy: this save is how the edit reaches the
	// person's own file. It must be written.
	status, body = save(workCopy)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":0`, "the editor's save of the working copy was refused: %s", body)
	assert.Equal(t, "EDITED IN THE BROWSER", onDisk(workCopy), "the working copy's save did not land")

	// Belt and braces for the downgrade: a save for a version snapshot is not
	// written, even signed.
	status, body = save(".versions/7/1.docx")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"error":1`, "a save for a version snapshot was accepted: %s", body)
	assert.Equal(t, "PK not really a docx", onDisk(".versions/7/1.docx"), "a version snapshot was overwritten")

	// ⚠ Found on the way: a request that left `mode` out got an EDIT session
	// even for a viewer — the downgrade only looked for the literal "edit".
	testutil.SeedRegularUser(t, store, "viewer@oo.test", "ViewerPass1!")
	viewer, err := store.GetUserByEmail(ctx, "viewer@oo.test")
	require.NoError(t, err)
	status, body = fxPost(t, srv.URL+"/api/files/permissions", admin,
		map[string]any{"path": "oo://Documents", "user_id": viewer.ID, "level": "viewer"})
	require.Equal(t, http.StatusOK, status, "grant viewer: %s", body)
	vtok := issueToken(t, store, viewer.ID, fullScopes, nil)
	status, body = config(vtok, map[string]any{"path": "oo://Documents/Rapor.docx"})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "view", editorModeOf(t, body), "a viewer who left out `mode` was handed an editor")

	// Version restore of a working copy writes into the work area.
	wc := nodeOf(workCopy)
	v, err := store.CreateNodeVersion(ctx, &model.NodeVersion{NodeID: wc.ID, VersionN: 1, StorageKey: ".versions/1/1", Size: 1})
	require.NoError(t, err)
	status, body = fxPost(t, srv.URL+"/api/files/versions/restore", admin, map[string]any{"node_id": wc.ID, "version_id": v.ID})
	assertReserved(t, "a version restore into the work area", status, body)
}

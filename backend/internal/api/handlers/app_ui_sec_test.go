package handlers_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// ── Security review, feat/app-ui: the interface's save (UI-7, UI-9, UI-15) ──

func (f *appFixture) adminID(t *testing.T) int64 {
	t.Helper()
	users, err := f.store.ListUsers(context.Background())
	require.NoError(t, err)
	for _, u := range users {
		if u.Email == "admin@test.local" {
			return u.ID
		}
	}
	t.Fatal("no seeded admin")
	return 0
}

// UI-7: an interface's save is a write like any other: it counts against the
// person's quota, and a save over it is refused before a byte is written.
func TestAppUISec_ASaveCountsAgainstTheQuota(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	id := f.adminID(t)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/quota/"+strconv.FormatInt(id, 10), strings.NewReader(`{"quota_bytes": 8}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Less(t, res.StatusCode, 300, string(body))

	code, out := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), strings.Repeat("x", 4096))
	assert.Equal(t, http.StatusInsufficientStorage, code, out)
	assert.Contains(t, out, "quota_exceeded")
	assert.Equal(t, "v1", f.readFile(t, "doc.sketch"), "nothing written")

	code, out = f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://")+"&name=big.sketch", strings.Repeat("x", 4096))
	assert.Equal(t, http.StatusInsufficientStorage, code, out)
}

// UI-9: "save as" is held to what a save over a file is held to: the kind of
// file the view opens, and never into an encrypted folder.
func TestAppUISec_SaveAsIsHeldToTheSameRules(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	code, out := f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://")+"&name=evil.html", "<script>")
	assert.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Contains(t, out, "not_applicable")

	f.markEncrypted(t, f.st, f.root, "kasa")
	code, out = f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://kasa")+"&name=plain.sketch", "plaintext")
	assert.Equal(t, http.StatusForbidden, code, out)
	assert.Contains(t, out, "encrypted")
	_, err := os.Stat(filepath.Join(f.root, "kasa", "plain.sketch"))
	assert.True(t, os.IsNotExist(err), "nothing was written")
}

// markEncrypted makes dir an encrypted folder the way the browser does: the
// marker file on the storage and in the node cache the server reads it from.
func (f *appFixture) markEncrypted(t *testing.T, st *model.Storage, root, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, dir, ".filex-e2e.json"), []byte("{}"), 0o644))
	ctx := context.Background()
	d, err := f.store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: dir, Path: "/" + dir,
		PathHash: pathkey.Hash(st.ID, dir), Type: model.NodeTypeDirectory, Etag: "d"})
	require.NoError(t, err)
	_, err = f.store.CreateNode(ctx, &model.Node{StorageID: st.ID, ParentID: &d.ID, Name: ".filex-e2e.json", Path: "/" + dir + "/.filex-e2e.json",
		PathHash: pathkey.Hash(st.ID, dir+"/.filex-e2e.json"), Type: model.NodeTypeFile, Size: 2, Etag: "m"})
	require.NoError(t, err)
}

// UI-9, the job's half: a folder a person chooses for a job's result is the
// same check (checkOutputFolder), so an encrypted one is refused there too.
func TestAppUISec_AChosenFolderIsNeverAnEncryptedOne(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "upper" {
				a["view"] = "hello"
				a["output"] = map[string]any{"mode": "sibling", "name": "{stem}-upper{ext}", "elsewhere": true}
			}
		}
	})
	f.writeFile(t, "docs/a.txt", "hello")
	f.markEncrypted(t, f.st, f.root, "kasa")
	status, raw := f.helloSubmit(t, f.admin, "main://docs/a.txt", map[string]any{"output_mode": "folder", "output_dir": "main://kasa"})
	assert.Equal(t, http.StatusForbidden, status, string(raw))
	assert.Contains(t, string(raw), "encrypted")
	status, raw = f.helloSubmit(t, f.admin, "main://docs/a.txt", map[string]any{"output_mode": "folder", "output_dir": "main://docs"})
	assert.Equal(t, http.StatusAccepted, status, string(raw), "an ordinary folder still takes it")
}

// UI-15: what the server says to an interface is a code and a sentence of
// filex's own — never an internal error text (a path on disk, a driver's
// message). The client maps them too; this is the server half.
func TestAppUISec_AFailedSaveSaysACodeNotTheInternals(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	code, out := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://missing/nothing.sketch"), "x")
	assert.Equal(t, http.StatusNotFound, code, out)
	assert.NotContains(t, out, f.root, "no path on the server's disk")

	// A write the disk refuses: the driver's error names the folder on the
	// server's disk. The interface is told a code.
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "sealed"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(f.root, "sealed"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(f.root, "sealed"), 0o755) })
	code, out = f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://sealed")+"&name=n.sketch", "x")
	assert.Equal(t, http.StatusInternalServerError, code, out)
	assert.Contains(t, out, `"save_failed"`)
	assert.NotContains(t, out, f.root, "no path on the server's disk")
	assert.NotContains(t, out, "permission denied", "no driver's words")
}

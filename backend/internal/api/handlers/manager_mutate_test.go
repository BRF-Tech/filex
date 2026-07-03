package handlers_test

// Smoke tests for the Vuefinder mutation verbs (newfolder/rename/move/
// delete/upload). These exercise the handler against a real local FS
// driver + the in-memory SQLite store, so the assertions cover both
// the byte-level driver effect and the DB cache mirror.

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// newMutateFixture spins up a temp-dir backed local driver, registers a
// matching storage row, and returns the Manager handler wired to both.
func newMutateFixture(t *testing.T) (*handlers.Manager, db.Store, *local.Driver, *model.Storage, string) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	dir := t.TempDir()

	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": dir}))

	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(dir) + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	mh := handlers.NewManager(store, resolver)
	return mh, store, drv, st, dir
}

func escapeJSON(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

// mutTestPathHash mirrors the unexported mutTestPathHash helper in
// manager_mutate.go so the external test package can verify cache rows.
func mutTestPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

// callMutate POSTs body to /api/files/manager?action=…, returning the
// raw response so callers can assert status + JSON shape.
func callMutate(t *testing.T, mh *handlers.Manager, action string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/files/manager?action="+action, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mh.Mutate(rec, req)
	return rec
}

// ---------- 503 short-circuit ----------

func TestManagerMutate_NoResolver_503(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	mh := handlers.NewManager(store, nil) // explicit nil resolver
	rec := callMutate(t, mh, "newfolder", map[string]any{"path": "main://", "name": "x"})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// ---------- newfolder ----------

func TestManagerMutate_NewFolder_OK(t *testing.T) {
	mh, store, _, st, root := newMutateFixture(t)

	rec := callMutate(t, mh, "newfolder", map[string]any{
		"path": "main://",
		"name": "alpha",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Driver: dir was created on disk.
	info, err := os.Stat(filepath.Join(root, "alpha"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// DB cache: row was mirrored.
	hash := mutTestPathHash(st.ID, "/alpha")
	n, err := store.GetNodeByPath(context.Background(), st.ID, hash)
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Equal(t, model.NodeTypeDirectory, n.Type)
	assert.Equal(t, "alpha", n.Name)

	// Response body: ManagerResponse for the parent dir.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "main", resp["adapter"])
}

func TestManagerMutate_NewFolder_BadName(t *testing.T) {
	mh, _, _, _, _ := newMutateFixture(t)
	rec := callMutate(t, mh, "newfolder", map[string]any{"path": "main://", "name": "with/slash"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestManagerMutate_NewFolder_UnknownAdapter(t *testing.T) {
	mh, _, _, _, _ := newMutateFixture(t)
	rec := callMutate(t, mh, "newfolder", map[string]any{"path": "ghost://", "name": "x"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ---------- rename ----------

func TestManagerMutate_Rename_OK(t *testing.T) {
	mh, store, _, st, root := newMutateFixture(t)

	// Pre-create the source dir + its DB row.
	require.NoError(t, os.Mkdir(filepath.Join(root, "before"), 0o755))
	hash := mutTestPathHash(st.ID, "/before")
	_, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID,
		Name:      "before",
		Path:      "/before",
		PathHash:  hash,
		Type:      model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	rec := callMutate(t, mh, "rename", map[string]any{
		"path": "main://",
		"item": "main://before",
		"name": "after",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, err = os.Stat(filepath.Join(root, "before"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(root, "after"))
	assert.NoError(t, err)
}

// ---------- move ----------

func TestManagerMutate_Move_OK(t *testing.T) {
	mh, store, _, st, root := newMutateFixture(t)

	// Source file + dest dir on disk; mirror the dest dir in DB so
	// the post-move re-render walks succeed (matches real-world flow
	// where the sync worker has already picked up the dest dir).
	require.NoError(t, os.WriteFile(filepath.Join(root, "src.txt"), []byte("hi"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "dest"), 0o755))
	_, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID,
		Name:      "dest",
		Path:      "/dest",
		PathHash:  mutTestPathHash(st.ID, "/dest"),
		Type:      model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	rec := callMutate(t, mh, "move", map[string]any{
		"path":  "main://dest",
		"items": []map[string]any{{"path": "main://src.txt"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, statErr := os.Stat(filepath.Join(root, "src.txt"))
	assert.True(t, os.IsNotExist(statErr))
	_, statErr = os.Stat(filepath.Join(root, "dest", "src.txt"))
	assert.NoError(t, statErr)
}

// ---------- delete ----------

func TestManagerMutate_Delete_OK(t *testing.T) {
	mh, store, _, st, root := newMutateFixture(t)

	require.NoError(t, os.WriteFile(filepath.Join(root, "doomed.txt"), []byte("x"), 0o644))
	hash := mutTestPathHash(st.ID, "/doomed.txt")
	created, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID,
		Name:      "doomed.txt",
		Path:      "/doomed.txt",
		PathHash:  hash,
		Type:      model.NodeTypeFile,
		Size:      1,
	})
	require.NoError(t, err)

	rec := callMutate(t, mh, "delete", map[string]any{
		"path":  "main://",
		"items": []map[string]any{{"path": "main://doomed.txt"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, err = os.Stat(filepath.Join(root, "doomed.txt"))
	assert.True(t, os.IsNotExist(err))

	got, err := store.GetNode(context.Background(), created.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.DeletedAt)
}

// ---------- upload ----------

func TestManagerMutate_Upload_OK(t *testing.T) {
	mh, _, _, _, root := newMutateFixture(t)

	// Build a multipart body with a single file[].
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("path", "main://"))
	part, err := mw.CreateFormFile("file[]", "hello.txt")
	require.NoError(t, err)
	_, err = io.WriteString(part, "hello world")
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/files/manager?action=upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mh.Mutate(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(got))
}

func TestManagerMutate_BadAction(t *testing.T) {
	mh, _, _, _, _ := newMutateFixture(t)
	rec := callMutate(t, mh, "wat", map[string]any{"path": "main://"})
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

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
	"github.com/brf-tech/filex/backend/internal/trash"
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

// ---------- delete folder → trash (GitHub issue #5) ----------

// seedTrashFolderFixture creates alpha/ + alpha/a.txt on disk and as
// parent-linked DB rows, returning (dirNode, childNode).
func seedTrashFolderFixture(t *testing.T, store db.Store, st *model.Storage, root string) (*model.Node, *model.Node) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, os.Mkdir(filepath.Join(root, "alpha"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "alpha", "a.txt"), []byte("x"), 0o644))
	dir, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID,
		Name:      "alpha",
		Path:      "/alpha",
		PathHash:  mutTestPathHash(st.ID, "/alpha"),
		Type:      model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	child, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID,
		ParentID:  &dir.ID,
		Name:      "a.txt",
		Path:      "/alpha/a.txt",
		PathHash:  mutTestPathHash(st.ID, "/alpha/a.txt"),
		Type:      model.NodeTypeFile,
		Size:      1,
	})
	require.NoError(t, err)
	return dir, child
}

// Regression for issue #5.1: trashing a folder must drag the cached child
// rows into the trash path-wise — previously only the folder row was
// retagged while children kept their ORIGINAL paths as soft-deleted rows.
func TestManagerMutate_DeleteFolder_RetagsChildren(t *testing.T) {
	mh, store, _, st, root := newMutateFixture(t)
	ctx := context.Background()
	dir, child := seedTrashFolderFixture(t, store, st, root)

	rec := callMutate(t, mh, "delete", map[string]any{
		"path":  "main://",
		"items": []map[string]any{{"path": "main://alpha"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// On disk: alpha moved (not deleted) under .filex-trash, child intact.
	_, err := os.Stat(filepath.Join(root, "alpha"))
	assert.True(t, os.IsNotExist(err))
	matches, err := filepath.Glob(filepath.Join(root, ".filex-trash", "*__alpha", "a.txt"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "child file must follow the folder into the trash")

	// Folder row: soft-deleted + retagged, original path in storage_key.
	gotDir, err := store.GetNode(ctx, dir.ID)
	require.NoError(t, err)
	require.NotNil(t, gotDir.DeletedAt)
	assert.True(t, strings.HasPrefix(gotDir.Path, "/.filex-trash/"), gotDir.Path)
	assert.Equal(t, "/alpha", gotDir.StorageKey)

	// Child row: FOLLOWED the folder (issue #5.1) — trash-prefixed path,
	// original path preserved in storage_key, parent link intact.
	gotChild, err := store.GetNode(ctx, child.ID)
	require.NoError(t, err)
	require.NotNil(t, gotChild.DeletedAt)
	assert.Equal(t, gotDir.Path+"/a.txt", gotChild.Path)
	assert.Equal(t, "/alpha/a.txt", gotChild.StorageKey)
	require.NotNil(t, gotChild.ParentID)
	assert.Equal(t, dir.ID, *gotChild.ParentID)

	// The original path_hash slot is free again.
	live, _ := store.GetNodeByPath(ctx, st.ID, mutTestPathHash(st.ID, "/alpha/a.txt"))
	assert.Nil(t, live)

	// Regression for issue #5.2: even with the soft-deleted child still
	// holding (storage, parent, name), the sync worker must be able to
	// create a fresh live row at the same (parent, name) — the unique
	// index is soft-delete-aware since migration 00018.
	relive, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID,
		ParentID:  &dir.ID,
		Name:      "a.txt",
		Path:      "/alpha/a.txt",
		PathHash:  mutTestPathHash(st.ID, "/alpha/a.txt"),
		Type:      model.NodeTypeFile,
	})
	require.NoError(t, err, "issue #5.2: soft-deleted (parent,name) row must not block a fresh create")
	require.NotNil(t, relive)
}

// Round trip: trash a folder with a child, then restore it via the trash
// service — bytes and DB rows must land back at the original locations.
func TestManagerMutate_DeleteFolder_RestoreRoundTrip(t *testing.T) {
	mh, store, drv, st, root := newMutateFixture(t)
	ctx := context.Background()
	dir, child := seedTrashFolderFixture(t, store, st, root)

	rec := callMutate(t, mh, "delete", map[string]any{
		"path":  "main://",
		"items": []map[string]any{{"path": "main://alpha"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	trashSvc := trash.New(store, func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}, nil)
	require.NoError(t, trashSvc.Restore(ctx, dir.ID))

	// Bytes are back at the original location.
	b, err := os.ReadFile(filepath.Join(root, "alpha", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "x", string(b))

	// Rows are live again at their original paths (same ids).
	gotDir, err := store.GetNode(ctx, dir.ID)
	require.NoError(t, err)
	assert.Nil(t, gotDir.DeletedAt)
	assert.Equal(t, "/alpha", gotDir.Path)
	gotChild, err := store.GetNode(ctx, child.ID)
	require.NoError(t, err)
	assert.Nil(t, gotChild.DeletedAt)
	assert.Equal(t, "/alpha/a.txt", gotChild.Path)
	assert.Equal(t, mutTestPathHash(st.ID, "/alpha/a.txt"), gotChild.PathHash)
	require.NotNil(t, gotChild.ParentID)
	assert.Equal(t, dir.ID, *gotChild.ParentID)

	// Live lookups resolve again.
	liveChild, _ := store.GetNodeByPath(ctx, st.ID, mutTestPathHash(st.ID, "/alpha/a.txt"))
	require.NotNil(t, liveChild)
	assert.Equal(t, child.ID, liveChild.ID)
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

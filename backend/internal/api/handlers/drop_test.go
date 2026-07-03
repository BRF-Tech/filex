package handlers_test

// End-to-end tests for the public file-drop (upload link) handler. They wire
// the real Drop handler to a temp-dir local driver + in-memory SQLite store
// (reusing newMutateFixture / mutTestPathHash from manager_mutate_test.go) and
// drive it over a chi router, so the assertions cover the byte-level effect,
// the DB cache mirror AND the blind-drop guarantee.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// newDropFixture builds a router exposing POST /api/files/share (create) and
// the public /d/{token} drop endpoints, all wired to one store + local driver.
func newDropFixture(t *testing.T) (*chi.Mux, *share.Service, db.Store, *model.Storage, string) {
	t.Helper()
	mh, store, drv, st, root := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	svc := share.NewService(store)
	shareH := handlers.NewShare(svc, store, resolver, "")
	dh := handlers.NewDrop(store, mh, svc, nil, nil, "")
	r := chi.NewRouter()
	r.Post("/api/files/share", shareH.HandleCreate)
	r.Get("/d/{token}", dh.Page)
	r.Post("/d/{token}", dh.Upload)
	return r, svc, store, st, root
}

func mkdirNode(t *testing.T, store db.Store, st *model.Storage, root, rel string) *model.Node {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
	clean := "/" + strings.Trim(rel, "/")
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID,
		Name:      filepath.Base(rel),
		Path:      clean,
		PathHash:  mutTestPathHash(st.ID, clean),
		Type:      model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	return n
}

type fpart struct{ name, content string }

func doDropUpload(t *testing.T, r http.Handler, url, pin string, fields map[string]string, files []fpart) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if pin != "" {
		_ = mw.WriteField("pin", pin)
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for _, f := range files {
		part, err := mw.CreateFormFile("file[]", f.name)
		require.NoError(t, err)
		_, _ = io.WriteString(part, f.content)
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest("POST", url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func doGet(t *testing.T, r http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// findUnder walks base for a file named name and returns its full path ("" if
// none). Used to locate uploads inside the timestamped submission subfolder.
func findUnder(root, base, name string) string {
	var found string
	_ = filepath.Walk(filepath.Join(root, base), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == name {
			found = p
		}
		return nil
	})
	return found
}

// TestDrop_BlindUpload is the core happy path + the blind-drop guarantee: the
// public page never reveals existing folder contents, and a dropped file lands
// in a per-submission subfolder with the optional note beside it.
func TestDrop_BlindUpload(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")

	// Plant a pre-existing secret file to prove the drop page never lists it.
	require.NoError(t, os.WriteFile(filepath.Join(root, "inbox", "SECRET.txt"), []byte("top secret"), 0o644))

	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)

	// GET page renders the uploader, NOT the folder listing.
	page := doGet(t, r, "/d/"+sh.Token)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), "Dosya gönder")
	assert.NotContains(t, page.Body.String(), "SECRET.txt", "blind drop: existing contents must never leak")

	// POST a file + a note.
	rec := doDropUpload(t, r, "/d/"+sh.Token, "", map[string]string{"uploader_name": "Ahmet", "note": "selam"},
		[]fpart{{"hello.txt", "hello world"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, true, out["ok"])
	assert.Equal(t, float64(1), out["count"])

	// File landed under a submission subfolder (named with the uploader).
	got := findUnder(root, "inbox", "hello.txt")
	require.NotEmpty(t, got, "uploaded file should exist under a submission subfolder")
	data, _ := os.ReadFile(got)
	assert.Equal(t, "hello world", string(data))
	assert.Contains(t, got, "Ahmet")
	assert.NotEmpty(t, findUnder(root, "inbox", "NOT.txt"), "note should be persisted next to the files")

	// Upload counter bumped.
	fresh, err := store.GetShareByToken(context.Background(), sh.Token)
	require.NoError(t, err)
	assert.Equal(t, 1, fresh.UploadCount)
}

// TestDrop_DownloadTokenRejected: a normal download share token must not work
// as a drop endpoint (and vice-versa the /d page rejects it).
func TestDrop_DownloadTokenRejected(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")

	dl, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID}) // kind defaults to download
	require.NoError(t, err)

	page := doGet(t, r, "/d/"+dl.Token)
	assert.Equal(t, http.StatusNotFound, page.Code, "download token on /d must 404")

	rec := doDropUpload(t, r, "/d/"+dl.Token, "", nil, []fpart{{"x.txt", "x"}})
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_a_drop_link")
}

// TestDrop_LimitTooManyFiles: the max_files cap rejects an over-sized batch.
func TestDrop_LimitTooManyFiles(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")

	ds := `{"max_files":1}`
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &ds})
	require.NoError(t, err)

	rec := doDropUpload(t, r, "/d/"+sh.Token, "", nil, []fpart{{"a.txt", "a"}, {"b.txt", "b"}})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "too_many_files")
}

// TestDrop_ExtNotAllowed: the allowlist rejects a disallowed extension.
func TestDrop_ExtNotAllowed(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")

	ds := `{"allowed_ext":["pdf","jpg"]}`
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop, DropSettings: &ds})
	require.NoError(t, err)

	rec := doDropUpload(t, r, "/d/"+sh.Token, "", nil, []fpart{{"evil.exe", "MZ"}})
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "ext_not_allowed")
}

// TestDrop_CreateOnFileRejected: minting a drop link requires a folder target.
func TestDrop_CreateOnFileRejected(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)

	require.NoError(t, os.WriteFile(filepath.Join(root, "doc.txt"), []byte("x"), 0o644))
	_, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: "doc.txt", Path: "/doc.txt",
		PathHash: mutTestPathHash(st.ID, "/doc.txt"), Type: model.NodeTypeFile, Size: 1,
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]any{"path": "main://doc.txt", "kind": "drop"})
	req := httptest.NewRequest("POST", "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "folder")
}

// TestDrop_CreateOnFolderOK: minting a drop link on a folder returns a /d/ URL.
func TestDrop_CreateOnFolderOK(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	mkdirNode(t, store, st, root, "inbox")

	body, _ := json.Marshal(map[string]any{"path": "main://inbox", "kind": "drop"})
	req := httptest.NewRequest("POST", "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "drop", out["kind"])
	assert.Contains(t, out["url"], "/d/")
}

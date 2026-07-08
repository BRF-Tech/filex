package handlers_test

// Handler-level tests for the ShareX upload endpoint. They drive
// ShareX.Upload directly (httptest, no router) against a real local-FS
// driver + in-memory store, then resolve the minted link through the actual
// Share.HandleDownload to prove the capture is stored, indexed, publicly
// shareable, and inline-viewable — the full ShareX round trip.

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

const shareXPublicURL = "http://test.local"

// newShareXFixture wires a ShareX upload handler and a matching Share handler
// over the same local-FS storage ("main") so a minted link can be resolved
// end-to-end. Returns both handlers plus the storage root on disk.
func newShareXFixture(t *testing.T) (*handlers.ShareX, *handlers.Share, string) {
	t.Helper()
	_, store, drv, _, root := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }
	shareSvc := share.NewService(store)
	sx := handlers.NewShareX(store, resolver, shareSvc, shareXPublicURL)
	sh := handlers.NewShare(shareSvc, store, resolver, shareXPublicURL, sharezip.New(t.TempDir()))
	return sx, sh, root
}

// shareXUpload POSTs a multipart capture to ShareX.Upload and returns the raw
// recorder. folder is omitted from the form when empty.
func shareXUpload(t *testing.T, h *handlers.ShareX, filename string, content []byte, folder string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if folder != "" {
		require.NoError(t, mw.WriteField("folder", folder))
	}
	part, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/sharex/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	return rec
}

// shareXURL asserts a 200 + decodes the {"url":…} body.
func shareXURL(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["url"], "response must carry a url")
	return resp["url"]
}

// shareXToken validates the returned link's shape and extracts the token:
//
//	http://test.local/s/<token>?inline=1
func shareXToken(t *testing.T, url string) string {
	t.Helper()
	const pre = shareXPublicURL + "/s/"
	require.True(t, strings.HasPrefix(url, pre), "url must start with %s, got %s", pre, url)
	rest := strings.TrimPrefix(url, pre)
	require.True(t, strings.HasSuffix(rest, "?inline=1"), "url must end with ?inline=1, got %s", url)
	return strings.TrimSuffix(rest, "?inline=1")
}

// shareXDownload resolves the public /s/<token> link through the real Share
// handler (inline mode) with a chi route context carrying the token param.
func shareXDownload(t *testing.T, sh *handlers.Share, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/s/"+token+"?inline=1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	sh.HandleDownload(rec, req)
	return rec
}

// A default upload (no folder) lands under sharex/, mints a public link, and
// that link serves the original bytes inline.
func TestShareX_Upload_DefaultFolderInlineShare(t *testing.T) {
	sx, sh, root := newShareXFixture(t)
	content := []byte("hello sharex")

	token := shareXToken(t, shareXURL(t, shareXUpload(t, sx, "screenshot.png", content, "")))

	// Stored under the default sharex/ folder with a random-prefixed name.
	matches, _ := filepath.Glob(filepath.Join(root, "sharex", "*-screenshot.png"))
	require.Len(t, matches, 1, "exactly one capture stored under sharex/")
	got, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	assert.Equal(t, content, got)

	// The minted link resolves inline with the original bytes.
	dl := shareXDownload(t, sh, token)
	require.Equal(t, http.StatusOK, dl.Code, "download body: %s", dl.Body.String())
	assert.Contains(t, dl.Header().Get("Content-Disposition"), "inline",
		"image link must render inline, not force a download")
	assert.Equal(t, content, dl.Body.Bytes())
}

// A caller-supplied nested folder is created (and indexed) before the file is
// written, and the resulting link resolves.
func TestShareX_Upload_CustomNestedFolder(t *testing.T) {
	sx, sh, root := newShareXFixture(t)
	content := []byte("a note captured by sharex text upload")

	token := shareXToken(t, shareXURL(t, shareXUpload(t, sx, "capture.txt", content, "screens/2026")))

	matches, _ := filepath.Glob(filepath.Join(root, "screens", "2026", "*-capture.txt"))
	require.Len(t, matches, 1, "capture stored under the nested folder")

	dl := shareXDownload(t, sh, token)
	require.Equal(t, http.StatusOK, dl.Code, "download body: %s", dl.Body.String())
	assert.Equal(t, content, dl.Body.Bytes())
}

// A request with no `file` part is rejected with 400.
func TestShareX_Upload_MissingFile(t *testing.T) {
	sx, _, _ := newShareXFixture(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("folder", "sharex"))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", "/api/sharex/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	sx.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

// Traversal segments in `folder` are stripped rather than escaping the storage.
func TestShareX_Upload_FolderTraversalStripped(t *testing.T) {
	sx, _, root := newShareXFixture(t)

	rec := shareXUpload(t, sx, "x.bin", []byte("z"), "../../etc")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	matches, _ := filepath.Glob(filepath.Join(root, "etc", "*-x.bin"))
	require.Len(t, matches, 1, "traversal stripped → lands under root/etc, not above root")
}

// Two same-named captures produce distinct files AND distinct share tokens
// (the random filename prefix prevents one upload from repointing another's link).
func TestShareX_Upload_UniquePerCapture(t *testing.T) {
	sx, _, root := newShareXFixture(t)

	tok1 := shareXToken(t, shareXURL(t, shareXUpload(t, sx, "a.png", []byte("one"), "")))
	tok2 := shareXToken(t, shareXURL(t, shareXUpload(t, sx, "a.png", []byte("two"), "")))

	assert.NotEqual(t, tok1, tok2, "each capture mints its own token")
	matches, _ := filepath.Glob(filepath.Join(root, "sharex", "*-a.png"))
	assert.Len(t, matches, 2, "same-named captures must not collide")
}

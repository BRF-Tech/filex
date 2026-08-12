package handlers_test

/* Two things the public share surface was doing the slow way.

   1. A gallery tile fetched with ?thumb=1 streamed the ORIGINAL file. A folder
      of photos therefore shipped its full weight to paint one screen, and the
      page crawled. It now serves the same cached thumbnail the app uses.

   2. A folder share's ZIP was built when somebody clicked download (or by the
      five-minute warmer, whichever came first). The person who just created the
      link is usually the next one to open it, so the wait landed on them. The
      build now starts when the link is created. */

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// mkfileNode indexes a file that already exists on the driver, so the
// thumbnail pipeline (which is keyed by node id) has something to render.
func mkfileNode(t *testing.T, store db.Store, st *model.Storage, rel string, size int64) *model.Node {
	t.Helper()
	clean := "/" + strings.Trim(rel, "/")
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID,
		Name:      filepath.Base(rel),
		Path:      clean,
		PathHash:  mutTestPathHash(st.ID, clean),
		Type:      model.NodeTypeFile,
		Size:      size,
	})
	require.NoError(t, err)
	return n
}

// TestShareBrowse_ThumbServesCachedThumbnail is the measurement that matters:
// the bytes a gallery tile receives must NOT be the original file.
func TestShareBrowse_ThumbServesCachedThumbnail(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "album"))
	require.NoError(t, drv.Write(ctx, "album/a.gif", strings.NewReader(browseTestGif), int64(len(browseTestGif))))

	dir, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "album", Path: "album",
		PathHash: mutTestPathHash(st.ID, "album"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	mkfileNode(t, store, st, "album/a.gif", int64(len(browseTestGif)))

	svc := share.NewService(store)
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: dir.ID})
	require.NoError(t, err)

	// ── before: no pipeline wired, so the tile gets the original ────────
	// This is the behaviour being replaced, asserted here so the test would
	// still fail if the fix were reverted AND the assertion below relaxed.
	bare := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	rec := browseGetFile(bare, sh.Token, "a.gif", "?thumb=1")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, browseTestGif, rec.Body.String(),
		"with no thumbnail pipeline the endpoint still streams the original")

	// ── after: pipeline wired → a rendered JPEG, not the source bytes ───
	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true})
	pipe.AttachStorage(st.ID, drv)
	h := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	h.AttachThumbs(pipe)

	rec = browseGetFile(h, sh.Token, "a.gif", "?thumb=1")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, "image/jpeg", rec.Header().Get("Content-Type"))
	require.NotEqual(t, browseTestGif, rec.Body.String(), "the tile must not receive the original file")
	require.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte{0xFF, 0xD8}), "expected JPEG bytes")

	// Still not a download — a wall of tiles must not exhaust max_downloads.
	got, err := store.GetShareByToken(ctx, sh.Token)
	require.NoError(t, err)
	require.Equal(t, 0, got.DownloadCount)

	// Second fetch comes from the cache; same bytes, no re-render.
	again := browseGetFile(h, sh.Token, "a.gif", "?thumb=1")
	require.Equal(t, rec.Body.String(), again.Body.String())
}

// A file the pipeline cannot render (or that was never indexed) must still be
// served rather than 404 — the tile falls back to the original exactly as
// before. Here the file has no node row at all.
func TestShareBrowse_ThumbFallsBackWhenUnindexed(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "album"))
	require.NoError(t, drv.Write(ctx, "album/b.gif", strings.NewReader(browseTestGif), int64(len(browseTestGif))))
	dir, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "album", Path: "album",
		PathHash: mutTestPathHash(st.ID, "album"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	svc := share.NewService(store)
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: dir.ID})
	require.NoError(t, err)

	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true})
	pipe.AttachStorage(st.ID, drv)
	h := handlers.NewShare(svc, store, resolver, "", sharezip.New(""))
	h.AttachThumbs(pipe)

	rec := browseGetFile(h, sh.Token, "b.gif", "?thumb=1")
	require.Equal(t, 200, rec.Code, "an unindexed file must not break the gallery")
	require.Equal(t, browseTestGif, rec.Body.String())
}

// TestShare_FolderZipWarmsOnCreate: creating a folder share starts the ZIP
// build immediately, so the first download is a cache hit.
func TestShare_FolderZipWarmsOnCreate(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, root := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	dir := mkdirNode(t, store, st, root, "album")
	require.NoError(t, drv.Write(ctx, "album/one.txt", strings.NewReader("hello"), 5))

	zipDir := t.TempDir()
	h := handlers.NewShare(share.NewService(store), store, resolver, "https://fm.example", sharezip.New(zipDir))
	r := chi.NewRouter()
	r.Post("/api/files/share", h.HandleCreate)

	body, _ := json.Marshal(map[string]any{"node_id": dir.ID})
	req := httptest.NewRequest("POST", "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// ⚠ The warm is deliberately asynchronous — the response must not wait on a
	// multi-gigabyte zip — so poll rather than assert immediately.
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(zipDir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".zip") {
				info, err := e.Info()
				return err == nil && info.Size() > 0
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "creating a folder share must start building its ZIP")
}

// A FILE share has no ZIP to build; creating one must not leave anything in the
// cache (and must not panic on the directory-only code path).
func TestShare_FileShareDoesNotWarmZip(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Write(ctx, "solo.txt", strings.NewReader("hello"), 5))
	file := mkfileNode(t, store, st, "solo.txt", 5)

	zipDir := t.TempDir()
	h := handlers.NewShare(share.NewService(store), store, resolver, "https://fm.example", sharezip.New(zipDir))
	r := chi.NewRouter()
	r.Post("/api/files/share", h.HandleCreate)

	body, _ := json.Marshal(map[string]any{"node_id": file.ID})
	req := httptest.NewRequest("POST", "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	time.Sleep(300 * time.Millisecond)
	entries, err := os.ReadDir(zipDir)
	require.NoError(t, err)
	require.Empty(t, entries, "a file share has no folder to zip")
}

// TestShare_FolderThumbsWarmOnCreate: creating a folder share also renders the
// gallery's tiles, so the FIRST visit is fast too — not just the second.
func TestShare_FolderThumbsWarmOnCreate(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, root := newMutateFixture(t)
	resolver := func(int64) (storage.Driver, error) { return drv, nil }

	dir := mkdirNode(t, store, st, root, "album")
	require.NoError(t, drv.Write(ctx, "album/pic.gif", strings.NewReader(browseTestGif), int64(len(browseTestGif))))
	pic := mkfileNode(t, store, st, "album/pic.gif", int64(len(browseTestGif)))

	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true})
	pipe.AttachStorage(st.ID, drv)
	h := handlers.NewShare(share.NewService(store), store, resolver, "https://fm.example", sharezip.New(t.TempDir()))
	h.AttachThumbs(pipe)

	r := chi.NewRouter()
	r.Post("/api/files/share", h.HandleCreate)
	body, _ := json.Marshal(map[string]any{"node_id": dir.ID})
	req := httptest.NewRequest("POST", "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Eventually(t, func() bool {
		th, err := store.GetThumbnail(context.Background(), pic.ID)
		return err == nil && th != nil && th.State == "ready"
	}, 10*time.Second, 50*time.Millisecond,
		"sharing a folder must render its gallery tiles, not leave them for the first visitor")
}

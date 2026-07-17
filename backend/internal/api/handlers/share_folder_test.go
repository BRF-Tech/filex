package handlers_test

// Regression test for folder shares: sharing a directory and hitting the
// public download link must stream a ZIP of every file under it ("download
// all"). The bug: HandleDownload called drv.Read() on the directory path,
// which 500'd ("read error") because you can't open a dir as a byte stream.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func TestShare_DownloadFolder_StreamsZip(t *testing.T) {
	ctx := context.Background()
	mh, store, drv, st, _ := newMutateFixture(t)
	_ = mh
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	// Build a small tree on disk: docs/a.txt, docs/sub/b.txt
	require.NoError(t, drv.Mkdir(ctx, "docs"))
	require.NoError(t, drv.Mkdir(ctx, "docs/sub"))
	require.NoError(t, drv.Write(ctx, "docs/a.txt", strings.NewReader("alpha"), 5))
	require.NoError(t, drv.Write(ctx, "docs/sub/b.txt", strings.NewReader("beta"), 4))

	// DB node for the shared folder (HandleDownload resolves the share to
	// this node and keys off node.Type == dir).
	docs, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "docs", Path: "docs",
		PathHash: mutTestPathHash(st.ID, "docs"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	// Create a (no-PIN) share for the folder.
	shareSvc := share.NewService(store)
	sh, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: docs.ID})
	require.NoError(t, err)

	sharH := handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(t.TempDir()))

	// ?zip=wait blocks until the (cached) zip is built, then serves it — the
	// default GET now shows a progress page for a cold cache.
	req := httptest.NewRequest("GET", "/s/"+sh.Token+"?zip=wait", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", sh.Token)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	sharH.HandleDownload(rec, req)

	require.Equal(t, 200, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "zip")

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err, "response must be a valid zip")

	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		_ = rc.Close()
		got[f.Name] = b.String()
	}
	require.Equal(t, "alpha", got["a.txt"], "zip must contain a.txt with its bytes")
	require.Equal(t, "beta", got["sub/b.txt"], "zip must contain nested sub/b.txt")
}

// A folder share's ZIP is cached on disk (keyed by content signature): repeat
// downloads reuse the cache file, and changing the folder invalidates it.
func TestShare_DownloadFolder_CachesAndInvalidates(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "docs"))
	require.NoError(t, drv.Write(ctx, "docs/a.txt", strings.NewReader("alpha"), 5))

	docs, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "docs", Path: "docs",
		PathHash: mutTestPathHash(st.ID, "docs"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	shareSvc := share.NewService(store)
	sh, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: docs.ID})
	require.NoError(t, err)

	cacheDir := t.TempDir()
	sharH := handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(cacheDir))

	download := func() []byte {
		// ?zip=wait yields the finished zip whether the cache is cold (blocks
		// until built) or warm (served straight from the top-level cache check).
		req := httptest.NewRequest("GET", "/s/"+sh.Token+"?zip=wait", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("token", sh.Token)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		sharH.HandleDownload(rec, req)
		require.Equal(t, 200, rec.Code, "body: %s", rec.Body.String())
		require.NotEmpty(t, rec.Header().Get("Content-Length"), "cached zip must advertise Content-Length")
		return rec.Body.Bytes()
	}
	entries := func(b []byte) map[string]string {
		zr, zerr := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		require.NoError(t, zerr)
		out := map[string]string{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rc)
			_ = rc.Close()
			out[f.Name] = buf.String()
		}
		return out
	}
	glob := filepath.Join(cacheDir, fmt.Sprintf("%d-*.zip", docs.ID))

	// First download → cache miss, writes one cache file.
	require.Equal(t, "alpha", entries(download())["a.txt"])
	m1, _ := filepath.Glob(glob)
	require.Len(t, m1, 1, "one cached zip after first download")
	fi1, _ := os.Stat(m1[0])

	// Second download → cache hit, same file, not regenerated.
	_ = download()
	m2, _ := filepath.Glob(glob)
	require.Len(t, m2, 1)
	fi2, _ := os.Stat(m2[0])
	require.Equal(t, fi1.ModTime(), fi2.ModTime(), "cache hit must not regenerate the zip")

	// Add a file → signature changes → cache miss → new content, old zip pruned.
	require.NoError(t, drv.Write(ctx, "docs/b.txt", strings.NewReader("beta"), 4))
	third := entries(download())
	require.Equal(t, "alpha", third["a.txt"])
	require.Equal(t, "beta", third["b.txt"], "new file must appear after invalidation")
	m3, _ := filepath.Glob(glob)
	require.Len(t, m3, 1, "old cached zip pruned, only the new signature remains")
	require.NotEqual(t, m1[0], m3[0], "new signature → new cache filename")
}

// A cold-cache folder download shows a "preparing" progress page, and
// ?zip=status reports build progress as JSON, eventually ready.
func TestShare_DownloadFolder_WaitPageAndStatus(t *testing.T) {
	ctx := context.Background()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return drv, nil }

	require.NoError(t, drv.Mkdir(ctx, "docs"))
	require.NoError(t, drv.Write(ctx, "docs/a.txt", strings.NewReader("alpha"), 5))

	docs, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "docs", Path: "docs",
		PathHash: mutTestPathHash(st.ID, "docs"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)

	shareSvc := share.NewService(store)
	sh, err := shareSvc.Create(ctx, share.CreateOpts{NodeID: docs.ID})
	require.NoError(t, err)

	sharH := handlers.NewShare(shareSvc, store, resolver, "", sharezip.New(t.TempDir()))

	get := func(q string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/s/"+sh.Token+q, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("token", sh.Token)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		sharH.HandleDownload(rec, req)
		return rec
	}

	/* wiring:d2 — the default GET now renders the folder BROWSE page (list /
	   gallery); the ZIP progress page moved behind ?zip=… (the page's
	   "Tümünü indir" button). */
	rec := get("")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "a.txt", "browse page must list the folder contents")

	// ?zip=1 (the download-all button) on a cold cache → HTML progress page.
	rec = get("?zip=1")
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "hazırlanıyor")

	// ?zip=status → JSON {ready, percent}; a 1-file folder becomes ready fast.
	ready := false
	for i := 0; i < 100; i++ {
		rec := get("?zip=status")
		require.Equal(t, 200, rec.Code)
		require.Contains(t, rec.Header().Get("Content-Type"), "json")
		var s struct {
			Ready   bool `json:"ready"`
			Percent int  `json:"percent"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
		require.GreaterOrEqual(t, s.Percent, 0)
		require.LessOrEqual(t, s.Percent, 100)
		if s.Ready {
			require.Equal(t, 100, s.Percent)
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, ready, "status must eventually report ready")
}

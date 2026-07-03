package handlers_test

// Regression test for folder shares: sharing a directory and hitting the
// public download link must stream a ZIP of every file under it ("download
// all"). The bug: HandleDownload called drv.Read() on the directory path,
// which 500'd ("read error") because you can't open a dir as a byte stream.

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
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

	sharH := handlers.NewShare(shareSvc, store, resolver, "")

	req := httptest.NewRequest("GET", "/s/"+sh.Token, nil)
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

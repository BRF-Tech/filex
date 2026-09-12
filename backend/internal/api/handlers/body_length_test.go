package handlers

// A declared length that the body does not match breaks the download in the
// recipient's program, which reports it as its own failure and names nothing.
// The catalogue row is the wrong source for that number whenever the last sync
// is older than the file.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

func lengthFixture(t *testing.T, body string) (*filebody.Source, *local.Driver) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.docx"), []byte(body), 0o644))
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": dir}))
	src, err := filebody.New(nil, nil).Resolve(context.Background(), drv, 1, "doc.docx", nil)
	require.NoError(t, err)
	return src, drv
}

func TestDeclareBodyLength(t *testing.T) {
	ctx := context.Background()
	body := "the twelve bytes of this document are not what the row says"

	t.Run("a stale catalogue row does not become the declared length", func(t *testing.T) {
		src, _ := lengthFixture(t, body)
		w := httptest.NewRecorder()
		// The row was written when the file was much larger — a sync that has
		// not run since, or a catalogue half-repaired after a rename.
		declareBodyLength(ctx, w, src, &model.Node{ID: 7, StorageID: 1, Size: 900000})
		require.Equal(t, "59", w.Header().Get("Content-Length"),
			"the length came from the catalogue instead of the storage")
		require.Len(t, body, 59)
	})

	t.Run("an agreeing row is still answered from the storage", func(t *testing.T) {
		src, _ := lengthFixture(t, body)
		w := httptest.NewRecorder()
		declareBodyLength(ctx, w, src, &model.Node{ID: 7, StorageID: 1, Size: int64(len(body))})
		require.Equal(t, "59", w.Header().Get("Content-Length"))
	})

	t.Run("an object the storage cannot describe is served without a length", func(t *testing.T) {
		src, err := filebody.New(nil, nil).Resolve(ctx, mustLocal(t), 1, "absent.docx", nil)
		require.NoError(t, err)
		w := httptest.NewRecorder()
		declareBodyLength(ctx, w, src, &model.Node{ID: 7, StorageID: 1, Size: 1234})
		require.Empty(t, w.Header().Get("Content-Length"),
			"an unverified length is worse than none: the client cannot tell a short read from a finished one")
	})

	t.Run("no row at all is fine", func(t *testing.T) {
		src, _ := lengthFixture(t, body)
		w := httptest.NewRecorder()
		declareBodyLength(ctx, w, src, nil)
		require.Equal(t, "59", w.Header().Get("Content-Length"))
	})
}

func mustLocal(t *testing.T) *local.Driver {
	t.Helper()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": t.TempDir()}))
	return drv
}

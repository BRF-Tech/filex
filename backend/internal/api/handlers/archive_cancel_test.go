package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestArchiveProviderErrorsDoNotExposeHostPaths(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeArchiveProviderError(recorder, errors.Join(
		archivecli.ErrUnavailable,
		errors.New(`configured 7-Zip binary "/srv/filex/private/tools/7zz" is not executable`),
	))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "/srv/filex/private/tools/7zz")
	assert.JSONEq(t, `{"code":"PROVIDER_UNAVAILABLE","error":"archive provider is unavailable"}`, recorder.Body.String())
}

func TestArchiveExtractionCancellationKeepsCompletedFiles(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + filepath.ToSlash(root) + `"}`),
	})
	require.NoError(t, err)

	archivePath := filepath.Join(t.TempDir(), "source.zip")
	f, err := os.Create(archivePath)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for _, name := range []string{"first.txt", "second.txt"} {
		entry, createErr := zw.Create(name)
		require.NoError(t, createErr)
		_, writeErr := entry.Write([]byte(name))
		require.NoError(t, writeErr)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())

	h := NewArchive(store, func(id int64) (storage.Driver, error) { return drv, nil })
	ctx, cancel := context.WithCancel(context.Background())
	result, err := h.extractArchive(ctx, archiveRequest{
		StorageID: st.ID, Path: "source.zip", DestDir: "restored",
	}, drv, drv, archivePath, false, func(done int) {
		if done == 1 {
			cancel()
		}
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, result["count"])

	first, err := os.ReadFile(filepath.Join(root, "restored", "first.txt"))
	require.NoError(t, err)
	assert.Equal(t, "first.txt", string(first))
	_, err = os.Stat(filepath.Join(root, "restored", "second.txt"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "a file after the cancellation point must not be written")
}

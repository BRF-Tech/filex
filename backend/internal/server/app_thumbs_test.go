package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/testutil/wasmfixture"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// The whole chain, as internal/server wires it: a real app (the echo fixture,
// installed with a `thumbnails` block for .jar), the Default apps rules, the
// thumbnail pipeline. What a listing, an upload and a repair go through.

const echoDir = "../wasmplugin/testdata/echo"

func TestAppThumbs_TheAppDrawsWhatTheAdministratorLeftItOn(t *testing.T) {
	ctx := context.Background()
	wasmfixture.Require(t, filepath.Join(echoDir, "echo.wasm"))
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{Name: "arsiv", Driver: "local", MountPath: "/arsiv", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + filepath.ToSlash(root) + `"}`)})
	require.NoError(t, err)

	reg, err := wasmplugin.New(wasmplugin.Options{
		Store: store, Dir: filepath.Join(t.TempDir(), "app-plugins"), SecretKey: "0123456789abcdef0123456789abcdef",
		StorageResolver: func(int64) (storage.Driver, error) { return drv, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })

	raw, err := os.ReadFile(filepath.Join(echoDir, "manifest.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["thumbnails"] = map[string]any{"applies": map[string]any{"ext": []string{"jar"}}}
	raw, _ = json.Marshal(m)
	parsed, err := wasmplugin.ParseManifest(raw)
	require.NoError(t, err)
	granted := []string{}
	for _, p := range parsed.Perms {
		granted = append(granted, string(p))
	}
	wasm, err := os.ReadFile(filepath.Join(echoDir, "echo.wasm"))
	require.NoError(t, err)
	_, _, err = reg.Install(ctx, &wasmplugin.InstallInput{Manifest: raw, Wasm: bytes.NewReader(wasm), Source: "upload", Granted: granted, Lang: "en"})
	require.NoError(t, err)

	svc := assoc.New(store)
	svc.SetSource(reg)
	svc.SetBuiltinThumb(thumb.BuiltinDraws)
	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true, SVG: true})
	pipe.AttachStorage(st.ID, drv)
	pipe.AttachApps(&appThumbs{assoc: svc, reg: reg})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("META-INF/MANIFEST.MF")
	_, _ = w.Write([]byte("Manifest-Version: 1.0\n"))
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(root, "lib.jar"), buf.Bytes(), 0o644))
	n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "lib.jar", Path: "/lib.jar",
		PathHash: pathkey.Hash(st.ID, "/lib.jar"), Type: model.NodeTypeFile, Size: int64(buf.Len()), Mime: "application/zip"})
	require.NoError(t, err)

	// A listing draws a .jar now: an app draws that kind.
	assert.Equal(t, thumb.Render, pipe.Assess(n, nil, time.Now()))
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "ready", row.State)
	assert.Equal(t, "app:echo@0.0.1", row.Generator)
	_, err = os.Stat(pipe.CachePath(n.ID))
	require.NoError(t, err, "filex wrote its JPEG of the app's picture")

	// The administrator switches the app off for .jar: the picture is stale,
	// and drawing it again says nobody may.
	_, err = svc.Put(ctx, assoc.CapThumbnail, "jar", assoc.Rule{Off: []string{"app:echo"}}, nil)
	require.NoError(t, err)
	old := time.Now().Add(-time.Hour)
	row.AttemptedAt = &old
	assert.Equal(t, thumb.Render, pipe.Assess(n, row, time.Now()))
	require.ErrorIs(t, pipe.GenerateThumb(ctx, n), thumb.ErrSkipped)
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "skipped", row.State)
	assert.Equal(t, thumb.SkipNoHandler, row.Error)

	// Back on: drawn again.
	_, err = svc.Reset(ctx, assoc.CapThumbnail, "jar")
	require.NoError(t, err)
	row.AttemptedAt = &old
	require.NoError(t, store.UpsertThumbnail(ctx, row))
	assert.Equal(t, thumb.Render, pipe.Assess(n, row, time.Now()))
	require.NoError(t, pipe.GenerateThumb(ctx, n))
	row, err = store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "app:echo@0.0.1", row.Generator)

	gens, err := store.ThumbnailGenerators(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), gens["app:echo@0.0.1"])
}

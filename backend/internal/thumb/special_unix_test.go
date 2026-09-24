//go:build unix

package thumb_test

// Issue #38 from the thumbnail side: a catalogued file that is a named pipe on
// disk (a file replaced by one between two scans) must fail its thumbnail at
// once, not hold a pipeline worker on an open that never returns.

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

func TestThumbnailOfANamedPipeFailsAtOnce(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "foto.png"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"path": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "yerel", Driver: "local", MountPath: "/yerel", Enabled: true,
		ConfigJSON: []byte(`{"path":"` + root + `"}`),
	})
	require.NoError(t, err)
	pipe := thumb.New(store, t.TempDir(), thumb.Capabilities{Image: true})
	pipe.AttachStorage(st.ID, drv)
	n := fileNode(t, store, st, "/foto.png", "foto.png", 2048)

	done := make(chan error, 1)
	go func() { done <- pipe.GenerateThumb(ctx, n) }()
	select {
	case err := <-done:
		require.Error(t, err, "a pipe produced a thumbnail")
	case <-time.After(5 * time.Second):
		t.Fatal("GenerateThumb is waiting on a named pipe (issue #38)")
	}
	row, err := store.GetThumbnail(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "failed", row.State)
	_, statErr := os.Stat(pipe.CachePath(n.ID))
	require.True(t, os.IsNotExist(statErr), "no thumbnail file may be written for a pipe")
}

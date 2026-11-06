//go:build linux

package trash_test

// Issue #133 on a real filesystem boundary: no stand-in, the kernel's own
// refusal. A mount needs privileges a test run does not have, so the second
// filesystem is reached the way follow_symlinks reaches one: a link in the root
// to /dev/shm, which os.Rename crosses exactly like a mount.

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/trash"
)

func acrossFilesystems(t *testing.T) (storage.Driver, string, string) {
	t.Helper()
	root := t.TempDir()
	other, err := os.MkdirTemp("/dev/shm", "fx133")
	if err != nil {
		t.Skipf("no second filesystem here: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	var a, b syscall.Stat_t
	require.NoError(t, syscall.Stat(root, &a))
	require.NoError(t, syscall.Stat(other, &b))
	if a.Dev == b.Dev {
		t.Skip("the temp dir and /dev/shm are one filesystem here")
	}
	require.NoError(t, os.Symlink(other, filepath.Join(root, "gdrive")))
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": root, "follow_symlinks": true}))
	return drv, root, other
}

func TestPut_AFileOnAnotherFilesystemGoesToTheTrashAndComesBack(t *testing.T) {
	ctx := context.Background()
	drv, root, other := acrossFilesystems(t)
	src := filepath.Join(other, "Canada_IMM0276.pdf")
	require.NoError(t, os.WriteFile(src, []byte("the only copy"), 0o644))
	when := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	require.NoError(t, os.Chtimes(src, when, when))

	out, err := trash.Put(ctx, drv, "gdrive/Canada_IMM0276.pdf")

	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.NoFileExists(t, src)
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Key)))
	require.NoError(t, err)
	require.Equal(t, "the only copy", string(b))

	require.NoError(t, trash.TakeBack(ctx, drv, out.Key, "gdrive/Canada_IMM0276.pdf"))

	b, err = os.ReadFile(src)
	require.NoError(t, err)
	require.Equal(t, "the only copy", string(b))
	info, err := os.Stat(src)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(when), "restored with the time it left with: %v", info.ModTime())
}

func TestPut_AFolderOnAnotherFilesystemGoesToTheTrashWhole(t *testing.T) {
	ctx := context.Background()
	drv, root, other := acrossFilesystems(t)
	require.NoError(t, os.MkdirAll(filepath.Join(other, "Juan", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "Juan", "a.txt"), []byte("A"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(other, "Juan", "deep", "b.txt"), []byte("B"), 0o644))

	out, err := trash.Put(ctx, drv, "gdrive/Juan")

	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.NoDirExists(t, filepath.Join(other, "Juan"))
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Key), "deep", "b.txt"))
	require.NoError(t, err)
	require.Equal(t, "B", string(b))
}

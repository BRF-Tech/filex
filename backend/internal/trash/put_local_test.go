package trash_test

// Deleting something that is already gone must not fail the operation.
//
// This lives in its own file, and against the REAL `local` driver, on purpose.
// put_test.go's fixture is an in-memory object store — a flat key→bytes map
// with no directories — and its Move returns storage.ErrNotFound for a missing
// key, exactly as the Driver contract requires. The `local` driver did not: it
// returned the raw *fs.PathError from os.Rename.
//
// Put asks `errors.Is(err, storage.ErrNotFound)` to tell "already gone"
// (report Missing, succeed) from "the rename genuinely failed" (fail the op).
// With the raw error it could not, so a delete whose source had already moved
// ended the async op as `failed`. Every in-memory test passed throughout.
//
// Measured 2026-08-15: three different E2E specs failing on different runs,
// all of them async deletes, with
// `rename …: The system cannot find the path specified` in the server log.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/trash"
)

func localDriver(t *testing.T) (storage.Driver, string) {
	t.Helper()
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": root}))
	return drv, root
}

func TestPut_MissingPathIsMissingNotAnError(t *testing.T) {
	ctx := context.Background()
	drv, _ := localDriver(t)

	out, err := trash.Put(ctx, drv, "sub/gone.txt")
	require.NoError(t, err, "deleting an already-absent path must not fail the op")
	require.True(t, out.Missing, "it should be reported as missing")
	require.False(t, out.Trashed, "nothing was preserved, so nothing may claim to be")
}

func TestLocalMove_ReportsErrNotFoundForAMissingSource(t *testing.T) {
	ctx := context.Background()
	drv, _ := localDriver(t)

	mover, ok := drv.(storage.Mover)
	require.True(t, ok)
	err := mover.Move(ctx, "nope.txt", "elsewhere.txt")
	require.ErrorIs(t, err, storage.ErrNotFound,
		"the Driver contract: a missing path is storage.ErrNotFound, not a raw PathError")
}

func TestLocalCopy_ReportsErrNotFoundForAMissingSource(t *testing.T) {
	ctx := context.Background()
	drv, _ := localDriver(t)

	copier, ok := drv.(storage.Copier)
	require.True(t, ok)
	require.ErrorIs(t, copier.Copy(ctx, "nope.txt", "elsewhere.txt"), storage.ErrNotFound)
}

func TestPut_StillTrashesAFileThatIsActuallyThere(t *testing.T) {
	ctx := context.Background()
	drv, root := localDriver(t)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("veri"), 0o644))

	out, err := trash.Put(ctx, drv, "sub/a.txt")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	require.True(t, strings.HasPrefix(out.Key, trash.Prefix+"/"), "key %q", out.Key)

	moved, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Key)))
	require.NoError(t, err, "Trashed=true promised the bytes were preserved")
	require.Equal(t, "veri", string(moved))

	_, err = os.Stat(filepath.Join(root, "sub", "a.txt"))
	require.True(t, os.IsNotExist(err), "the source must be gone")
}

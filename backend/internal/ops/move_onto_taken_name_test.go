package ops_test

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

func readAll(t *testing.T, f *opsFixture, rel string) string {
	t.Helper()
	rc, err := f.drv.Read(context.Background(), rel)
	require.NoError(t, err, "nothing at %s", rel)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

// A move never destroys what already has the name at the destination.
//
// ⚠⚠ Measured 2026-09-14: moving `a.txt` into a folder that already held an
// `a.txt` finished as `ok`, and the file that had been there was gone — not in
// the trash, not anywhere. The worker called the driver's Move straight onto
// the occupied path, and a local disk's rename replaces a file (so does an
// object store's copy-then-delete). A copy and a cross-storage move already
// resolve a free name (`a-copy.txt`) for exactly this reason; the same-storage
// move was the one transfer that did not.
func TestOpsWorker_Move_OntoATakenName_KeepsBoth(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedFile(t, "dest/a.txt", "was-here")
	f.seedFile(t, "a.txt", "incoming")

	op := f.runOp(t, ops.OpMove, []string{"a.txt"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "move op error: %s", op.Error)

	require.Equal(t, "was-here", readAll(t, f, "dest/a.txt"),
		"the file that already had the name was overwritten by the move")
	require.Equal(t, "incoming", readAll(t, f, "dest/a-copy.txt"),
		"the moved file must land beside it under a free name")
}

// Moving an item into the folder it is already in is not a collision with
// itself: it stays where it is, under its own name.
func TestOpsWorker_Move_IntoItsOwnFolder_IsANoOp(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedFile(t, "dest/a.txt", "stay")

	op := f.runOp(t, ops.OpMove, []string{"dest/a.txt"}, "dest/")
	require.Equal(t, ops.StatusOK, op.Status, "move op error: %s", op.Error)
	require.Equal(t, "stay", readAll(t, f, "dest/a.txt"))
	_, err := f.drv.Stat(context.Background(), "dest/a-copy.txt")
	require.Error(t, err, "a move into its own folder renamed the file")
}

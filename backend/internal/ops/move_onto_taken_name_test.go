package ops_test

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
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

// ⚠⚠ When every free name was taken too, the de-collision handed back the
// TAKEN name "to let the driver surface the collision" — and every driver's
// Move replaces an occupied destination. A folder already holding `a-copy.txt`
// through `a-copy-100.txt` had its `a.txt` silently overwritten. It is an
// error now, and nothing moves.
func TestOpsWorker_Move_WithNoFreeNameLeft_MovesNothing(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedFile(t, "dest/a.txt", "was-here")
	f.seedFile(t, "dest/a-copy.txt", "c1")
	for i := 2; i <= 100; i++ {
		f.seedFile(t, fmt.Sprintf("dest/a-copy-%d.txt", i), "cN")
	}
	f.seedFile(t, "a.txt", "incoming")

	op := f.runOp(t, ops.OpMove, []string{"a.txt"}, "dest/")
	require.Equal(t, ops.StatusFailed, op.Status, "a move with no free name must fail, not overwrite")
	require.Equal(t, "was-here", readAll(t, f, "dest/a.txt"))
	require.Equal(t, "incoming", readAll(t, f, "a.txt"), "and the source stays where it was")
}

// A live catalogue row holds its name even when its bytes went missing: the
// driver sees a free name, but moving onto it would make the catalogue drop
// that row and its history.
func TestMoveDest_ACatalogueRowHoldsItsName(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "dest")
	f.seedFile(t, "a.txt", "incoming")
	taken := func(rel string) bool { return strings.Trim(rel, "/") == "dest/a.txt" }
	got, err := ops.MoveDest(context.Background(), f.drv, "a.txt", "dest/a.txt", taken)
	require.NoError(t, err)
	require.Equal(t, "dest/a-copy.txt", got)
}

// caseFolding is a local disk that matches names regardless of case, the way
// Windows and macOS volumes do by default.
type caseFolding struct{ storage.Driver }

func (c caseFolding) Stat(ctx context.Context, p string) (storage.Object, error) {
	dir, base := path.Split("/" + strings.Trim(p, "/"))
	objs, err := c.Driver.List(ctx, dir)
	if err != nil {
		return storage.Object{}, err
	}
	for _, o := range objs {
		if strings.EqualFold(o.Name, base) {
			return o, nil
		}
	}
	return storage.Object{}, storage.ErrNotFound
}

// On a case-insensitive disk Stat(`C.txt`) finds `c.txt` — the item being
// renamed — and a case-only rename by an agent became `C-copy.txt`. Only an
// entry spelled exactly like the new name is another item.
func TestMoveDest_ACaseOnlyRenameIsNotACollision(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "c.txt", "content")
	ci := caseFolding{f.drv}
	got, err := ops.MoveDest(context.Background(), ci, "c.txt", "C.txt")
	require.NoError(t, err)
	require.Equal(t, "C.txt", got)

	// A case-SENSITIVE store holding both is a real collision.
	f.seedFile(t, "C.txt", "another file")
	got, err = ops.MoveDest(context.Background(), f.drv, "c.txt", "C.txt")
	require.NoError(t, err)
	require.Equal(t, "C-copy.txt", got)
}

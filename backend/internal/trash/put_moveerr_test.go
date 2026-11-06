package trash

// What Put does with a move that failed (issue #133). The per-object walk is a
// second chance for an object-store folder and nothing else: it must not put
// its own words in the mouth of a move that failed for a real reason, and it
// must not start over on a folder whose move was already attempted whole.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// diskDrv answers the way a driver with real folders does: a listing of a FILE
// is an error, os.ReadDir's own.
type diskDrv struct {
	*memCore
	refuse  string
	moveErr error
}

func (d diskDrv) List(ctx context.Context, dir string) ([]storage.Object, error) {
	if _, isFile := d.files[dir]; isFile {
		return nil, errors.New("open /srv/files/" + dir + ": not a directory")
	}
	return d.memCore.List(ctx, dir)
}

func (d diskDrv) Move(_ context.Context, s, t string) error {
	if s == d.refuse {
		return d.moveErr
	}
	return d.move(s, t)
}

func (d diskDrv) Delete(_ context.Context, p string) error { return d.del(p) }

func TestPutSaysWhyTheMoveFailed(t *testing.T) {
	refused := errors.New("read-only file system")
	drv := diskDrv{memCore: newCore("doc.pdf"), refuse: "doc.pdf", moveErr: refused}

	_, err := Put(context.Background(), drv, "doc.pdf")

	require.ErrorIs(t, err, refused, "the person sees this, and `not a directory` tells them nothing true")
	require.Equal(t, []string{"doc.pdf"}, drv.keys())
}

// ⚠⚠ The disk fills half way through a folder. The walk would then trash the
// files that still fit, with no row to find them by, and clean what is left of
// the folder away as if it were a marker.
func TestPutStopsAtAFailedMoveAcross(t *testing.T) {
	full := fmt.Errorf("%w: no space left on device", storage.ErrMoveAcross)
	drv := diskDrv{memCore: newCore("folder/a.txt", "folder/b.txt"), refuse: "folder", moveErr: full}

	_, err := Put(context.Background(), drv, "folder")

	require.ErrorIs(t, err, storage.ErrMoveAcross)
	require.Equal(t, []string{"folder/a.txt", "folder/b.txt"}, drv.keys(), "nothing may have moved")
}

func TestTakeBackStopsAtAFailedMoveAcross(t *testing.T) {
	key := Prefix + "/k__folder"
	full := fmt.Errorf("%w: no space left on device", storage.ErrMoveAcross)
	drv := diskDrv{memCore: newCore(key + "/a.txt"), refuse: key, moveErr: full}

	err := TakeBack(context.Background(), drv, key, "folder")

	require.ErrorIs(t, err, storage.ErrMoveAcross)
	require.Equal(t, []string{key + "/a.txt"}, drv.keys(), "nothing may have moved")
}

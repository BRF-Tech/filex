package handlers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// nameTakenError is a rename or move refused because something already holds
// the destination name. It unwraps to os.ErrExist, so mapDriverErr and
// aiStatus answer 409 without learning a new error, and its text names the
// file — never a server path.
type nameTakenError struct{ name string }

func (e *nameTakenError) Error() string { return fmt.Sprintf("%q already exists here", e.name) }
func (e *nameTakenError) Unwrap() error { return os.ErrExist }

// errNameCheckFailed means the destination could not be checked. It is a
// refusal (503), never a pass: "I could not tell whether something is there"
// followed by a move is exactly how the thing that was there gets replaced.
var errNameCheckFailed = errors.New("could not check whether that name is free")

// liveRowTaken is the catalogue half of a move's de-collision (ops.Taken): a
// LIVE row at rel holds the name even when its bytes have gone missing — the
// driver cannot see it, and moving onto it would make the catalogue drop that
// row, its history with it.
func liveRowTaken(ctx context.Context, store livePathLookup, storageID int64) ops.Taken {
	if store == nil {
		return nil
	}
	return func(rel string) bool {
		n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, normalizeDBPath(rel)))
		return err == nil && n != nil
	}
}

// livePathLookup is the one catalogue question destinationTaken asks. db.Store
// satisfies it.
type livePathLookup interface {
	GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
}

// destinationTaken reports whether something already holds dstRel on a
// storage, so that a rename or a move never lands on top of it.
//
// ⚠⚠ Every driver's Move REPLACES an occupied destination: a local rename
// replaces a file, an object store's copy-then-delete replaces the object and
// MERGES a folder, a WebDAV MOVE is sent with Overwrite: T. The catalogue then
// hard-deletes the row that held the name to make room (see
// protocolsync.ReclaimDestination) — its version history, shares and comments
// go with it, and nothing is put in the trash. So the answer has to come
// before the driver is asked.
//
// Two things count as taken, the same two trash.Service.occupied checks:
//
//   - a LIVE catalogue row at dstRel — what the listing shows, and what owns
//     the history — even when its bytes have gone missing;
//   - anything the driver's Stat finds there, file or folder.
//
// srcRel is the item being moved when it lives in the SAME storage, and it is
// what lets a case-only rename through: on a case-insensitive disk (a default
// macOS or Windows volume) Stat("A.txt") answers with "a.txt" — the source
// itself. When the two names differ only in case, the folder is listed and
// only an entry spelled EXACTLY like the new name counts; on a case-sensitive
// store that is the second file, which must not be replaced. Pass "" when the
// source lives in another storage.
//
// Any Stat failure other than a clean not-found is returned as an error, and
// the caller must refuse: failing open here is how a flaky backend turns into
// a destroyed file.
func destinationTaken(ctx context.Context, rows livePathLookup, drv storage.Driver, storageID int64, srcRel, dstRel string) (bool, error) {
	dstClean := normalizeDBPath(dstRel)
	if rows != nil {
		if row, err := rows.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, dstClean)); err == nil && row != nil {
			return true, nil
		}
	}
	if _, err := drv.Stat(ctx, dstRel); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if srcRel == "" {
		return true, nil
	}
	srcClean := normalizeDBPath(srcRel)
	if path.Dir(srcClean) != path.Dir(dstClean) || !strings.EqualFold(path.Base(srcClean), path.Base(dstClean)) {
		return true, nil
	}
	// The two names differ only in case: find out whether a second entry is
	// spelled like the new name, or whether Stat simply found the source.
	dir := strings.TrimPrefix(path.Dir(dstClean), "/")
	objs, err := drv.List(ctx, dir)
	if err != nil {
		return false, err
	}
	want := path.Base(dstClean)
	for _, o := range objs {
		if o.Name == want {
			return true, nil
		}
	}
	return false, nil
}

// clientErrText is an error's text with the server's own paths taken out.
//
// A local rename fails with *os.LinkError, whose message carries both absolute
// paths on the server's disk ("rename /srv/data/… /srv/data/…: file exists").
// That is meaningless to the person reading it and more than they should be
// shown. The inner errno says the same thing without the paths.
func clientErrText(err error) string {
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

package versioning

// What a file's history leaves on its storage once the file is gone for good.
//
// ⚠ Issue #104: snapshots are keyed by node id (`.versions/<id>/<n>`), and
// their node_versions rows go with the node (a foreign-key cascade). Every
// path that destroys a row for good - the trash purge, the storage sync
// dropping a file deleted outside filex, a delete whose bytes were already
// gone - therefore took the only record of the snapshots with it and left
// their bytes on the storage for ever, where nothing could reach them again.
//
// The two halves are separate because the record has to be read while the row
// still exists, and the bytes deleted only once it is gone: the sync drops
// rows inside a database transaction, and bytes deleted before a transaction
// that then rolls back are bytes a live file has lost.

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Keys returns the storage keys of nodeID's snapshots. Read it BEFORE the
// node's row is deleted: the version rows go with it.
func Keys(ctx context.Context, store db.Store, nodeID int64) []string {
	if store == nil {
		return nil
	}
	vs, err := store.ListNodeVersions(ctx, nodeID)
	if err != nil {
		return nil
	}
	var keys []string
	for _, v := range vs {
		if v != nil && v.StorageKey != "" {
			keys = append(keys, v.StorageKey)
		}
	}
	return keys
}

// Forget deletes nodeID's snapshot objects (keys, from Keys) from drv, then the
// node's folder under `.versions/` once nothing is left in it. Best effort: the
// row is already gone, so a failure is logged and leaves an orphan behind,
// never an error for the caller.
//
// ⚠ Only keys inside the node's own `.versions/<id>/` folder are deleted. The
// key comes from a database row; a row that named anything else - a live file,
// another node's history - is not something this cleanup may destroy.
func Forget(ctx context.Context, drv storage.Driver, nodeID int64, keys []string) {
	if drv == nil || len(keys) == 0 {
		return
	}
	d, ok := drv.(storage.Deleter)
	if !ok {
		return
	}
	dir := nodeDir(nodeID)
	for _, k := range keys {
		clean := strings.Trim(path.Clean("/"+k), "/")
		if !strings.HasPrefix(clean, dir+"/") {
			slog.Warn("versioning: a version row names a key outside its node's history; left alone",
				slog.Int64("node", nodeID), slog.String("key", k))
			continue
		}
		if err := d.Delete(ctx, clean); err != nil && !errors.Is(err, storage.ErrNotFound) {
			slog.Warn("versioning: could not delete the snapshot of a file gone for good",
				slog.Int64("node", nodeID), slog.String("key", clean), slog.String("err", err.Error()))
		}
	}
	if objs, err := drv.List(ctx, dir); err == nil && len(objs) == 0 {
		_ = d.Delete(ctx, dir)
	}
}

// nodeDir is the folder a node's snapshots live in (versionKey's parent).
func nodeDir(nodeID int64) string {
	return path.Join(VersionsPrefix, strconv.FormatInt(nodeID, 10))
}

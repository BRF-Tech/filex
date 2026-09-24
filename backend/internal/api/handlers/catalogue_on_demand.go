package handlers

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// catalogueOnDemand gives an entry that is on the STORAGE a row in the
// catalogue, exactly as a write through filex would have given it one, and
// answers the row. An entry that already has one gets it back untouched.
//
// ⚠⚠ Why it exists: a share points at a catalogue row, and the catalogue lags
// the storage — a file dropped into a storage outside filex (a local folder,
// another client, a mount) has no row until the next sync. The explorer lists
// it anyway (manager.go vfIndexFromDriver: cache first, driver fallback), so a
// person can pick it and share it — and both doors that open a link answered
// "no such file" / "file not found". Measured 2026-09-21 on an instance whose
// storage had a PDF on disk and no sync run yet: the signing app's request job
// failed at share_create, and the Share dialog answered 404. The cause is the
// lagging catalogue, so the catalogue is what is fixed, in ONE place, for both.
//
// ⚠ Callers check who may reach the entry BEFORE calling: this writes a row,
// and a row for a path the caller may not see is not this function's to
// refuse. share_create only ever passes one of its job's inputs (checked
// against the ACL when the job was queued); the Share dialog checks the
// tenant, the root confinement and editor rights on the path first.
//
// ⚠ Bookkeeping only (protocolsync.WriteRows / EnsureDirChain): no write hook,
// because nothing was written — the bytes were already there, and telling
// versioning, the antivirus and the webhooks that somebody just wrote them
// would be a lie. It is the same call the AI surface makes for a moved file
// whose source was never catalogued, for the same reason.
//
// ⚠ One visible consequence, and it is the one a filex upload into the same
// folder already has: a folder that had NO catalogued children was listed
// straight from the storage (vfIndex's pre-sync hatch); with one row in it,
// the listing is read from the catalogue until the next sync fills in the
// rest. Recorded here because it looks like a bug the first time it is seen.
func catalogueOnDemand(ctx context.Context, store db.Store, resolve func(int64) (storage.Driver, error),
	sy *protocolsync.Syncer, storageID int64, rel string) (*model.Node, error) {
	rel = strings.Trim(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	if rel == "" || rel == "." {
		return nil, errors.New("a storage root has no catalogue row to share")
	}
	if n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, "/"+rel)); err == nil && n != nil {
		return n, nil
	}
	if resolve == nil || sy == nil {
		return nil, errors.New("storage unavailable")
	}
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return nil, errors.New("storage row missing")
	}
	drv, err := resolve(storageID)
	if err != nil {
		return nil, err
	}
	obj, err := drv.Stat(ctx, rel)
	if err != nil {
		return nil, err
	}
	switch obj.Kind {
	case storage.KindFile:
		node, _, ok := sy.WriteRows(ctx, st, rel, obj.Size, obj.Mime)
		if !ok || node == nil {
			return nil, errors.New(rel + " could not be recorded in the catalogue")
		}
		return node, nil
	case storage.KindDirectory:
		id, err := sy.EnsureDirChain(ctx, st, rel)
		if err != nil || id == nil {
			return nil, errors.New(rel + " could not be recorded in the catalogue")
		}
		return store.GetNode(ctx, *id)
	}
	return nil, errors.New(rel + " is neither a file nor a folder")
}

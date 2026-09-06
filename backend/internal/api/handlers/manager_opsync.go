package handlers

import (
	"context"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// This file implements ops.DBSync on *Manager. The async ops worker
// (backend/internal/ops) calls these AFTER it has moved/deleted/copied the
// bytes on the storage driver, so the DB node index — which directory
// listings read from (Store.ListNodesByParent) — reflects the change.
//
// The logic deliberately reuses the exact same DB mutations the synchronous
// manager handlers (vfMove/vfDelete) perform, so the two code paths can never
// drift again. `src`/`dst` are bare storage-relative paths (no adapter
// prefix), matching what the ops worker holds.
//
// Each sync also routes through the writehook gate with origin "ops", so
// worker-driven copies/moves/deletes emit the same canonical file events
// (and, for copies, the same antivirus enqueue) the synchronous manager
// paths do. The ops worker runs on a background context with no request
// actor — the events simply carry no actor, like other system activity.
//
// ⚠ They also emit the realtime change frame, which they did not until the
// write-announce audit. Missing it here is not a small thing: an explorer with
// a live socket does not poll (see useRealtime.ts — the 12 s poll is the
// FALLBACK for a dead socket), so a copy or a move a user started from the UI
// finished, updated the row and the index, and left that user's own listing
// showing the old contents until they navigated away and back.
//
// There is no barrier to emitting from here and no missing data: EmitChange
// takes no context and no actor, it fans out to whoever is in the room, and
// storage id + storage-relative path are already parameters of every method
// below. The staged-upload commit path has been emitting from this very
// goroutine all along (upload_staged.go).

// SyncMove updates the moved node's path/parent. Delegates to the same
// helper vfMove uses.
func (h *Manager) SyncMove(ctx context.Context, storageID int64, src, dst string) {
	h.applyDBMove(ctx, storageID, src, dst)
	/* bag:b3 event */
	writehook.OnFileMoved(ctx, storageID, normalizeDBPath(src), normalizeDBPath(dst), path.Base(normalizeDBPath(dst)),
		writehook.OriginOps)
	emitMoved(storageID, normalizeDBPath(src), normalizeDBPath(dst))
}

// emitMoved tells both ends of a move. srcClean/dstClean are normalized
// storage-relative paths. A rename inside one folder is the same room twice,
// so it is announced once — carrying both names, which is what lets the hub
// follow a viewer's presence focus across the rename.
func emitMoved(storageID int64, srcClean, dstClean string) {
	ev := realtime.ChangeEvent{
		Action:  "move",
		Name:    path.Base(srcClean),
		NewName: path.Base(dstClean),
	}
	srcDir, dstDir := path.Dir(srcClean), path.Dir(dstClean)
	emitFolderChange(storageID, srcDir, ev)
	if dstDir != srcDir {
		emitFolderChange(storageID, dstDir, ev)
	}
}

// SyncSoftDelete flags the node deleted and retags it to the trash path,
// preserving the original path in storage_key so trash.Service.Restore can
// move the bytes back. Mirrors vfDelete's soft-delete DB branch.
func (h *Manager) SyncSoftDelete(ctx context.Context, storageID int64, src, trashRel string) {
	origClean := normalizeDBPath(src)
	origHash := pathkey.Hash(storageID, origClean)
	// ⚠ Deferred: the bytes are already in the trash, so every open listing of
	// this folder is wrong whether or not we find a row to retag below.
	defer emitFolderChange(storageID, path.Dir(origClean), realtime.ChangeEvent{
		Action: "delete", Name: path.Base(origClean),
	})
	/* bag:b3 event — the worker already moved the bytes into the trash */
	writehook.OnFileTrashed(ctx, storageID, origClean, path.Base(origClean),
		normalizeDBPath(trashRel), writehook.OriginOps)
	existing, err := h.Store.GetNodeByPath(ctx, storageID, origHash)
	if err != nil || existing == nil {
		return
	}
	trashClean := normalizeDBPath(trashRel)
	trashHash := pathkey.Hash(storageID, trashClean)
	_ = h.Store.SoftDeleteAndRetag(ctx, existing.ID, trashClean, trashHash, origClean)
	h.removeFromIndex(ctx, existing.ID)
}

// SyncHardDelete flags the node deleted when the driver couldn't move the
// file to trash and deleted the bytes outright. Mirrors vfDelete's no-mover
// branch.
func (h *Manager) SyncHardDelete(ctx context.Context, storageID int64, src string) {
	origClean := normalizeDBPath(src)
	origHash := pathkey.Hash(storageID, origClean)
	defer emitFolderChange(storageID, path.Dir(origClean), realtime.ChangeEvent{
		Action: "delete", Name: path.Base(origClean),
	})
	if existing, err := h.Store.GetNodeByPath(ctx, storageID, origHash); err == nil && existing != nil {
		_ = h.Store.SoftDeleteNode(ctx, existing.ID)
		h.removeFromIndex(ctx, existing.ID)
		/* bag:b3 event — only when the index actually reflected the file */
		writehook.OnFileDeleted(ctx, storageID, origClean, path.Base(origClean), writehook.OriginOps)
	}
}

// SyncCopy inserts a DB node for a freshly written copy, cloning the source
// node's type/size/mime. Idempotent: a node already at dst is left alone (a
// later background sync would reconcile anyway).
// SyncCopyAcross mirrors a copy whose ends live in two different storages —
// the cross-depo paste. The node is created under the DESTINATION storage,
// with the type/size/mime read from the source storage's node when there is
// one; there need not be (a folder nobody has browsed yet has no row), so the
// driver's own Stat is the fallback rather than a reason to skip the mirror.
// Skipping it is what leaves a pasted file invisible until the next scan.
func (h *Manager) SyncCopyAcross(ctx context.Context, srcStorageID int64, src string, dstStorageID int64, dst string) {
	if srcStorageID == dstStorageID {
		h.SyncCopy(ctx, srcStorageID, src, dst)
		return
	}
	dstClean := normalizeDBPath(dst)
	dstHash := pathkey.Hash(dstStorageID, dstClean)
	if existing, _ := h.Store.GetNodeByPath(ctx, dstStorageID, dstHash); existing != nil {
		return
	}
	// ⚠ ensureDirChain, not lookupDirID: the destination folder may have no
	// node row yet (nobody has browsed that depo), and giving up there is what
	// leaves a pasted file invisible. At the storage root the parent is "" and
	// this correctly returns nil — `path.Dir` on a bare basename would say "."
	// and find nothing.
	parentID, err := h.ensureDirChain(ctx, dstStorageID, path.Dir(dstClean))
	if err != nil {
		return
	}
	n := &model.Node{
		StorageID: dstStorageID,
		ParentID:  parentID,
		Name:      path.Base(dstClean),
		Path:      dstClean,
		PathHash:  dstHash,
		Type:      model.NodeTypeFile,
	}
	srcClean := normalizeDBPath(src)
	if srcNode, serr := h.Store.GetNodeByPath(ctx, srcStorageID, pathkey.Hash(srcStorageID, srcClean)); serr == nil && srcNode != nil {
		n.Type, n.Size, n.Mime = srcNode.Type, srcNode.Size, srcNode.Mime
		// Bill the new bytes to whoever owned the original, exactly as the
		// same-storage copy does — the copy is a duplicate of their file.
		if owner, oerr := h.Store.GetNodeOwner(ctx, srcNode.ID); oerr == nil && owner != nil && *owner > 0 {
			ctx = quotastore.WithOwner(ctx, *owner)
		}
	} else if h.StorageResolver != nil {
		// No cached source row — ask the destination driver what actually
		// landed. Guessing "file, 0 bytes" would put a wrong size in the
		// listing and in the quota.
		if drv, derr := h.StorageResolver(dstStorageID); derr == nil {
			if o, oerr := drv.Stat(ctx, strings.TrimPrefix(dstClean, "/")); oerr == nil {
				n.Size, n.Mime = o.Size, o.Mime
				if o.Kind == storage.KindDirectory {
					n.Type = model.NodeTypeDirectory
					n.Size = 0
				}
			}
		}
	}
	if created, cerr := h.Store.CreateNode(ctx, n); cerr == nil && created != nil {
		h.indexNode(ctx, created)
		writehook.OnFileWritten(ctx, dstStorageID, created, writehook.OriginOps, writehook.Created,
			map[string]any{"copy": true, "from": srcClean, "cross_storage": true})
		// ⚠ The DESTINATION storage's room. Emitting on srcStorageID here
		// would tell the wrong depo about a file it never received.
		emitFolderChange(dstStorageID, path.Dir(dstClean), realtime.ChangeEvent{
			Action: "upload", Name: created.Name,
		})
	}
}

func (h *Manager) SyncCopy(ctx context.Context, storageID int64, src, dst string) {
	dstClean := normalizeDBPath(dst)
	dstHash := pathkey.Hash(storageID, dstClean)
	if existing, _ := h.Store.GetNodeByPath(ctx, storageID, dstHash); existing != nil {
		return
	}
	srcClean := normalizeDBPath(src)
	srcHash := pathkey.Hash(storageID, srcClean)
	srcNode, err := h.Store.GetNodeByPath(ctx, storageID, srcHash)
	if err != nil || srcNode == nil {
		return
	}
	// Same reasoning as SyncCopyAcross: `path.Dir("orig-copy.txt")` is "." —
	// a directory that is never in the index — so a copy pasted at the STORAGE
	// ROOT found no parent and was never mirrored. `path.Dir("/orig-copy.txt")`
	// is "/", which ensureDirChain reads as the root.
	parentID, err := h.ensureDirChain(ctx, storageID, path.Dir(dstClean))
	if err != nil {
		return
	}
	// A copy is a second set of bytes on the disk and has to be counted, but
	// this runs on the ops worker's server-lifetime context — the requesting
	// user is long gone and the legacy `pending_ops` row does not carry them.
	// Bill the copy to whoever owns the ORIGINAL: the bytes are a duplicate of
	// theirs, and it needs no schema change to be true. Unowned source (a file
	// the scanner found) stays unowned, exactly as the original is.
	if owner, oerr := h.Store.GetNodeOwner(ctx, srcNode.ID); oerr == nil && owner != nil && *owner > 0 {
		ctx = quotastore.WithOwner(ctx, *owner)
	}
	n := &model.Node{
		StorageID: storageID,
		ParentID:  parentID,
		Name:      path.Base(dstClean),
		Path:      dstClean,
		PathHash:  dstHash,
		Type:      srcNode.Type,
		Size:      srcNode.Size,
		Mime:      srcNode.Mime,
	}
	if created, err := h.Store.CreateNode(ctx, n); err == nil && created != nil {
		h.indexNode(ctx, created)
		/* bag:b3 event + koru:k2 av — a copy writes fresh bytes; the gate
		   itself skips directories, so folder-copy rows stay silent. */
		writehook.OnFileWritten(ctx, storageID, created, writehook.OriginOps, writehook.Created,
			map[string]any{"copy": true, "from": srcClean})
		emitFolderChange(storageID, path.Dir(dstClean), realtime.ChangeEvent{
			Action: "upload", Name: created.Name,
		})
	}
}

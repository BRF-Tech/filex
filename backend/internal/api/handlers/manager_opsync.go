package handlers

import (
	"context"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
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

// SyncMove updates the moved node's path/parent. Delegates to the same
// helper vfMove uses.
func (h *Manager) SyncMove(ctx context.Context, storageID int64, src, dst string) {
	h.applyDBMove(ctx, storageID, src, dst)
	/* bag:b3 event */
	writehook.OnFileMoved(ctx, storageID, normalizeDBPath(src), normalizeDBPath(dst), path.Base(normalizeDBPath(dst)),
		writehook.OriginOps)
}

// SyncSoftDelete flags the node deleted and retags it to the trash path,
// preserving the original path in storage_key so trash.Service.Restore can
// move the bytes back. Mirrors vfDelete's soft-delete DB branch.
func (h *Manager) SyncSoftDelete(ctx context.Context, storageID int64, src, trashRel string) {
	origClean := normalizeDBPath(src)
	origHash := managerPathHash(storageID, origClean)
	/* bag:b3 event — the worker already moved the bytes into the trash */
	writehook.OnFileTrashed(ctx, storageID, origClean, path.Base(origClean),
		normalizeDBPath(trashRel), writehook.OriginOps)
	existing, err := h.Store.GetNodeByPath(ctx, storageID, origHash)
	if err != nil || existing == nil {
		return
	}
	trashClean := normalizeDBPath(trashRel)
	trashHash := managerPathHash(storageID, trashClean)
	_ = h.Store.SoftDeleteAndRetag(ctx, existing.ID, trashClean, trashHash, origClean)
	h.removeFromIndex(ctx, existing.ID)
}

// SyncHardDelete flags the node deleted when the driver couldn't move the
// file to trash and deleted the bytes outright. Mirrors vfDelete's no-mover
// branch.
func (h *Manager) SyncHardDelete(ctx context.Context, storageID int64, src string) {
	origClean := normalizeDBPath(src)
	origHash := managerPathHash(storageID, origClean)
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
func (h *Manager) SyncCopy(ctx context.Context, storageID int64, src, dst string) {
	dstClean := normalizeDBPath(dst)
	dstHash := managerPathHash(storageID, dstClean)
	if existing, _ := h.Store.GetNodeByPath(ctx, storageID, dstHash); existing != nil {
		return
	}
	srcClean := normalizeDBPath(src)
	srcHash := managerPathHash(storageID, srcClean)
	srcNode, err := h.Store.GetNodeByPath(ctx, storageID, srcHash)
	if err != nil || srcNode == nil {
		return
	}
	parentID, err := h.lookupDirID(ctx, storageID, path.Dir(strings.TrimPrefix(dstClean, "/")))
	if err != nil {
		return
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
		writehook.OnFileWritten(ctx, storageID, created, writehook.OriginOps,
			map[string]any{"copy": true, "from": srcClean})
	}
}

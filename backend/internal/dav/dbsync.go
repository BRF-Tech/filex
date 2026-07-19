package dav

// Best-effort DB node-cache + search-index + thumbnail sync after WebDAV
// mutations. Mirrors the patterns of the AI surface (handlers/ai_ops.go
// cacheUpsertFile/cacheUpsertDir/cacheMove) and manager.EnsureDir: listings
// read the DB cache, so a write that skips it would be invisible in the UI
// until the next scheduled sync run. Every function here logs-and-continues
// on failure — a sync hiccup must never fail the WebDAV response (the bytes
// already landed on the driver; the sync worker reconciles later).

import (
	"context"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// normalizeDBPath canonicalises a path the way the shared pathkey.Hash key
// expects (handlers.normalizeDBPath twin) — the cache rows written here must
// collide with the ones the sync worker and the manager write, or the same
// file would appear twice after the next sync run.
func normalizeDBPath(rel string) string {
	rel = strings.Trim(rel, "/")
	clean := path.Clean("/" + rel)
	return strings.TrimRight(clean, "/")
}

// syncWrite upserts the node row for a written file, indexes it (which also
// triggers the content-extraction hook when enabled) and dispatches a
// thumbnail.
func (h *Handler) syncWrite(ctx context.Context, st *model.Storage, rel string, size int64, mime string) {
	defer h.recoverSync("write", st, rel)
	clean := normalizeDBPath(rel)
	hash := pathkey.Hash(st.ID, clean)

	if existing, _ := h.cfg.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		if err := h.cfg.Store.UpdateNodeMeta(ctx, existing.ID, size, mime, existing.Etag, time.Now()); err != nil {
			slog.Warn("dav: node meta update", slog.String("path", clean), slog.String("err", err.Error()))
			return
		}
		existing.Size = size
		existing.Mime = mime
		h.indexNode(ctx, existing)
		h.dispatchThumb(existing)
		writehook.OnFileWritten(ctx, st.ID, existing, writehook.OriginDAV)
		return
	}

	parentID, err := h.ensureDirChain(ctx, st, parentOf(clean))
	if err != nil {
		slog.Warn("dav: parent chain", slog.String("path", clean), slog.String("err", err.Error()))
		return
	}
	node, err := h.cfg.Store.CreateNode(ctx, &model.Node{
		StorageID:  st.ID,
		ParentID:   parentID,
		Name:       path.Base(clean),
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeFile,
		Size:       size,
		Mime:       mime,
		SyncState:  model.SyncStateSynced,
	})
	if err != nil || node == nil {
		if err != nil {
			slog.Warn("dav: node create", slog.String("path", clean), slog.String("err", err.Error()))
		}
		return
	}
	h.indexNode(ctx, node)
	h.dispatchThumb(node)
	writehook.OnFileWritten(ctx, st.ID, node, writehook.OriginDAV)
}

// syncMkdir upserts the dir node chain for a created collection.
func (h *Handler) syncMkdir(ctx context.Context, st *model.Storage, rel string) {
	defer h.recoverSync("mkdir", st, rel)
	if _, err := h.ensureDirChain(ctx, st, normalizeDBPath(rel)); err != nil {
		slog.Warn("dav: mkdir sync", slog.String("path", rel), slog.String("err", err.Error()))
	}
}

// syncTrash retags the node row to its `.filex-trash/` location (original
// path preserved in storage_key) and soft-deletes it — the DB half of the
// DAV DELETE → trash flow, identical to the manager's vfDelete bookkeeping.
// Directories drag their cached subtree along inside SoftDeleteAndRetag;
// here we only collect the subtree UP FRONT (children are still live) so
// every affected row can be dropped from the search index afterwards.
func (h *Handler) syncTrash(ctx context.Context, st *model.Storage, rel, trashRel string) {
	defer h.recoverSync("trash", st, rel)
	clean := normalizeDBPath(rel)
	node, _ := h.cfg.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	if node == nil {
		return
	}
	subtree := h.collectSubtree(ctx, st.ID, node)
	trashClean := normalizeDBPath(trashRel)
	if err := h.cfg.Store.SoftDeleteAndRetag(ctx, node.ID, trashClean,
		pathkey.Hash(st.ID, trashClean), clean); err != nil {
		slog.Warn("dav: node trash", slog.Int64("id", node.ID), slog.String("err", err.Error()))
		return
	}
	for _, n := range subtree {
		h.removeFromIndex(ctx, n.ID)
	}
	writehook.OnFileTrashed(ctx, st.ID, clean, node.Name, trashClean, writehook.OriginDAV)
}

// syncDelete soft-deletes the node row (and, for directories, every cached
// descendant) and drops them from the search index.
func (h *Handler) syncDelete(ctx context.Context, st *model.Storage, rel string) {
	defer h.recoverSync("delete", st, rel)
	clean := normalizeDBPath(rel)
	node, _ := h.cfg.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	if node == nil {
		return
	}
	for _, n := range h.collectSubtree(ctx, st.ID, node) {
		if err := h.cfg.Store.SoftDeleteNode(ctx, n.ID); err != nil {
			slog.Warn("dav: node delete", slog.Int64("id", n.ID), slog.String("err", err.Error()))
			continue
		}
		h.removeFromIndex(ctx, n.ID)
	}
	writehook.OnFileDeleted(ctx, st.ID, clean, node.Name, writehook.OriginDAV)
}

// syncMove re-homes the node row (and cached descendants) to the new path
// and re-indexes each. On a conflicting destination row the move degrades to
// soft-deleting the source rows (the sync worker resurrects the truth).
func (h *Handler) syncMove(ctx context.Context, st *model.Storage, srcRel, dstRel string) {
	defer h.recoverSync("move", st, srcRel)
	srcClean := normalizeDBPath(srcRel)
	dstClean := normalizeDBPath(dstRel)
	node, _ := h.cfg.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, srcClean))
	if node == nil {
		return
	}
	parentID, err := h.ensureDirChain(ctx, st, parentOf(dstClean))
	if err != nil {
		slog.Warn("dav: move parent chain", slog.String("path", dstClean), slog.String("err", err.Error()))
		return
	}
	subtree := h.collectSubtree(ctx, st.ID, node)
	for _, n := range subtree {
		newPath := dstClean
		if n.ID != node.ID {
			newPath = dstClean + strings.TrimPrefix(n.Path, srcClean)
		}
		newHash := pathkey.Hash(st.ID, newPath)
		pid := n.ParentID
		if n.ID == node.ID {
			pid = parentID
		}
		if err := h.cfg.Store.MoveNode(ctx, n.ID, pid, path.Base(newPath), newPath, newHash); err != nil {
			_ = h.cfg.Store.SoftDeleteNode(ctx, n.ID)
			h.removeFromIndex(ctx, n.ID)
			continue
		}
		n.Path = newPath
		n.PathHash = newHash
		n.Name = path.Base(newPath)
		h.indexNode(ctx, n)
	}
	if node.Path == dstClean {
		writehook.OnFileMoved(ctx, st.ID, srcClean, dstClean, path.Base(dstClean), writehook.OriginDAV)
	}
}

// ensureDirChain walks rel segment by segment, creating any missing dir
// rows (manager.EnsureDir pattern), and returns the leaf dir's node id (nil
// at storage root).
func (h *Handler) ensureDirChain(ctx context.Context, st *model.Storage, rel string) (*int64, error) {
	rel = normalizeDBPath(rel)
	if rel == "" {
		return nil, nil
	}
	var parent *int64
	built := ""
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			continue
		}
		if built == "" {
			built = seg
		} else {
			built += "/" + seg
		}
		hash := pathkey.Hash(st.ID, built)
		if existing, _ := h.cfg.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
			id := existing.ID
			parent = &id
			continue
		}
		node, err := h.cfg.Store.CreateNode(ctx, &model.Node{
			StorageID:  st.ID,
			ParentID:   parent,
			Name:       seg,
			Path:       built,
			PathHash:   hash,
			StorageKey: built,
			Type:       model.NodeTypeDirectory,
			SyncState:  model.SyncStateSynced,
		})
		if err != nil || node == nil {
			return nil, err
		}
		h.indexNode(ctx, node)
		id := node.ID
		parent = &id
	}
	return parent, nil
}

// collectSubtree returns node plus every live cached descendant (DFS via
// ListNodesByParent). Directories only recurse; files are leaves.
func (h *Handler) collectSubtree(ctx context.Context, storageID int64, node *model.Node) []*model.Node {
	out := []*model.Node{node}
	if node.Type != model.NodeTypeDirectory {
		return out
	}
	var walk func(parentID int64, depth int)
	walk = func(parentID int64, depth int) {
		if depth > 64 {
			return
		}
		children, err := h.cfg.Store.ListNodesByParent(ctx, storageID, &parentID)
		if err != nil {
			return
		}
		for _, c := range children {
			if c.DeletedAt != nil {
				continue
			}
			out = append(out, c)
			if c.Type == model.NodeTypeDirectory {
				walk(c.ID, depth+1)
			}
		}
	}
	walk(node.ID, 0)
	return out
}

func (h *Handler) indexNode(ctx context.Context, n *model.Node) {
	if h.cfg.Index == nil || n == nil {
		return
	}
	if err := h.cfg.Index.IndexNode(ctx, n); err != nil {
		slog.Debug("dav: index node", slog.Int64("id", n.ID), slog.String("err", err.Error()))
	}
}

func (h *Handler) removeFromIndex(ctx context.Context, id int64) {
	if h.cfg.Index == nil {
		return
	}
	if err := h.cfg.Index.DeleteNode(ctx, id); err != nil {
		slog.Debug("dav: index delete", slog.Int64("id", id), slog.String("err", err.Error()))
	}
}

// dispatchThumb fires async thumbnail generation for a written file — the
// same behaviour manager/AI uploads get. Nil pipeline is a no-op.
func (h *Handler) dispatchThumb(node *model.Node) {
	if h.cfg.Thumbs == nil || node == nil {
		return
	}
	go func(n *model.Node) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn("dav: thumbnail panic", slog.Any("recover", rec))
			}
		}()
		tctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := h.cfg.Thumbs.GenerateThumb(tctx, n); err != nil && err != thumb.ErrSkipped {
			slog.Debug("dav: thumbnail dispatch",
				slog.Int64("node", n.ID), slog.String("err", err.Error()))
		}
	}(node)
}

// recoverSync keeps a panicking sync helper from killing the WebDAV reply.
func (h *Handler) recoverSync(op string, st *model.Storage, rel string) {
	if rec := recover(); rec != nil {
		slog.Warn("dav: sync panic",
			slog.String("op", op), slog.Int64("storage", st.ID),
			slog.String("path", rel), slog.Any("recover", rec))
	}
}

// Package protocolsync keeps the DB node cache, the search index, the
// thumbnails and the write hooks in step with what a protocol server just did
// to the bytes.
//
// # Why this is one package and not one per protocol
//
// A write is not "put the bytes down". It also has to upsert the node row,
// create any missing parent directory rows, index the result, dispatch a
// thumbnail, and fire the write hook that drives versioning, antivirus,
// webhooks and notify. Listings read the DB cache, so a write that skips it is
// invisible in the UI until the next scheduled sync run.
//
// All of that lived in internal/dav/dbsync.go, written for the one protocol
// filex had. S3, SFTP, FTPS, NFS and FUSE each need exactly the same thing,
// and a copy per protocol means every future fix has to be made five times.
// The fifth one is the one somebody forgets, and the symptom is silent: the
// file is there, so nothing looks broken — it is merely unindexed, has no
// thumbnail, and counts against nobody's quota.
//
// # Contract
//
// Every method is BEST EFFORT and never returns an error: the bytes already
// landed on the driver, so failing the protocol response afterwards would
// report a failure for an operation that succeeded. Failures are logged and
// the sync worker reconciles later. Panics are contained for the same reason.
//
// ⚠ The origin string is per protocol and must be a NEW writehook constant,
// never a borrowed one — the audit trail's answer to "which protocol wrote
// this" is exactly that field.
package protocolsync

import (
	"context"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Syncer performs the bookkeeping for one protocol.
type Syncer struct {
	Store db.Store
	// Index is optional; nil skips indexing.
	Index *search.Index
	// Thumbs is optional; nil skips thumbnail dispatch.
	Thumbs *thumb.Pipeline
	// Origin is the writehook origin for this protocol (writehook.OriginDAV,
	// OriginS3, …). It is what the audit trail reports.
	Origin string
}

// New builds a Syncer.
func New(store db.Store, index *search.Index, thumbs *thumb.Pipeline, origin string) *Syncer {
	return &Syncer{Store: store, Index: index, Thumbs: thumbs, Origin: origin}
}

// NormalizePath canonicalises a path the way the shared pathkey.Hash key
// expects. The cache rows written here must collide with the ones the sync
// worker and the manager write, or the same file appears twice after the next
// sync run.
func NormalizePath(rel string) string {
	rel = strings.Trim(rel, "/")
	clean := path.Clean("/" + rel)
	return strings.TrimRight(clean, "/")
}

// ParentOf returns the directory part of a relative path ("" at the root).
func ParentOf(rel string) string {
	rel = NormalizePath(rel)
	i := strings.LastIndex(rel, "/")
	if i <= 0 {
		return ""
	}
	return rel[:i]
}

// Write upserts the node row for a written file, indexes it (which also
// triggers the content-extraction hook when enabled) and dispatches a
// thumbnail.
func (s *Syncer) Write(ctx context.Context, st *model.Storage, rel string, size int64, mime string) {
	defer s.recoverSync("write", st, rel)
	clean := NormalizePath(rel)
	hash := pathkey.Hash(st.ID, clean)

	if existing, _ := s.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
		if err := s.Store.UpdateNodeMeta(ctx, existing.ID, size, mime, existing.Etag, time.Now()); err != nil {
			s.warn("node meta update", slog.String("path", clean), slog.String("err", err.Error()))
			return
		}
		existing.Size = size
		existing.Mime = mime
		s.IndexNode(ctx, existing)
		s.DispatchThumb(existing)
		writehook.OnFileWritten(ctx, st.ID, existing, s.Origin)
		return
	}

	parentID, err := s.EnsureDirChain(ctx, st, ParentOf(clean))
	if err != nil {
		s.warn("parent chain", slog.String("path", clean), slog.String("err", err.Error()))
		return
	}
	node, err := s.Store.CreateNode(ctx, &model.Node{
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
			s.warn("node create", slog.String("path", clean), slog.String("err", err.Error()))
		}
		return
	}
	s.IndexNode(ctx, node)
	s.DispatchThumb(node)
	writehook.OnFileWritten(ctx, st.ID, node, s.Origin)
}

// Touch records a new modification time for a file whose BYTES did not change.
//
// It is deliberately not Write: a client setting an mtime (rclone does it after
// every upload it thinks needs it) has changed no content, so re-indexing the
// file and re-cutting its thumbnail would be pure cost — a thousand-file sync
// would do a thousand of each for nothing. Nothing is created either; if the
// row is not there yet, the write that will create it is the one that carries
// the truth.
func (s *Syncer) Touch(ctx context.Context, st *model.Storage, rel string, mtime time.Time) {
	defer s.recoverSync("touch", st, rel)
	clean := NormalizePath(rel)
	node, _ := s.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	if node == nil {
		return
	}
	if err := s.Store.UpdateNodeMeta(ctx, node.ID, node.Size, node.Mime, node.Etag, mtime); err != nil {
		s.warn("node touch", slog.String("path", clean), slog.String("err", err.Error()))
	}
}

// Mkdir upserts the dir node chain for a created collection.
func (s *Syncer) Mkdir(ctx context.Context, st *model.Storage, rel string) {
	defer s.recoverSync("mkdir", st, rel)
	if _, err := s.EnsureDirChain(ctx, st, NormalizePath(rel)); err != nil {
		s.warn("mkdir sync", slog.String("path", rel), slog.String("err", err.Error()))
	}
}

// Trash retags the node row to its `.filex-trash/` location (original path
// preserved in storage_key) and soft-deletes it.
//
// Directories drag their cached subtree along inside SoftDeleteAndRetag; the
// subtree is collected UP FRONT (children are still live) so every affected
// row can be dropped from the search index afterwards.
func (s *Syncer) Trash(ctx context.Context, st *model.Storage, rel, trashRel string) {
	defer s.recoverSync("trash", st, rel)
	clean := NormalizePath(rel)
	node, _ := s.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	if node == nil {
		return
	}
	subtree := s.CollectSubtree(ctx, st.ID, node)
	trashClean := NormalizePath(trashRel)
	if err := s.Store.SoftDeleteAndRetag(ctx, node.ID, trashClean,
		pathkey.Hash(st.ID, trashClean), clean); err != nil {
		s.warn("node trash", slog.Int64("id", node.ID), slog.String("err", err.Error()))
		return
	}
	for _, n := range subtree {
		s.RemoveFromIndex(ctx, n.ID)
	}
	writehook.OnFileTrashed(ctx, st.ID, clean, node.Name, trashClean, s.Origin)
}

// Delete drops the node row (and, for directories, every cached descendant)
// and removes them from the search index.
//
// This is the path taken when the bytes are gone for good — either the driver
// could not trash them, or there was nothing at the path to begin with. The
// rows are HARD deleted on purpose: a soft delete would park them in the trash
// listing with a Restore button behind which no bytes exist, which reads to the
// user as data loss with a broken recovery rather than an honest deletion.
//
// ⚠ Children are removed before their parent: nodes.parent_id cascades, so
// deleting the folder row first would drop the child rows before they could be
// dropped from the search index, leaving them searchable forever.
func (s *Syncer) Delete(ctx context.Context, st *model.Storage, rel string) {
	defer s.recoverSync("delete", st, rel)
	clean := NormalizePath(rel)
	node, _ := s.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, clean))
	if node == nil {
		return
	}
	subtree := s.CollectSubtree(ctx, st.ID, node)
	for i := len(subtree) - 1; i >= 0; i-- {
		n := subtree[i]
		if err := s.Store.HardDeleteNode(ctx, n.ID); err != nil {
			s.warn("node delete", slog.Int64("id", n.ID), slog.String("err", err.Error()))
			continue
		}
		s.RemoveFromIndex(ctx, n.ID)
	}
	writehook.OnFileDeleted(ctx, st.ID, clean, node.Name, s.Origin)
}

// Move re-homes the node row (and cached descendants) to the new path and
// re-indexes each. On a conflicting destination row the move degrades to
// soft-deleting the source rows (the sync worker resurrects the truth).
func (s *Syncer) Move(ctx context.Context, st *model.Storage, srcRel, dstRel string) {
	defer s.recoverSync("move", st, srcRel)
	srcClean := NormalizePath(srcRel)
	dstClean := NormalizePath(dstRel)
	node, _ := s.Store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, srcClean))
	if node == nil {
		return
	}
	parentID, err := s.EnsureDirChain(ctx, st, ParentOf(dstClean))
	if err != nil {
		s.warn("move parent chain", slog.String("path", dstClean), slog.String("err", err.Error()))
		return
	}
	subtree := s.CollectSubtree(ctx, st.ID, node)
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
		if err := s.Store.MoveNode(ctx, n.ID, pid, path.Base(newPath), newPath, newHash); err != nil {
			_ = s.Store.SoftDeleteNode(ctx, n.ID)
			s.RemoveFromIndex(ctx, n.ID)
			continue
		}
		n.Path = newPath
		n.PathHash = newHash
		n.Name = path.Base(newPath)
		s.IndexNode(ctx, n)
	}
	if node.Path == dstClean {
		writehook.OnFileMoved(ctx, st.ID, srcClean, dstClean, path.Base(dstClean), s.Origin)
	}
}

// EnsureDirChain walks rel segment by segment, creating any missing dir rows,
// and returns the leaf dir's node id (nil at storage root).
func (s *Syncer) EnsureDirChain(ctx context.Context, st *model.Storage, rel string) (*int64, error) {
	rel = NormalizePath(rel)
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
		if existing, _ := s.Store.GetNodeByPath(ctx, st.ID, hash); existing != nil {
			id := existing.ID
			parent = &id
			continue
		}
		node, err := s.Store.CreateNode(ctx, &model.Node{
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
		s.IndexNode(ctx, node)
		id := node.ID
		parent = &id
	}
	return parent, nil
}

// CollectSubtree returns node plus every live cached descendant (DFS via
// ListNodesByParent). Directories only recurse; files are leaves.
func (s *Syncer) CollectSubtree(ctx context.Context, storageID int64, node *model.Node) []*model.Node {
	out := []*model.Node{node}
	if node.Type != model.NodeTypeDirectory {
		return out
	}
	var walk func(parentID int64, depth int)
	walk = func(parentID int64, depth int) {
		if depth > 64 {
			return
		}
		children, err := s.Store.ListNodesByParent(ctx, storageID, &parentID)
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

// IndexNode adds or updates a node in the search index.
func (s *Syncer) IndexNode(ctx context.Context, n *model.Node) {
	if s.Index == nil || n == nil {
		return
	}
	if err := s.Index.IndexNode(ctx, n); err != nil {
		slog.Debug(s.Origin+": index node", slog.Int64("id", n.ID), slog.String("err", err.Error()))
	}
}

// RemoveFromIndex drops a node from the search index.
func (s *Syncer) RemoveFromIndex(ctx context.Context, id int64) {
	if s.Index == nil {
		return
	}
	if err := s.Index.DeleteNode(ctx, id); err != nil {
		slog.Debug(s.Origin+": index delete", slog.Int64("id", id), slog.String("err", err.Error()))
	}
}

// DispatchThumb fires async thumbnail generation for a written file — the same
// behaviour manager/AI uploads get. A nil pipeline is a no-op.
func (s *Syncer) DispatchThumb(node *model.Node) {
	if s.Thumbs == nil || node == nil {
		return
	}
	origin := s.Origin
	go func(n *model.Node) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn(origin+": thumbnail panic", slog.Any("recover", rec))
			}
		}()
		tctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.Thumbs.GenerateThumb(tctx, n); err != nil && err != thumb.ErrSkipped {
			slog.Debug(origin+": thumbnail dispatch",
				slog.Int64("node", n.ID), slog.String("err", err.Error()))
		}
	}(node)
}

func (s *Syncer) warn(msg string, attrs ...any) {
	slog.Warn(s.Origin+": "+msg, attrs...)
}

// recoverSync keeps a panicking helper from killing the protocol reply.
//
// ⚠ It must be the DEFERRED function itself: recover() only works when called
// directly by a deferred call, so wrapping it one level deeper silently stops
// containing anything. Naming it `recover` would also shadow the builtin.
func (s *Syncer) recoverSync(op string, st *model.Storage, rel string) {
	if rec := recover(); rec != nil {
		slog.Warn(s.Origin+": sync panic",
			slog.String("op", op), slog.Int64("storage", st.ID),
			slog.String("path", rel), slog.Any("recover", rec))
	}
}

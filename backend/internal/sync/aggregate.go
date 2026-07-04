package sync

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/db"
)

// RecomputeFolderSizes walks a storage's cached node tree and stores each
// folder's recursive total size (the sum of its descendant file sizes) in that
// folder's own `size` column. The file explorer then shows folder sizes served
// straight from the index — no backend re-scan, no per-folder query.
//
// Soft-deleted nodes (including trashed items under .filex-trash) are excluded
// by AggNodes, so trash never counts toward a folder's size. Only rows whose
// size actually changed are written back. Called at the end of every sync (and
// can be invoked directly, e.g. after an upgrade, to backfill).
func RecomputeFolderSizes(ctx context.Context, store db.Store, storageID int64) error {
	rows, err := store.AggNodes(ctx, storageID)
	if err != nil {
		return err
	}

	byID := make(map[int64]*db.NodeAgg, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	children := make(map[int64][]*db.NodeAgg, len(rows))
	var roots []*db.NodeAgg
	for i := range rows {
		n := &rows[i]
		if n.ParentID != nil {
			if _, ok := byID[*n.ParentID]; ok {
				children[*n.ParentID] = append(children[*n.ParentID], n)
				continue
			}
		}
		// real root, or an orphan whose parent is gone — treat as a root so
		// its subtree still gets aggregated.
		roots = append(roots, n)
	}

	sizes := make(map[int64]int64, len(rows))
	var sum func(n *db.NodeAgg) int64
	sum = func(n *db.NodeAgg) int64 {
		if !n.IsDir {
			return n.Size
		}
		var total int64
		for _, c := range children[n.ID] {
			total += sum(c)
		}
		sizes[n.ID] = total
		return total
	}
	for _, r := range roots {
		sum(r)
	}

	for id, sz := range sizes {
		if byID[id].Size != sz {
			if err := store.SetNodeSize(ctx, id, sz); err != nil {
				return err
			}
		}
	}
	return nil
}

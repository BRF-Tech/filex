package sync

import (
	"context"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
)

// RecomputeFolderSizes walks a storage's cached node tree and stores, for every
// folder, two aggregates in that folder's own row:
//
//   - size          = the sum of its descendant file sizes (recursive total)
//   - backend_mtime = its newest descendant mtime ("last activity")
//
// The file explorer then shows folder size + date served straight from the
// index — no backend re-scan, no per-folder query. The date matters for drivers
// that don't report a native directory mtime (e.g. synthetic S3 prefixes have
// none); "newest descendant" is both universal and the more useful semantic
// (it tracks when the folder's contents last changed).
//
// Soft-deleted nodes (including trashed items under .filex-trash) are excluded
// by AggNodes, so trash never counts toward a folder's aggregates. Only rows
// whose value actually changed are written back. Called at the end of every
// sync (and can be invoked directly, e.g. after an upgrade, to backfill).
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
	mtimes := make(map[int64]*time.Time, len(rows))
	// walk returns a subtree's (total file size, newest mtime). Only folders get
	// recorded in the maps — a file contributes its own size/mtime upward.
	var walk func(n *db.NodeAgg) (int64, *time.Time)
	walk = func(n *db.NodeAgg) (int64, *time.Time) {
		if !n.IsDir {
			return n.Size, n.Mtime
		}
		var total int64
		newest := n.Mtime
		for _, c := range children[n.ID] {
			cs, cm := walk(c)
			total += cs
			newest = maxTime(newest, cm)
		}
		sizes[n.ID] = total
		mtimes[n.ID] = newest
		return total, newest
	}
	for _, r := range roots {
		walk(r)
	}

	for id, sz := range sizes {
		if byID[id].Size != sz {
			if err := store.SetNodeSize(ctx, id, sz); err != nil {
				return err
			}
		}
	}
	for id, m := range mtimes {
		if !sameTime(byID[id].Mtime, m) {
			if err := store.SetNodeMtime(ctx, id, m); err != nil {
				return err
			}
		}
	}
	return nil
}

// maxTime returns the later of two nullable times (nil = unknown, loses).
func maxTime(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.After(*a) {
		return b
	}
	return a
}

// sameTime reports whether two nullable times are equal (both nil, or equal).
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

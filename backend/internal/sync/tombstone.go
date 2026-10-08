package sync

// tombstone.go — what the sync does with a catalogue row whose object is gone
// from the storage. The candidates and their guards live in poll.go (RunOnce,
// guardOK, confirmGone) and lazy.go (the per-folder delete pass); both end
// here, in dropRows.
//
// ⚠⚠ Issue #74: such a row is DROPPED, not moved to the trash. Up to 0.47 the
// pass soft-deleted it where it stood, and the Trash listed it: a file deleted
// in a shell, on the NAS or in the bucket showed up in filex's Trash with a
// Restore that could bring nothing back — its bytes were never in
// `.filex-trash/`, and all a restore produced was a live row for a file that
// is not there, which the next sync took away again. The Trash is where
// deleted BYTES wait; a row that has none is not an entry of it
// (trash.Vanished).
//
// Why dropped rather than parked in some hidden state:
//
//   - nothing can use the row again. A file that comes back at the same path is
//     catalogued as a NEW row on purpose (catalogueEntry): bytes that reappear
//     are not the file that was deleted, and handing them the old row's
//     shares, comments and version history would expose them through links
//     made for something else;
//   - it is what filex's own delete surfaces already do with a row whose bytes
//     are gone (vfDelete, protocolsync.DeleteRows);
//   - a hidden state would keep billing the owner's quota for bytes that do not
//     exist, and every trash query would have to learn to skip it.
//
// Because a drop cannot be undone, the pass is stricter about what it may
// drop than it was about what it trashed: every row, folders included, is
// confirmed gone by its own Stat (confirmGone), nothing below a folder the walk
// could not list is a candidate (walkCounts.unlisted), and the whole-listing
// guard is unchanged.
//
// Roadmap (unchanged): a per-storage guard threshold; a persistent "miss
// counter" so a row has to be missing N runs in a row.

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// droppedHistory is a dropped file's snapshot keys, deleted from the storage
// once the drop has committed (handOff).
type droppedHistory struct {
	node int64
	keys []string
}

// dropRows removes from the catalogue, for good, rows whose objects are
// confirmed gone from the storage, and returns how many of the LIVE ones it
// removed.
//
// Everything a row carries goes with it, as with a trash purge:
//
//   - the owner's quota is released (HardDeleteNode in internal/quotastore,
//     one file row at a time — the parent_id cascade releases nothing);
//   - the foreign keys cascade its shares, version rows, thumbnail row,
//     comments, tags, stars and per-user metadata;
//   - its search document and its cached files (thumbnail, staging) are
//     released through b, AFTER the caller's transaction commits (handOff);
//   - so are a file's snapshots under `.versions/<id>/` (issue #104): their
//     rows cascade with it, so their keys are read before the drop.
//
// Access grants are written about paths, not rows, and stay as they are.
//
// ⚠⚠ The parent_id cascade must never take a row this pass did not decide on.
// So the rows go deepest first, and a folder goes only when no row names it as
// its parent any more: a child the pass kept (its Stat still sees it, it is an
// upload in flight, a scan exclusion covers it) keeps the folder too.
//
// A folder also takes the rows deleted where they stood below it (vanished,
// see trash.Vanished): an earlier version's tombstones of its contents, which
// still name it as their parent. They are no trash entries and would
// otherwise pin the folder for good.
//
// ⚠⚠ Each row is read again right before it goes, and kept when it no longer
// stands where it stood when it was confirmed gone (issue #192). The decision
// was made about a PATH - its Stat said "not found" - and the drop is made by
// ID: a row a move re-homed in between is a row whose object is fine at its
// new path. The storage's gate (rowgate) keeps filex's own moves out of the
// pass; this keeps out the ones it does not cover (a second filex process on
// the same database, a protocol surface that moves without the gate).
//
// Every row dropped is said at INFO (droppedLogMax of them per call, then a
// count): this used to be silent, and a row that vanished left nothing in the
// server log to tell a drop from a bug.
func (s *storageSyncer) dropRows(ctx context.Context, rows []*model.Node, b *entryBatch) int {
	live := map[int64]bool{}
	var all []*model.Node
	add := func(n *model.Node) {
		if n == nil {
			return
		}
		if _, dup := live[n.ID]; dup {
			return
		}
		live[n.ID] = n.DeletedAt == nil
		all = append(all, n)
	}
	var dirs []string
	for _, n := range rows {
		add(n)
		if n != nil && n.Type == model.NodeTypeDirectory {
			dirs = append(dirs, n.Path)
		}
	}
	// Only when the storage has such rows at all (after the upgrade's first
	// full pass it has none), and one subtree query per outermost folder:
	// a folder tree deleted outside filex can be thousands of folders.
	if len(dirs) > 0 {
		if some, err := s.store.ListVanishedNodeIDs(ctx, s.storage.ID, 0, 1); err == nil && len(some) > 0 {
			for _, d := range dirs {
				if belowAny(d, dirs) {
					continue // its outermost folder's query covers it
				}
				below, err := s.store.ListNodesUnder(ctx, s.storage.ID, d, true)
				if err != nil {
					continue
				}
				for _, c := range below {
					if trash.Vanished(c) {
						add(c)
					}
				}
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		di, dj := pathDepth(all[i].Path), pathDepth(all[j].Path)
		if di != dj {
			return di > dj
		}
		return all[i].ID > all[j].ID
	})
	// dropped: the live rows dropped (the answer); went: every row dropped;
	// said: the ones said one by one.
	dropped, went, said := 0, 0, 0
	for _, n := range all {
		if n.Type == model.NodeTypeDirectory {
			left, err := s.store.CountChildRows(ctx, n.ID)
			if err != nil || left > 0 {
				if err == nil && live[n.ID] {
					slog.Info("sync: keeping a folder row that still has rows under it",
						slog.Int64("node", n.ID), slog.String("path", n.Path),
						slog.Int("rows", left), slog.String("storage", s.storage.Name))
				}
				continue
			}
		}
		if s.stillAsListed(ctx, n) == nil {
			continue
		}
		// The file's snapshots under `.versions/<id>/`: their rows go with this
		// one, so their keys are read now and the bytes deleted after the
		// commit (handOff). Up to 0.49 they were left on the storage for good.
		var history []string
		if n.Type == model.NodeTypeFile {
			history = versioning.Keys(ctx, s.store, n.ID)
		}
		if err := s.store.HardDeleteNode(ctx, n.ID); err != nil {
			slog.Warn("sync: could not drop the row of an object gone from the storage",
				slog.Int64("node", n.ID), slog.String("path", n.Path), slog.String("err", err.Error()))
			continue
		}
		if live[n.ID] {
			dropped++
		}
		went++
		if said < droppedLogMax {
			said++
			slog.Info("sync: dropped the row of an object gone from the storage",
				slog.Int64("node", n.ID),
				slog.String("path", n.Path),
				slog.String("type", string(n.Type)),
				slog.Bool("live", live[n.ID]),
				slog.String("storage", s.storage.Name))
		}
		b.unindex = append(b.unindex, n.ID)
		b.reclaim = append(b.reclaim, n.ID)
		if len(history) > 0 {
			b.history = append(b.history, droppedHistory{node: n.ID, keys: history})
		}
	}
	if more := went - said; more > 0 {
		slog.Info("sync: dropped more rows of objects gone from the storage than are listed one by one",
			slog.Int("more", more), slog.String("storage", s.storage.Name))
	}
	return dropped
}

// droppedLogMax bounds the per-row INFO lines one dropRows call writes: a
// folder of a hundred thousand files deleted outside filex is one summary
// line after the first ones, not a hundred thousand.
const droppedLogMax = 50

// dropVanished drops every row of this storage that an earlier version
// soft-deleted where it stood (trash.Vanished): the tombstones the sync wrote
// up to 0.47 for files it found gone, which the Trash listed with a Restore
// that could do nothing (issue #74). It is the upgrade's repair and runs on
// every full pass; on a healthy install it finds nothing.
//
// Catalogue only, like the other reconcile passes: such a row never had bytes
// in the trash, and whatever stands at its path now arrived later — the trash
// purge leaves that path alone for the same reason (trash.ownsBytesAt).
// Best-effort: it never fails the run.
func (s *storageSyncer) dropVanished(ctx context.Context) {
	const batch = 500
	var rows []*model.Node
	for after := int64(0); ; {
		ids, err := s.store.ListVanishedNodeIDs(ctx, s.storage.ID, after, batch)
		if err != nil {
			slog.Warn("sync: vanished-row query",
				slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
			return
		}
		for _, id := range ids {
			after = max(after, id)
			if n, err := s.store.GetNode(ctx, id); err == nil && trash.Vanished(n) {
				rows = append(rows, n)
			}
		}
		if len(ids) < batch || ctx.Err() != nil {
			break
		}
	}
	if len(rows) == 0 {
		return
	}
	b := &entryBatch{}
	s.dropRows(ctx, rows, b)
	s.handOff(ctx, b)
	slog.Info("sync: dropped rows an earlier version left in the trash for files gone from the storage; none of them had anything in the trash",
		slog.Int("dropped", len(b.unindex)),
		slog.Int("found", len(rows)),
		slog.String("storage", s.storage.Name))
}

// pathDepth is how many segments deep p is, in either spelling a row carries.
func pathDepth(p string) int {
	return strings.Count(strings.Trim(p, "/"), "/")
}

// belowAny reports whether p is strictly below one of dirs, in either spelling
// a path carries.
func belowAny(p string, dirs []string) bool {
	rp := strings.Trim(p, "/")
	for _, d := range dirs {
		dp := strings.Trim(d, "/")
		if dp == "" || (rp != dp && strings.HasPrefix(rp, dp+"/")) {
			return true
		}
	}
	return false
}

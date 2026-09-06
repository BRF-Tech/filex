package sync

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// RunOnce performs one full sync pass for the storage:
//  1. Open a sync_runs row (status=running)
//  2. Recursively walk the backend, upserting nodes and updating seen_at
//  3. Reconcile the trash bucket: nothing may be live in there.
//  4. Tombstone-pass: any node whose seen_at < runStart is soft-deleted —
//     but only if seen_count >= 0.7 * lastSeenCount (false-positive guard).
//  5. Close the sync_runs row with the final status.
//
// ⚠ Step 2 skips `.filex-trash/` entirely, so `seen` no longer counts trashed
// objects. On the first pass after upgrading, a storage whose trash held more
// than 30% of its objects will trip the step-4 guard once (a warning, and one
// tombstone pass skipped); the next run compares like with like.
func (s *storageSyncer) RunOnce(ctx context.Context) error {
	// `runStart` is truncated to second precision to match SQLite's
	// CURRENT_TIMESTAMP resolution. Without the truncation a sub-second
	// runStart compares STRICTLY GREATER than every same-second seen_at
	// touched during the run (because TouchNodeSeen + UpdateNodeMeta
	// both write CURRENT_TIMESTAMP, which has no fractional part). The
	// tombstone-pass would then re-delete the nodes the walk just
	// resurrected — exactly the loop we hit on s3-test://.
	runStart := time.Now().Truncate(time.Second)
	prevSeen, _ := s.previousSeenCount(ctx)
	run, err := s.store.CreateSyncRun(ctx, s.storage.ID, s.storage.LastSyncToken)
	if err != nil {
		return err
	}
	added, updated := 0, 0
	seen, err := s.walk(ctx, "/", nil, &added, &updated)
	if err != nil {
		_ = s.store.FinishSyncRun(ctx, run.ID, "", seen, added, updated, 0, "failed", err.Error())
		return err
	}

	s.reconcileTrash(ctx)

	deleted := 0
	if guardOK(seen, prevSeen) {
		stale, err := s.store.ListStaleNodes(ctx, s.storage.ID, runStart)
		if err == nil {
			for _, n := range stale {
				if !s.confirmGone(ctx, n) {
					continue
				}
				if err := s.store.SoftDeleteNode(ctx, n.ID); err == nil {
					deleted++
					if s.index != nil {
						_ = s.index.DeleteNode(ctx, n.ID)
					}
				}
			}
		}
	} else {
		slog.Warn("sync: tombstone guard tripped",
			slog.Int("seen", seen),
			slog.Int("prev_seen", prevSeen),
			slog.String("storage", s.storage.Name))
	}

	_ = s.store.UpdateStorageSyncCursor(ctx, s.storage.ID, runStart, "")
	// Cache each folder's recursive size on its own row so the explorer can show
	// folder sizes without re-scanning the backend (best-effort; never fails the
	// sync).
	if err := RecomputeFolderSizes(ctx, s.store, s.storage.ID); err != nil {
		slog.Warn("sync: folder-size recompute",
			slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
	}
	_ = s.store.FinishSyncRun(ctx, run.ID, "", seen, added, updated, deleted, "ok", "")
	return nil
}

// walk recursively lists the storage from `path` downwards. parent is the
// DB id of the parent node (nil at root).
func (s *storageSyncer) walk(ctx context.Context, p string, parent *int64, added, updated *int) (int, error) {
	objs, err := s.driver.List(ctx, p)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, obj := range objs {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}
		// filex's own trash bucket is not part of the catalogue and is not
		// the walk's to reconcile. The rows for everything in there already
		// exist -- soft-deleted, retagged to the very keys sitting on the
		// storage -- and they are maintained by the trash service (restore,
		// retention purge), never by a listing. Walking in was how a deletion
		// undid itself.
		if trash.IsTrashPath(obj.Path) {
			continue
		}
		hash := pathkey.Hash(s.storage.ID, obj.Path)
		existing, _ := s.store.GetNodeByPath(ctx, s.storage.ID, hash)
		if existing == nil {
			// ⚠⚠ There may still be a TRASHED row at this path -- not the
			// everyday deletion (that row was retagged into `.filex-trash`,
			// which we no longer walk), but the rows soft-deleted where they
			// stood: the tombstone pass's own SoftDeleteNode, and the error
			// branches in applyDBMove / SyncHardDelete.
			//
			// This used to clear deleted_at and carry on, on the theory that
			// UNIQUE(storage_id, path_hash) left no other way to catalogue the
			// object. Migration 00032 made that index live-only, so there is
			// now a better answer, and reviving was always the wrong one:
			// bytes that reappear at a path are NOT the file that was deleted
			// there. Someone restored something out of band, or a new file
			// landed with an old name. Reviving the row hands the new bytes
			// another file's identity, version history, comments and shares,
			// and -- because nothing downstream sees a new file -- means
			// nothing ever looks at them again.
			//
			// So: leave the trashed row in the trash (still restorable, still
			// on the retention clock) and catalogue what is really there as a
			// NEW node, which is indexed and treated as new everywhere else.
			if trashed, _ := s.store.GetNodeByPathIncludingDeleted(ctx, s.storage.ID, hash); trashed != nil && trashed.DeletedAt != nil {
				slog.Info("sync: an object reappeared where a trashed row still sits; catalogueing it as a new file",
					slog.Int64("trashed_node", trashed.ID),
					slog.String("path", obj.Path),
					slog.String("storage", s.storage.Name))
			}
			n := &model.Node{
				StorageID:    s.storage.ID,
				ParentID:     parent,
				Name:         obj.Name,
				Path:         obj.Path,
				PathHash:     hash,
				StorageKey:   obj.Path,
				Type:         model.NodeType(string(obj.Kind)),
				Size:         obj.Size,
				Mime:         obj.Mime,
				Etag:         obj.Etag,
				BackendMtime: timePtr(obj.Mtime),
				SyncState:    model.SyncStateSynced,
			}
			if obj.Kind == storage.KindDirectory {
				n.Type = model.NodeTypeDirectory
			} else {
				n.Type = model.NodeTypeFile
			}
			created, err := s.store.CreateNode(ctx, n)
			if err != nil {
				slog.Warn("sync: create node failed", slog.String("path", obj.Path), slog.String("err", err.Error()))
				continue
			}
			*added++
			count++
			if s.index != nil {
				_ = s.index.IndexNode(ctx, created)
			}
			// A file nobody wrote through filex, catalogued for the first
			// time. This — not the drift branch below — is the first import
			// of an existing storage, and the reason the hook exists.
			s.enqueueScan(ctx, created)
			if obj.Kind == storage.KindDirectory {
				cn, err := s.walk(ctx, obj.Path, &created.ID, added, updated)
				if err == nil {
					count += cn
				}
			}
		} else {
			// existing — update if the backend's copy drifted from the row
			drifted := false
			if objectDrift(existing, obj) {
				if err := s.store.UpdateNodeMeta(ctx, existing.ID, obj.Size, obj.Mime, obj.Etag, obj.Mtime); err == nil {
					*updated++
					drifted = true
				}
			} else {
				_ = s.store.TouchNodeSeen(ctx, existing.ID)
				// Backfill a missing backend_mtime for nodes first synced by an
				// older version (before mtime was recorded on insert). Without
				// this, files whose content never drifts keep a null date
				// forever, so their folders never get a "last activity" date
				// after an upgrade. One cheap write per node, only while null.
				if existing.BackendMtime == nil && !obj.Mtime.IsZero() {
					_ = s.store.SetNodeMtime(ctx, existing.ID, timePtr(obj.Mtime))
				}
			}
			// ⚠ Only a DRIFTED file is re-read here. The walk sees every
			// object on every pass, so hanging a scan off "the walk saw it"
			// would re-scan the whole storage every sync interval, forever.
			// Content that has not changed has already been scanned by the
			// pass that first catalogued it.
			//
			// The row is re-read once and shared by both consumers: `existing`
			// still carries the PRE-drift size, and the scanner's size ceiling
			// has to be applied to the bytes that are actually there.
			if drifted && (s.index != nil || s.avScan != nil) {
				if fresh, _ := s.store.GetNode(ctx, existing.ID); fresh != nil {
					if s.index != nil {
						_ = s.index.IndexNode(ctx, fresh)
					}
					s.enqueueScan(ctx, fresh)
				}
			}
			count++
			if existing.Type == model.NodeTypeDirectory {
				cn, err := s.walk(ctx, obj.Path, &existing.ID, added, updated)
				if err == nil {
					count += cn
				}
			}
		}
	}
	return count, nil
}

// enqueueScan hands a node the walk has just catalogued (or just found
// drifted) to the antivirus queue.
//
// It is the ONLY place the walk talks to the scanner, and it is deliberately
// thin: the eligibility rules live in queue.AntivirusScanner.Eligible, which
// already refuses directories, deleted rows, empty and oversized files, and
// anything under `.filex-trash/` or `.versions/`. Re-stating any of that here
// would be a second copy of a rule that has to stay identical to the one every
// other write surface is judged by.
//
// The directory check IS restated, and only that one: the walk hands this
// function a great many folder rows, and the type test is one comparison
// against a function call that would otherwise cross a package boundary for
// every directory on the storage.
//
// Best-effort by contract — an enqueue failure is logged inside the scanner
// and never reaches the run, so a sick queue cannot fail a sync pass.
func (s *storageSyncer) enqueueScan(ctx context.Context, n *model.Node) {
	if s.avScan == nil || n == nil || n.Type != model.NodeTypeFile {
		return
	}
	s.avScan(ctx, n)
}

// reconcileTrash puts right anything LIVE inside `.filex-trash/`.
//
// Nothing may be live in there, and until this release two kinds of row were.
// Both were minted by the walk, which used to descend into the trash bucket
// with no idea what it was:
//
//   - a real trashed item that the walk UN-DELETED, because it found the
//     object at the row's own retagged key and read "the catalogue does not
//     know about this file". That is the deletion undoing itself, and for a
//     quarantined file it is the security control expiring. It is put back:
//     soft-deleted, keeping the retagged path and the original path in
//     storage_key, so trash restore and retention purge work on it again.
//
//   - a row the walk CREATED for the trash's own bytes (the bucket directory,
//     and the contents of a trashed folder, which already have retagged rows
//     of their own). Those are duplicates that also carry a search-index
//     document each, since the walk indexes what it creates. They are dropped
//     outright, bytes untouched -- the retagged row is the one that owns them.
//     ⚠ The bucket row is the dangerous one, and only becomes dangerous now
//     that the walk skips the trash: unseen, the tombstone pass soft-deletes
//     it, and it lands in the trash listing as a DIRECTORY entry whose path is
//     `/.filex-trash`. Purging that runs purgeDirDescendants over the
//     `/.filex-trash/` prefix -- every trashed row on the storage -- and then
//     asks the driver to delete the directory itself.
//
// ⚠ The distinction is storage_key. A trashed row keeps the path the file came
// from; a row the walk minted in the trash points at itself. Hard-deleting the
// first kind would destroy a restorable deletion; soft-deleting the second
// would put a phantom in the trash listing whose purge would delete the trash
// directory itself.
//
// Best-effort: it never fails the run. On a healthy install it finds nothing.
func (s *storageSyncer) reconcileTrash(ctx context.Context) {
	rows, err := s.store.ListLiveNodesInTrash(ctx, s.storage.ID, trash.Prefix)
	if err != nil || len(rows) == 0 {
		if err != nil {
			slog.Warn("sync: trash reconcile query",
				slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
		}
		return
	}
	for _, n := range rows {
		revived := n.StorageKey != "" && !trash.IsTrashPath(n.StorageKey)
		if revived {
			if err := s.store.SoftDeleteNode(ctx, n.ID); err != nil {
				slog.Warn("sync: could not return a revived node to the trash",
					slog.Int64("node", n.ID), slog.String("err", err.Error()))
				continue
			}
			slog.Warn("sync: returned a node to the trash that an earlier sync had un-deleted",
				slog.Int64("node", n.ID),
				slog.String("path", n.Path),
				slog.String("original_path", n.StorageKey),
				slog.String("storage", s.storage.Name))
		} else {
			if err := s.store.HardDeleteNode(ctx, n.ID); err != nil {
				slog.Warn("sync: could not drop a catalogue row for trash bytes",
					slog.Int64("node", n.ID), slog.String("err", err.Error()))
				continue
			}
			slog.Info("sync: dropped a catalogue row an earlier sync minted for the trash's own bytes",
				slog.Int64("node", n.ID),
				slog.String("path", n.Path),
				slog.String("storage", s.storage.Name))
		}
		if s.index != nil {
			_ = s.index.DeleteNode(ctx, n.ID)
		}
	}
}

// confirmGone decides whether a node the walk did not see may be moved to
// trash.
//
// ⚠⚠ Absence from a listing is NOT proof that the user's file was deleted, and
// answering it with "move to trash" is how a bug in an unrelated part of filex
// becomes data loss. In issue #16 every staged upload above 8 MiB failed to
// reach S3 (the commit could not sign a non-seekable part body), the node stayed
// in the catalogue with its bytes still in staging, and the next sync run — doing
// exactly what it was told — read "this node is not in the bucket" and trashed
// the file the user had just uploaded and been shown as complete. The upload bug
// is fixed, but the shape recurs on its own: a permissions change that hides a
// prefix, a driver that pages a listing badly, an object restored to a bucket
// out of band. So the tombstone pass now has to be RIGHT about the deletion, not
// merely unable to see the file.
//
// Two questions, both of which must say "yes, it is really gone":
//
//  1. Did filex ever put the bytes there? A node whose transfer_state is not
//     "stored" has never been confirmed on the backend — it is either mid-flight
//     or a failed upload whose bytes are still in staging. The backend not
//     listing it is the EXPECTED state, not evidence of a deletion, and it is
//     never a reason to trash it. (This also closes the race in which a sync run
//     lands between publishing the node and the commit finishing, which would
//     trash a perfectly healthy upload.)
//
//  2. Does the object really not exist? A listing can omit an object for reasons
//     that have nothing to do with deletion. Stat is a direct, cheap second
//     opinion and it only runs for candidates. Only a definite ErrNotFound is
//     taken as deletion; any other error (permissions, timeout, 503) keeps the
//     node, because "I could not check" must never read as "it is gone".
func (s *storageSyncer) confirmGone(ctx context.Context, n *model.Node) bool {
	if n.TransferState != "" && n.TransferState != model.TransferStateStored {
		slog.Info("sync: keeping unstored node out of the tombstone pass",
			slog.Int64("node", n.ID),
			slog.String("path", n.Path),
			slog.String("transfer_state", n.TransferState),
			slog.String("storage", s.storage.Name))
		return false
	}
	if n.Type != model.NodeTypeFile {
		// A directory is an artefact of the listing on most drivers (S3 has no
		// such thing), so there is no object to Stat. The seen_at rule is all
		// there is, and a directory carries no bytes of its own.
		return true
	}
	key := n.StorageKey
	if key == "" {
		key = n.Path
	}
	if _, err := s.driver.Stat(ctx, key); err == nil {
		slog.Warn("sync: listing missed an object that is still there",
			slog.Int64("node", n.ID),
			slog.String("path", n.Path),
			slog.String("storage", s.storage.Name))
		return false
	} else if !errors.Is(err, storage.ErrNotFound) {
		slog.Warn("sync: could not confirm an object is gone, keeping it",
			slog.Int64("node", n.ID),
			slog.String("path", n.Path),
			slog.String("storage", s.storage.Name),
			slog.String("err", err.Error()))
		return false
	}
	return true
}

func (s *storageSyncer) previousSeenCount(ctx context.Context) (int, error) {
	last, err := s.store.GetLastSyncRun(ctx, s.storage.ID)
	if err != nil || last == nil {
		return 0, err
	}
	return last.SeenCount, nil
}

// guardOK returns true if it's safe to delete stale nodes.
//
// Block tombstone pass when seen_count drops more than 30% vs previous run —
// usually a transient backend glitch (network, perms, eventual consistency)
// rather than a real wholesale deletion.
func guardOK(seen, prev int) bool {
	if prev == 0 {
		return true
	}
	threshold := float64(prev) * 0.7
	return float64(seen) >= threshold
}

func decodeJSON(b []byte, out any) error {
	return json.Unmarshal(b, out)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/search"
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
	// ⚠ One run at a time per storage. The poll loop is sequential by itself,
	// but "Scan now" (Worker.Trigger) is a second door: pressed while a poll
	// was still walking a 150K-object bucket, it started a second full walk
	// over the same rows — two runs racing each other's seen_at, each one
	// making the other slower (issue #33). The second caller is told, not
	// queued: the run it wanted is the one already in progress.
	if !s.runMu.TryLock() {
		return ErrRunInProgress
	}
	defer s.runMu.Unlock()
	s.inFlight.Store(true)
	defer s.inFlight.Store(false)

	// ⚠⚠ The scanner attributes NOTHING. Every row it creates or updates is
	// SYSTEM, and that is true however the run was started.
	//
	// The background poller runs on a server-lifetime context with no user, so
	// it was system by accident. `Worker.Trigger` is the other door: the admin
	// "Scan now" button hands its REQUEST context straight through, and a
	// request context carries the admin. Unguarded, one click on Scan therefore
	// stamped the whole bucket — thousands of objects nobody uploaded — as that
	// admin's, and (because the same identity drives quota) billed the lot to
	// them. Measured on a three-file storage before this line existed: a file
	// written straight into the directory came back owned by the admin who
	// pressed the button.
	//
	// It is stripped HERE rather than at the trigger, because it is a property
	// of the scan and not of the caller: finding a file is not putting it there.
	ctx = quotastore.WithActor(quotastore.WithOwner(ctx, 0), 0)

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
	// A backend that can hand over the whole tree in one pass (object stores)
	// is asked once, up front; the walk then reads directories out of memory.
	// Anything else — or a tree too large to hold — is walked directory by
	// directory as before.
	list := dirLister(s.driver.List)
	if idx, ok := s.prefetchTree(ctx); ok {
		list = idx.list
	}
	seen, err := s.walk(ctx, "/", nil, &added, &updated, list)
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

// CatalogueTree catalogues everything under dir on drv exactly as a sync pass
// catalogues a directory: rows the index does not have yet are created (with
// the driver's own size, mime, etag and mtime), indexed and handed to avScan;
// rows it already has are left as the walk leaves them. parent is the row of
// dir itself.
//
// It exists for writes that put a whole subtree on the storage in ONE driver
// call — a same-storage folder copy — and have no per-file signal to mirror
// from. Reusing the walk rather than a second catalogue loop is the point: the
// walk's rules (the trash skip, the encrypted-folder marker ordering, the
// stale-row repair) cannot drift between the two.
//
// ⚠ Unlike RunOnce it does NOT strip the owner from ctx: the rows are billed
// to whoever ctx says wrote them.
func CatalogueTree(ctx context.Context, store db.Store, idx *search.Index,
	avScan func(ctx context.Context, n *model.Node),
	st *model.Storage, drv storage.Driver, dir string, parent *int64) error {
	s := &storageSyncer{store: store, index: idx, avScan: avScan, storage: st, driver: drv, ctx: ctx}
	added, updated := 0, 0
	// ⚠ Always the live listing, never a prefetch: the subtree was written a
	// moment ago by the caller and no snapshot taken before that can hold it.
	_, err := s.walk(ctx, dir, parent, &added, &updated, s.driver.List)
	return err
}

// dirLister answers "what is in directory p" for one walk — the driver's List,
// or a prefetched tree standing in for it.
type dirLister func(ctx context.Context, p string) ([]storage.Object, error)

// treeIndex is a whole subtree fetched in one pass (storage.TreeWalker) and
// grouped by parent directory, so the walk reads each directory out of memory
// instead of asking the backend for it.
type treeIndex map[string][]storage.Object

func (idx treeIndex) list(_ context.Context, p string) ([]storage.Object, error) {
	return idx[path.Clean("/"+p)], nil
}

// TreePrefetchMax bounds how many objects one RunOnce will hold in memory
// from a single-pass listing before it gives up on the shortcut and walks
// directory by directory instead. Two million objects is on the order of a
// few hundred MB, which a server syncing a bucket that size has.
var TreePrefetchMax = 2_000_000

var errTreeTooLarge = errors.New("sync: tree too large to prefetch")

// prefetchTree asks a TreeWalker backend for everything under "/" in one pass
// and returns it grouped by parent. ok is false when the driver cannot, the
// tree is over TreePrefetchMax, or the pass failed — the caller then walks the
// backend the ordinary way, so a shortcut that does not fit never costs a scan.
//
// The trash subtree is dropped here rather than in the walk (which skips it
// anyway): with 30% of a bucket in `.filex-trash/` that is 30% less to hold.
func (s *storageSyncer) prefetchTree(ctx context.Context) (treeIndex, bool) {
	tw, ok := s.driver.(storage.TreeWalker)
	if !ok {
		return nil, false
	}
	idx := treeIndex{}
	n := 0
	started := time.Now()
	err := tw.WalkTree(ctx, "/", func(o storage.Object) error {
		if trash.IsTrashPath(o.Path) {
			return nil
		}
		n++
		if n > TreePrefetchMax {
			return errTreeTooLarge
		}
		parent := path.Dir(path.Clean("/" + o.Path))
		idx[parent] = append(idx[parent], o)
		return nil
	})
	if err != nil {
		slog.Warn("sync: single-pass listing unavailable, walking directory by directory",
			slog.String("storage", s.storage.Name),
			slog.Int("objects", n),
			slog.String("err", err.Error()))
		return nil, false
	}
	slog.Debug("sync: tree prefetched in one pass",
		slog.String("storage", s.storage.Name),
		slog.Int("objects", n),
		slog.Duration("took", time.Since(started)))
	return idx, true
}

// walk recursively lists the storage from `path` downwards. parent is the
// DB id of the parent node (nil at root). list answers each directory —
// the driver, or a tree fetched up front (see prefetchTree).
func (s *storageSyncer) walk(ctx context.Context, p string, parent *int64, added, updated *int, list dirLister) (int, error) {
	objs, err := list(ctx, p)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	/* wiring:e2 — the encrypted-folder marker is catalogued FIRST, before any
	   sibling in this directory. "Is this folder encrypted?" is a DB-ROW
	   lookup (e2e.FindRoot asks for the marker's node), so until that row
	   exists every sibling row created here starts a content-extraction job
	   that reads UnderEncrypted as false, indexes the plaintext and records
	   the fingerprint — permanently, it never retries. Ordering is the
	   driver's: os.ReadDir is sorted and `-` (0x2D) sorts before `.` (0x2E),
	   and object stores promise nothing. walk runs once per directory, so
	   this covers every depth — and the copy mirror, which walks too. */
	for i, o := range objs {
		if o.Name == e2e.MarkerName && o.Kind != storage.KindDirectory {
			objs[0], objs[i] = objs[i], objs[0]
			break
		}
	}
	/* /wiring:e2 */
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
				// A folder row's size is the RECURSIVE total RecomputeFolderSizes
				// caches, never the directory entry's own few kilobytes. RunOnce
				// recomputes right after the walk; CatalogueTree (the copy
				// mirror) does not, so the entry size would stand until then.
				n.Size = 0
			} else {
				n.Type = model.NodeTypeFile
			}
			created, err := s.store.CreateNode(ctx, n)
			wasRepair := false
			if err != nil {
				// ⚠⚠ A LIVE row may already sit at (storage, parent, name)
				// carrying a DIFFERENT path — what a folder move that did not
				// carry its subtree leaves behind (issue #21). The unique
				// index refuses the insert, and the walk used to give up:
				// every pass, for every file under the renamed folder, while
				// the tombstone pass moved the stale rows into the trash. The
				// operator saw `duplicate key value violates unique constraint
				// idx_nodes_storage_parent_name` a hundred times and their
				// files in the bin.
				//
				// That row is the same object by definition — one directory,
				// one name — so the honest repair is to point it at the path
				// the storage actually has, in place, keeping its id, its
				// shares, its comments and its version history.
				if repaired := s.repairStalePath(ctx, parent, obj, hash); repaired != nil {
					created, err, wasRepair = repaired, nil, true
				} else {
					slog.Warn("sync: create node failed", slog.String("path", obj.Path), slog.String("err", err.Error()))
					/* wiring:e2 — a marker that was LISTED but whose row did not
					   land abandons this directory for this pass. Downstream,
					   "no marker row" and "the marker row failed" are the same
					   thing, and carrying on indexes plaintext for good; an
					   uncatalogued folder is repaired by the next pass. */
					if obj.Name == e2e.MarkerName && obj.Kind != storage.KindDirectory {
						slog.Warn("sync: leaving a directory uncatalogued this pass, its encrypted-folder marker row could not be written",
							slog.String("path", p), slog.String("storage", s.storage.Name))
						return count, nil
					}
					/* /wiring:e2 */
					continue
				}
			}
			if wasRepair {
				*updated++
			} else {
				*added++
			}
			count++
			if s.index != nil {
				_ = s.index.IndexNode(ctx, created)
			}
			// A file nobody wrote through filex, catalogued for the first
			// time. This — not the drift branch below — is the first import
			// of an existing storage, and the reason the hook exists.
			s.enqueueScan(ctx, created)
			if obj.Kind == storage.KindDirectory {
				cn, err := s.walk(ctx, obj.Path, &created.ID, added, updated, list)
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
				cn, err := s.walk(ctx, obj.Path, &existing.ID, added, updated, list)
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

// repairStalePath re-homes a live row that already holds (storage, parent,
// name) but points at a path the storage no longer has.
//
// It is the recovery half of issue #21: the write path no longer produces
// these rows, and this is what heals the installs that already have them, on
// their next sync, with no operator action.
//
// ⚠ It repairs ONLY a row under the same parent with the same name, which the
// unique index guarantees is at most one and which is the same object the walk
// is looking at. A row anywhere else is somebody else's file and is left
// alone. A row already at this path is not stale and is not touched.
func (s *storageSyncer) repairStalePath(ctx context.Context, parent *int64, obj storage.Object, hash string) *model.Node {
	siblings, err := s.store.ListNodesByParent(ctx, s.storage.ID, parent)
	if err != nil {
		return nil
	}
	for _, sib := range siblings {
		if sib == nil || sib.Name != obj.Name || sib.DeletedAt != nil || sib.Path == obj.Path {
			continue
		}
		if err := s.store.MoveNode(ctx, sib.ID, parent, obj.Name, obj.Path, hash); err != nil {
			slog.Warn("sync: repair stale path failed",
				slog.Int64("node", sib.ID), slog.String("from", sib.Path),
				slog.String("to", obj.Path), slog.String("err", err.Error()))
			return nil
		}
		slog.Info("sync: repaired a row left behind by a folder move",
			slog.Int64("node", sib.ID),
			slog.String("was", sib.Path), slog.String("now", obj.Path),
			slog.String("storage", s.storage.Name))
		fresh, _ := s.store.GetNode(ctx, sib.ID)
		if fresh == nil {
			sib.Path, sib.PathHash = obj.Path, hash
			return sib
		}
		return fresh
	}
	return nil
}

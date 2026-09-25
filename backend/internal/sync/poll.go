package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// RunOnce performs one full sync pass for the storage:
//  1. Open a sync_runs row (status=running)
//  2. Recursively walk the backend, upserting nodes and updating seen_at
//  3. Reconcile the trash bucket: nothing may be live in there. Then drop
//     every row an older walk minted inside `.versions/` and `.thumbs/`.
//  4. Tombstone-pass: any node whose seen_at < runStart is soft-deleted —
//     but only if seen_count >= 0.7 * lastSeenCount (false-positive guard),
//     and never a row inside filex's own trees.
//  5. Close the sync_runs row with the final status.
//
// ⚠ Step 2 skips filex's own trees entirely (`.filex-trash/`, `.versions/`,
// `.thumbs/`), so `seen` does not count their objects. On the first pass after
// the upgrade that stopped counting one of them, a storage where it held more
// than 30% of the objects will trip the step-4 guard once (a warning, and one
// tombstone pass skipped); the next run compares like with like. The same
// holds for the first pass after a scan exclusion (issue #44) that covers more
// than 30% of what the previous pass saw.
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
	c := &walkCounts{}
	// A backend that can hand over the whole tree in one pass (object stores)
	// is asked once, up front; the walk then reads directories out of memory.
	// Anything else — or a tree too large to hold — is walked directory by
	// directory as before.
	list := dirLister(s.driver.List)
	if idx, ok := s.prefetchTree(ctx, "/"); ok {
		list = idx.list
	}
	seen, err := s.walk(ctx, "/", nil, c, list, storage.NewCycleGuard(), 0)
	if err != nil {
		s.finishRun(ctx, run.ID, seen, c.added, c.updated, 0, err)
		return err
	}

	s.reconcileTrash(ctx)
	s.reconcileInternalTrees(ctx)

	deleted := 0
	if guardOK(seen, prevSeen) {
		stale, err := s.store.ListStaleNodes(ctx, s.storage.ID, runStart)
		if err == nil {
			deleted = s.tombstone(ctx, stale)
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
	s.finishRun(ctx, run.ID, seen, c.added, c.updated, deleted, nil)
	// A run whose context died after the walk skipped whatever came after it
	// (the tombstone pass, the folder sizes); it is recorded as aborted and
	// the caller hears why.
	return ctx.Err()
}

// finishRun closes the run's sync_runs row: "ok", "failed" (runErr), or
// "aborted" when the run's own context was cancelled or ran out — a shutdown,
// a storage edit restarting the syncer, the ceiling on a manual scan.
//
// ⚠ Always on a context the run's cancellation cannot reach. It used to close
// the row on the run's own context, so exactly the runs that were cut short
// tried to record their end on a dead context, failed silently, and said
// `running` for ever.
func (s *storageSyncer) finishRun(ctx context.Context, runID int64, seen, added, updated, deleted int, runErr error) {
	status, msg := "ok", ""
	switch {
	case ctx.Err() != nil:
		status, msg = "aborted", interruptedMessage(ctx.Err())
	case runErr != nil:
		status, msg = "failed", runErr.Error()
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.store.FinishSyncRun(fctx, runID, "", seen, added, updated, deleted, status, msg); err != nil {
		slog.Warn("sync: could not close the run's record",
			slog.Int64("run", runID), slog.String("status", status),
			slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
	}
}

// interruptedMessage says why a run stopped before it finished.
func interruptedMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "interrupted: the scan ran past its time limit"
	}
	return "interrupted: the scan was stopped before it finished (server shutdown or storage change)"
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
	s := &storageSyncer{store: store, index: idx, avScan: avScan, storage: st, driver: drv, rule: ruleFor(st, nil), ctx: ctx}
	// ⚠ Always the live listing, never a prefetch: the subtree was written a
	// moment ago by the caller and no snapshot taken before that can hold it.
	_, err := s.walk(ctx, dir, parent, &walkCounts{}, s.driver.List, storage.NewCycleGuard(), 0)
	return err
}

// walkCounts is what one walk did to the catalogue.
type walkCounts struct {
	added, updated int
	// reconciled is the part of updated that settled a staged upload whose
	// bytes were already on the storage (settleTransfer).
	reconciled int
	// partial is set when a directory below the walk's root could not be
	// listed, or was left uncatalogued: the walk carries on past it, and
	// everything under it looks unseen without being gone.
	partial bool
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

// prefetchTree asks a TreeWalker backend for everything under root in one pass
// and returns it grouped by parent. ok is false when the driver cannot, the
// tree is over TreePrefetchMax, or the pass failed — the caller then walks the
// backend the ordinary way, so a shortcut that does not fit never costs a scan.
//
// Whatever the walk would not enter (scanrule: filex's own `.filex-trash/`,
// `.versions/`, `.thumbs/`, and the storage's scan exclusions) is dropped here
// rather than in the walk (which skips it anyway): with 30% of a bucket in the
// trash, or a `node_modules` as large as the project around it, that is that
// much less to hold. ⚠ A one-pass listing cannot prune a prefix, so an object
// store still RETURNS the excluded keys; they are only not kept.
func (s *storageSyncer) prefetchTree(ctx context.Context, root string) (treeIndex, bool) {
	tw, ok := s.driver.(storage.TreeWalker)
	if !ok {
		return nil, false
	}
	idx := treeIndex{}
	n := 0
	started := time.Now()
	err := tw.WalkTree(ctx, root, func(o storage.Object) error {
		if s.rule.Skips(o.Path) {
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
//
// A directory's own entries are applied first, all of them (applyListing: in
// one transaction), and only then are its subfolders walked.
func (s *storageSyncer) walk(ctx context.Context, p string, parent *int64, c *walkCounts, list dirLister, guard *storage.CycleGuard, depth int) (int, error) {
	objs, err := list(ctx, p)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	markerFirst(objs)
	listed, err := s.applyListing(ctx, p, parent, objs, c, func(ctx context.Context, _ []listedEntry, _ *entryBatch) error {
		s.dirListed(ctx, p, len(objs))
		return nil
	})
	if errors.Is(err, errAbandoned) {
		c.partial = true
		return 0, nil
	}
	count := 0
	for _, e := range listed {
		if e.node != nil {
			count++
		}
	}
	if err != nil {
		return count, err
	}
	for _, e := range listed {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		if e.node == nil || !e.descend || !guard.Enter(e.obj, depth+1) {
			continue
		}
		cn, err := s.walk(ctx, e.obj.Path, &e.node.ID, c, list, guard, depth+1)
		if err == nil {
			count += cn
		} else {
			c.partial = true
		}
	}
	return count, nil
}

// catalogueTxEntries bounds one transaction of applyListing. A directory of
// this many entries or fewer is written in one; a bigger one in as many as it
// takes. ⚠ On SQLite the transaction holds the database's only connection:
// every listing, search and upload on the server waits until it commits, and
// a hundred thousand entries in one would stall them for seconds.
var catalogueTxEntries = 500

// errRetryOutsideTx: a write inside a directory's transaction failed. On
// PostgreSQL a failed statement ends the transaction, so the per-entry
// recovery (a row another writer won the insert of, a row a folder move left
// behind) cannot run there; the transaction is rolled back and the entries are
// applied again one statement at a time, where it can.
var errRetryOutsideTx = errors.New("sync: a catalogue write failed inside the directory's transaction")

// entryBatch is what the entries applied in one transaction still owe the rest
// of filex once it commits: rows for the search index, files for the
// antivirus, and rows tombstoned whose documents leave the index.
type entryBatch struct {
	// inTx: the entries run inside Store.WithTx.
	inTx    bool
	index   []*model.Node
	scan    []*model.Node
	unindex []int64
}

// listedEntry is one entry of a directory's listing as applyListing left it.
type listedEntry struct {
	obj storage.Object
	// node is the entry's row; nil when it could not be recorded this pass.
	node *model.Node
	// descend: a folder the walk may enter.
	descend bool
}

// applyListing applies ONE directory's listing to the catalogue, every entry
// through catalogueEntry. It is catalogueEntry's only caller: the full walk
// (and the copy mirror, which walks) and the lazy catalogue's folder reconcile
// both come through here, so neither can batch its writes differently.
//
// ⚠⚠ Issue #70: the entries' writes go in ONE transaction (catalogueTxEntries
// at most each). They used to be one implicit transaction per statement — on
// SQLite an fsync each — and every new row was indexed on its own, one Bleve
// write that waits for the disk per file. Measured on the 100k-file tree
// (docs/LAZY-CATALOGUE.md "Measured") that was the whole cost of cataloguing.
//
// Only after a transaction commits does anything outside the database hear of
// its rows: the search index (one batch) and the antivirus. Both enqueue jobs
// through the job queue, which shares the database but not the transaction —
// on SQLite a call from inside it would wait for ever for the connection the
// transaction holds (internal/db/tx.go).
//
// finish runs inside the LAST transaction, after the last entry, with every
// entry the listing held: the walk records the folder there, the lazy
// reconcile records it and runs its delete pass, so a folder's rows and its
// state row commit together.
//
// A transaction that fails is rolled back and its entries applied again
// without one — the per-entry path exactly as it was before, where losing an
// insert to another writer is recovered row by row.
//
// errAbandoned: the directory's encrypted-folder marker row could not be
// written; nothing more of the directory is recorded this pass.
func (s *storageSyncer) applyListing(ctx context.Context, p string, parent *int64, objs []storage.Object, c *walkCounts,
	finish func(ctx context.Context, listed []listedEntry, b *entryBatch) error) ([]listedEntry, error) {
	var listed []listedEntry
	for start := 0; ; start += catalogueTxEntries {
		end := min(start+catalogueTxEntries, len(objs))
		chunk, last := objs[start:end], end == len(objs)
		var (
			b   *entryBatch
			cc  walkCounts
			got []listedEntry
		)
		apply := func(ctx context.Context, inTx bool) error {
			b, cc, got = &entryBatch{inTx: inTx}, *c, nil
			for _, obj := range chunk {
				if err := ctx.Err(); err != nil {
					return err
				}
				// filex's own trees at the storage root are not part of the
				// catalogue and are not the walk's to reconcile
				// (syspath.Sealed).
				//
				//   - `.filex-trash/`: the rows for everything in there
				//     already exist -- soft-deleted, retagged to the very keys
				//     sitting on the storage -- and the trash service maintains
				//     them (restore, retention purge), never a listing. Walking
				//     in was how a deletion undid itself.
				//   - `.versions/`: snapshots belong to node_versions rows
				//     keyed by the file they version. Walking in minted a
				//     system-owned row for every snapshot folder and file —
				//     counted in the storage's totals, indexed for search —
				//     and, once unseen, the tombstone pass put the folder rows
				//     in the trash in place, where a purge deletes the whole
				//     prefix on the backend: the version history itself.
				//   - `.thumbs/`: a cache, never content.
				//
				// …and nor are the paths the storage's scan exclusions name
				// (issue #44): a matching folder is not listed at all, which
				// is the point — a `.git` or a download client's `incomplete/`
				// costs nothing. scanrule.Rule.Skips is the one question for
				// both.
				if s.rule.Skips(obj.Path) {
					continue
				}
				n, descend, err := s.catalogueEntry(ctx, p, parent, obj, &cc, b)
				if err != nil {
					return err
				}
				got = append(got, listedEntry{obj: obj, node: n, descend: descend})
			}
			if last && finish != nil {
				return finish(ctx, append(listed[:len(listed):len(listed)], got...), b)
			}
			return nil
		}
		err := s.store.WithTx(ctx, func(ctx context.Context) error { return apply(ctx, true) })
		applied := err == nil
		if err != nil && ctx.Err() == nil {
			if !errors.Is(err, errRetryOutsideTx) {
				slog.Warn("sync: a directory's catalogue transaction failed; applying its entries one by one",
					slog.String("path", p), slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
			}
			// Outside a transaction whatever is written stays written, even
			// when the pass stops half way: its rows are owed their hand-offs.
			err, applied = apply(ctx, false), true
		}
		if applied {
			*c = cc
			s.handOff(ctx, b)
			listed = append(listed, got...)
		}
		if err != nil || last {
			return listed, err
		}
	}
}

// handOff gives the search index and the antivirus what one applied batch of
// entries owes them. After the batch committed, never inside it: see
// applyListing.
func (s *storageSyncer) handOff(ctx context.Context, b *entryBatch) {
	if s.index != nil {
		for _, id := range b.unindex {
			_ = s.index.DeleteNode(ctx, id)
		}
		_ = s.index.IndexNodes(ctx, b.index)
	}
	for _, n := range b.scan {
		s.enqueueScan(ctx, n)
	}
}

// markerFirst moves the encrypted-folder marker to the front of a listing.
// Every consumer of a listing calls it once per directory: the walk (so every
// depth, and the copy mirror, which walks too) and the lazy catalogue's
// folder reconcile.
func markerFirst(objs []storage.Object) {
	/* wiring:e2 — the encrypted-folder marker is catalogued FIRST, before any
	   sibling in its directory. "Is this folder encrypted?" is a DB-ROW
	   lookup (e2e.FindRoot asks for the marker's node), so until that row
	   exists every sibling row created here starts a content-extraction job
	   that reads UnderEncrypted as false, indexes the plaintext and records
	   the fingerprint — permanently, it never retries. Ordering is the
	   driver's: os.ReadDir is sorted and `-` (0x2D) sorts before `.` (0x2E),
	   and object stores promise nothing. */
	for i, o := range objs {
		if o.Name == e2e.MarkerName && o.Kind != storage.KindDirectory {
			objs[0], objs[i] = objs[i], objs[0]
			return
		}
	}
	/* /wiring:e2 */
}

// catalogueEntry applies ONE listed entry of directory p to the catalogue —
// what the walk does for every object it sees, minus the descent — and returns
// the entry's row and whether a walk should descend into it. What the search
// index and the antivirus are owed goes into b, for after the commit.
//
// It is the one per-entry rule of the catalogue: the full walk, the copy
// mirror (CatalogueTree) and the lazy catalogue's folder reconcile all come
// through here (applyListing), so a fix to how an object becomes a row cannot
// reach one of them and miss the others. n is nil when the entry could not be
// recorded.
//
// errAbandoned: the whole directory has to be abandoned for this pass (an
// encrypted-folder marker whose row could not be written). errRetryOutsideTx:
// a write failed inside the directory's transaction (see applyListing).
func (s *storageSyncer) catalogueEntry(ctx context.Context, p string, parent *int64, obj storage.Object, c *walkCounts, b *entryBatch) (n *model.Node, descend bool, err error) {
	hash := pathkey.Hash(s.storage.ID, obj.Path)
	if existing, _ := s.store.GetNodeByPath(ctx, s.storage.ID, hash); existing != nil {
		s.refreshEntry(ctx, existing, obj, c, b)
		return existing, existing.Type == model.NodeTypeDirectory, nil
	}
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
	row := &model.Node{
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
	switch obj.Kind {
	case storage.KindDirectory:
		row.Type = model.NodeTypeDirectory
		// A folder row's size is the RECURSIVE total RecomputeFolderSizes
		// caches, never the directory entry's own few kilobytes. RunOnce
		// recomputes right after the walk; CatalogueTree (the copy
		// mirror) does not, so the entry size would stand until then.
		row.Size = 0
	case storage.KindSymlink:
		// ⚠⚠ This branch used to be absent, and the `else` below typed
		// every non-directory NodeTypeFile — symlink rows included. It
		// was not harmless bookkeeping: measured on a root holding a
		// link to a file outside it, the row was created as a file, the
		// antivirus queue was handed it, and the scanner READ the bytes
		// on the other end. Out-of-root content was being virus-scanned,
		// content-indexed, version-tracked and quota-counted, because
		// every one of those gates asks `Type == NodeTypeFile`.
		//
		// After v0.43.0 a driver reports KindSymlink only for something
		// the caller may NOT open — out of the root with following off,
		// broken, or a remote link the driver will not resolve — so the
		// honest type is the one that keeps all four gates shut.
		row.Type = model.NodeTypeSymlink
	default:
		row.Type = model.NodeTypeFile
	}
	created, err := s.store.CreateNode(ctx, row)
	wasRepair := false
	if err != nil && b.inTx {
		// ⚠ Not here. Everything below reads and writes again, and on
		// PostgreSQL the failed INSERT has already ended the transaction.
		return nil, false, errRetryOutsideTx
	}
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
			created, wasRepair = repaired, true
		} else if raced, _ := s.store.GetNodeByPath(ctx, s.storage.ID, hash); raced != nil {
			// ⚠⚠ Somebody else wrote this very row between our lookup and
			// our insert: a folder reconcile of the lazy catalogue — which
			// runs beside a full scan by design — or a write through filex
			// that landed mid-walk. It is the row we were about to create,
			// so carry on with it exactly as if the lookup had found it.
			//
			// Giving up here used to mean NOT DESCENDING into a folder whose
			// row the other writer created: everything below it that the
			// other writer had not catalogued yet was left out of the pass,
			// and a full scan finished with a hole in its catalogue.
			s.refreshEntry(ctx, raced, obj, c, b)
			return raced, raced.Type == model.NodeTypeDirectory, nil
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
				return nil, false, errAbandoned
			}
			/* /wiring:e2 */
			return nil, false, nil
		}
	}
	if wasRepair {
		c.updated++
	} else {
		c.added++
	}
	b.index = append(b.index, created)
	// A file nobody wrote through filex, catalogued for the first
	// time. This — not the drift branch below — is the first import
	// of an existing storage, and the reason the hook exists.
	b.scan = append(b.scan, created)
	return created, obj.Kind == storage.KindDirectory, nil
}

// refreshEntry is catalogueEntry for an entry that already has a row. A row
// whose staged upload never flipped to stored is settled first when the
// object is demonstrably its bytes; then the row is updated if the backend's
// copy drifted from it.
func (s *storageSyncer) refreshEntry(ctx context.Context, existing *model.Node, obj storage.Object, c *walkCounts, b *entryBatch) {
	unstored := isUnstored(existing)
	settled := unstored && s.settleTransfer(ctx, existing, obj)
	drifted := false
	switch {
	case unstored && !settled:
		// ⚠ Seen, and nothing else. An unstored row describes the upload
		// that was COMMITTED, not whatever sits at its key — the version
		// an in-flight overwrite is replacing, a partial write, nothing
		// related. Writing that object's size and time over the row
		// would show the wrong file, and would erase the evidence
		// settleTransfer reads: the next pass would take the wrong
		// object for the upload.
		_ = s.store.TouchNodeSeen(ctx, existing.ID)
	case objectDrift(existing, obj):
		// An object store's listing carries no mime at all. The row's
		// came from sniffing the bytes at upload, and a listing with
		// nothing to say about it must not erase it.
		mime := obj.Mime
		if mime == "" {
			mime = existing.Mime
		}
		if err := s.store.UpdateNodeMeta(ctx, existing.ID, obj.Size, mime, obj.Etag, obj.Mtime); err == nil {
			drifted = true
		}
	default:
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
	if settled {
		c.reconciled++
	}
	if settled || drifted {
		c.updated++
	}
	// ⚠ Only a DRIFTED or a SETTLED file is re-read here. The walk
	// sees every object on every pass, so hanging a scan off "the walk
	// saw it" would re-scan the whole storage every sync interval,
	// forever. Content that has not changed has already been scanned
	// by the pass that first catalogued it — except a settled upload's:
	// the post-transfer hooks that scan it never ran.
	//
	// The row is re-read once and shared by both consumers: `existing`
	// still carries the PRE-drift size, and the scanner's size ceiling
	// has to be applied to the bytes that are actually there.
	if (drifted || settled) && (s.index != nil || s.avScan != nil) {
		if fresh, _ := s.store.GetNode(ctx, existing.ID); fresh != nil {
			b.index = append(b.index, fresh)
			b.scan = append(b.scan, fresh)
		}
	}
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

// reconcileInternalTrees drops every catalogue row sitting in filex's own
// trees other than the trash: `.versions/` and `.thumbs/`.
//
// An older walk descended into them and minted a system-owned row for every
// snapshot folder and file, and for whatever `.thumbs/` held. Nothing maintains
// those rows and nothing should: a snapshot belongs to the node_versions row of
// the file it versions — keyed by THAT file's id, pointing at
// `.versions/<id>/<n>` by key and never at a catalogue row. So the rows only
// ever did harm: counted in the storage's totals, indexed for search and, once
// the walk stopped seeing them, moved into the trash IN PLACE by the tombstone
// pass. A trashed directory row in there is the worst thing the trash can hold:
// purging it deletes its prefix on the backend, which is the version history.
//
// So every such row is hard-deleted — live ones and ones already in the trash
// alike — deepest first (each row is released on its own, never swept away by
// the parent_id cascade), each with its search document.
//
// ⚠ Catalogue only. The backend is never touched, and dropping these rows
// cannot cascade into node_versions, whose rows reference the versioned file.
//
// Runs before the tombstone pass, like reconcileTrash. Best-effort: a failure
// is logged and the pass carries on, because the tombstone pass refuses these
// trees on its own (see tombstone) — a cleanup that did not happen is a cleanup
// deferred to the next pass, never a version history in the trash.
func (s *storageSyncer) reconcileInternalTrees(ctx context.Context) {
	for _, tree := range []string{syspath.Versions, syspath.Thumbs} {
		rows, err := s.store.ListNodesUnder(ctx, s.storage.ID, tree, true)
		if err != nil {
			slog.Warn("sync: internal-tree reconcile query",
				slog.String("tree", tree), slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
			continue
		}
		if len(rows) == 0 {
			continue
		}
		sort.SliceStable(rows, func(i, j int) bool {
			di, dj := strings.Count(rows[i].Path, "/"), strings.Count(rows[j].Path, "/")
			if di != dj {
				return di > dj
			}
			return rows[i].ID > rows[j].ID
		})
		dropped := 0
		for _, n := range rows {
			if err := s.store.HardDeleteNode(ctx, n.ID); err != nil {
				slog.Warn("sync: could not drop a catalogue row inside filex's own tree",
					slog.Int64("node", n.ID), slog.String("path", n.Path), slog.String("err", err.Error()))
				continue
			}
			dropped++
			if s.index != nil {
				_ = s.index.DeleteNode(ctx, n.ID)
			}
		}
		slog.Info("sync: dropped catalogue rows an earlier sync minted inside filex's own tree; the backend was not touched",
			slog.String("tree", tree),
			slog.Int("rows", dropped),
			slog.String("storage", s.storage.Name))
	}
}

// isUnstored reports whether a file row's bytes were never confirmed on the
// storage: a staged upload committed it, and its transfer has not flipped it
// to "stored" — still running, failed, or its bookkeeping was lost.
func isUnstored(n *model.Node) bool {
	return n.Type == model.NodeTypeFile && n.TransferState != "" && n.TransferState != model.TransferStateStored
}

// settleTransfer marks an unstored file row "stored" when its bytes are
// demonstrably on the storage and nothing else will ever say so.
//
// A staged upload flips its row to "stored" in a catalogue write AFTER the
// driver write. When that write failed and the cleanup after it went ahead,
// the row said "staged" for ever with no staging behind it: listed, but every
// read answered 503 STAGING_GONE, every overwrite was refused (the version
// snapshot reads the same way) and the bytes were never scanned. A full scan
// saw the object, updated the row's metadata and never touched
// transfer_state.
//
// Both of these must hold:
//
//   - no staged_uploads session references the row. While one does, the
//     session owns the bytes — in flight, or failed and retryable — and its
//     commit or the sweeper settles it. A lookup that fails is not "none".
//   - model.TransferLanded: the object has the committed size and is not
//     older than the commit.
func (s *storageSyncer) settleTransfer(ctx context.Context, n *model.Node, obj storage.Object) bool {
	if obj.Kind == storage.KindDirectory || !model.TransferLanded(n, obj.Size, obj.Mtime) {
		return false
	}
	sess, err := s.store.GetStagedUploadByNode(ctx, n.ID)
	switch {
	case err == nil && sess != nil:
		return false
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false
	}
	if err := s.store.SetNodeTransferState(ctx, n.ID, model.TransferStateStored); err != nil {
		slog.Warn("sync: could not mark a landed staged upload stored",
			slog.Int64("node", n.ID), slog.String("path", n.Path), slog.String("err", err.Error()))
		return false
	}
	slog.Info("sync: a staged upload's bytes were already on the storage; the row now says stored",
		slog.Int64("node", n.ID),
		slog.String("path", n.Path),
		slog.String("was", n.TransferState),
		slog.String("storage", s.storage.Name))
	return true
}

// tombstone moves the stale candidates that are really gone into the trash
// and returns how many it moved.
//
// ⚠⚠ A row inside one of filex's own trees is NEVER a candidate, whatever
// else went wrong. The walk does not look in there, so such a row is always
// "unseen"; for a directory row confirmGone has no object to Stat and says
// yes; and the trash then holds a folder whose purge deletes its prefix on the
// backend — `.versions/` is every version of every file. The reconcile passes
// that run before this one are what clears those rows up; this refusal is
// what makes their failure harmless.
//
// ⚠⚠ Nor is a row the storage's scan exclusions cover (issue #44), for the
// same reason: the walk does not look there, so "unseen" says nothing about
// it. Such a row was catalogued before its pattern was added, or written
// through filex since; it stays as it is. Trashing it would be worse than
// wrong — a folder row in the trash is purged by deleting its prefix on the
// backend, and the folder is still there.
func (s *storageSyncer) tombstone(ctx context.Context, stale []*model.Node) int {
	b := &entryBatch{}
	deleted := s.tombstoneRows(ctx, stale, b)
	s.handOff(ctx, b)
	return deleted
}

// tombstoneRows is tombstone with the search-index deletions left in b, for a
// caller running inside a transaction (the lazy delete pass): the documents
// leave the index only once the soft deletes have committed.
func (s *storageSyncer) tombstoneRows(ctx context.Context, stale []*model.Node, b *entryBatch) int {
	deleted := 0
	for _, n := range stale {
		if s.rule.Skips(n.Path) {
			continue
		}
		if !s.confirmGone(ctx, n) {
			continue
		}
		if err := s.store.SoftDeleteNode(ctx, n.ID); err == nil {
			deleted++
			b.unindex = append(b.unindex, n.ID)
		}
	}
	return deleted
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

// previousSeenCount is the tombstone guard's baseline: what the last run that
// FINISHED ok saw.
//
// ⚠ Not simply the last run. A failed or aborted run records whatever it had
// counted when it stopped — usually 0 — and as the baseline that switched the
// guard off for the next run: a listing that came back half empty after an
// interrupted scan went straight to the trash.
func (s *storageSyncer) previousSeenCount(ctx context.Context) (int, error) {
	last, err := s.store.GetLastSyncRunByStatus(ctx, s.storage.ID, "ok")
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

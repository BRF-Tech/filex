// Package search — rebuild.go
//
// The self-repairing index.
//
// v0.29.0 (issue #15) added two indexed fields, name_norm and path_norm,
// and shipped without a rebuild: an index written by an older build was
// opened as is, reported `needs_rebuild: true` in the admin stats, and was
// otherwise left alone. The reasoning was sound as far as it went — a
// rebuild started from an EMPTY index, and extracted file content lives
// only in the index (the database holds no copy), so rebuilding on an
// operator's behalf would have traded a recall improvement nobody asked
// for against a content-search blackout nobody was warned about.
//
// The consequence was worse than the cure: the default experience of the
// upgrade, on every existing installation, was "nothing changed". The
// reporter of issue #15 came back and said exactly that, and was right —
// he was measuring an un-rebuilt index.
//
// So the blackout is removed instead of the rebuild. Two things make an
// automatic rebuild safe here:
//
//  1. The replacement index is built ALONGSIDE the live one and swapped in
//     atomically. The old index answers every query until the new one is
//     complete and verified; the swap itself happens under the index write
//     lock, so a concurrent search waits a few milliseconds rather than
//     seeing an empty or closed index. Nothing observes a half-built index.
//
//  2. Extracted text is CARRIED OVER document by document from the live
//     index into the replacement, instead of being dropped and re-derived.
//     The rebuild changes how names are indexed; it has no reason to touch
//     content at all. Files whose bytes actually drifted are re-enqueued
//     for extraction the way any other stale file is.
package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/blevesearch/bleve/v2"

	"github.com/brf-tech/filex/backend/internal/diskfree"
	"github.com/brf-tech/filex/backend/internal/model"
)

// ErrRebuildInProgress is returned when a rebuild is already running.
// Manual (POST /api/admin/search/rebuild) and automatic rebuilds share one
// guard, held on the Index rather than on the HTTP handler — the handler's
// own atomic could only ever see the rebuilds the handler started.
var ErrRebuildInProgress = errors.New("search: rebuild already in progress")

// ErrIndexDisabled is returned when there is no live index to rebuild.
var ErrIndexDisabled = errors.New("search: index disabled")

// ErrNoDiskSpace is returned when the filesystem holding the index cannot
// hold a second copy of it. Failing loudly and continuing to serve the old
// index is the right answer here; filling the disk is not.
var ErrNoDiskSpace = errors.New("search: not enough free disk space to rebuild the index")

// rebuildHeadroom is the multiple of the current index size that must be
// free before a rebuild starts.
//
// Measured, not guessed, and the first guess (1.5) was wrong. On a
// 20 202-document corpus with an 11.4 MB v0.28.0 index:
//
//	unbatched rebuild:  peaked at 102 MB across both directories, and had
//	                    not finished after 55 s
//	batched rebuild:    peaked at 36.2 MB, finished in 3.0 s
//
// Bleve's scorch backend writes a segment per write and merges them
// afterwards, so the in-flight index is much larger than the one it
// settles into (14.9 MB here). 36.2 MB total is ~2.2x the old index in
// ADDITIONAL space; 4x leaves room for a corpus that merges less kindly
// than this one.
const rebuildHeadroom = 4.0

// rebuildMinFree is the floor under the headroom check — a tiny index on a
// full disk is still a rebuild that cannot finish.
const rebuildMinFree = 64 << 20

// rebuildBatchDocs / rebuildBatchBytes size the bulk-load batches.
//
// One Bleve Index() call per document produces one segment per document,
// which the merger then has to chew through: the unbatched rebuild of
// 20 202 documents had not finished after 55 seconds and peaked at 102 MiB
// of index on disk. Batching is the difference between a repair an
// operator never notices and one that is still running when they look.
//
// The byte budget matters as much as the document count: a batch carries
// the extracted text of every document in it, capped at 200 KiB each, so
// 500 text-heavy documents could otherwise hold ~100 MiB in memory.
const rebuildBatchDocs = 500
const rebuildBatchBytes = 8 << 20

// RebuildOptions selects what a rebuild does about file content. The
// metadata half is not optional: every row is rewritten with the current
// document schema, which is the point of the exercise.
type RebuildOptions struct {
	// ReExtract re-enqueues content extraction for EVERY eligible file.
	// This is the manual `?content=1` contract: an operator asking for it
	// has usually just added an extractor or changed the size cap, and
	// wants the text derived again rather than copied.
	ReExtract bool
	// ExtractMissing re-enqueues extraction only for files whose content
	// fingerprint drifted from what the index holds — what the automatic
	// schema repair uses, because it already carried the text over.
	ExtractMissing bool
	// Reason names the caller in the logs ("admin", "schema-upgrade").
	Reason string
}

// pendingPath is where a rebuild builds the replacement index. It sits
// beside the live one, so both live on the same filesystem and the swap is
// a rename rather than a copy.
func pendingPath(path string) string { return path + ".rebuilding" }

// retiredPath is where the live index is moved during the swap, and is
// removed once the replacement is open. Its existence at startup means a
// crash landed in the middle of a swap — see recoverInterrupted.
func retiredPath(path string) string { return path + ".old" }

// recoverInterrupted cleans up after a container that died mid-rebuild.
// Called from Open, before the index is opened.
//
// Two crash windows exist, and they need opposite treatment:
//
//   - Mid-BUILD: a `.rebuilding` directory holding some unknown fraction of
//     the corpus. It is never trustworthy — a half-built index that got
//     swapped in would silently lose files — so it is deleted, and the
//     schema check that follows starts the rebuild over.
//
//   - Mid-SWAP: the live index has been renamed to `.old` and the
//     replacement has not been moved into place yet, so the configured
//     path holds nothing at all. Without this, the next start would create
//     an EMPTY index there and every file would vanish from search. The
//     known-good old index is put back; the replacement is discarded
//     because nothing on disk says whether it was finished.
func recoverInterrupted(path string) {
	retired := retiredPath(path)
	if _, err := os.Stat(retired); err == nil {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(retired, path); err != nil {
				slog.Error("search: could not restore the previous index after an interrupted swap; search will start from an empty index",
					slog.String("from", retired), slog.String("err", err.Error()))
			} else {
				slog.Warn("search: restored the previous index after an interrupted swap",
					slog.String("index", path))
			}
		} else if err := os.RemoveAll(retired); err != nil {
			slog.Warn("search: could not remove the retired index directory",
				slog.String("dir", retired), slog.String("err", err.Error()))
		}
	}
	pending := pendingPath(path)
	if _, err := os.Stat(pending); err == nil {
		if err := os.RemoveAll(pending); err != nil {
			slog.Warn("search: could not remove a half-built index left by an interrupted rebuild",
				slog.String("dir", pending), slog.String("err", err.Error()))
		} else {
			slog.Warn("search: discarded a half-built index left by an interrupted rebuild; it will be rebuilt from scratch",
				slog.String("dir", pending))
		}
	}
}

// Rebuilding reports whether a rebuild is currently running. Surfaced by
// the admin stats endpoint so the UI can say "rebuilding" instead of
// looking broken.
func (i *Index) Rebuilding() bool { return i.rebuilding.Load() }

// AutoRebuildIfStale starts a background self-repair when the index on
// disk was written by an older document schema. Reports whether it
// started.
//
// enabled=false is FILEX_SEARCH_AUTO_REBUILD=0 and is honoured before any
// work starts — no disk touched, no replacement index created.
func (i *Index) AutoRebuildIfStale(store NodeLister, enabled bool) bool {
	if !i.Enabled() || !i.NeedsRebuild() {
		return false
	}
	i.mu.RLock()
	found := i.foundSchema
	i.mu.RUnlock()
	if !enabled {
		slog.Warn("search: the index was built by an older filex and FILEX_SEARCH_AUTO_REBUILD=0 is set; separator-blind and typo-tolerant name matching stay off for existing files",
			slog.String("found_schema", found),
			slog.String("want_schema", indexSchemaVersion),
			slog.String("action", "POST /api/admin/search/rebuild?content=1"))
		return false
	}
	slog.Info("search: the index was built by an older filex; rebuilding it in the background. The current index keeps answering every query until the replacement is ready",
		slog.String("found_schema", found),
		slog.String("want_schema", indexSchemaVersion))
	if err := i.StartRebuild(store, RebuildOptions{ExtractMissing: true, Reason: "schema-upgrade"}); err != nil {
		slog.Warn("search: automatic rebuild did not start", slog.String("err", err.Error()))
		return false
	}
	return true
}

// StartRebuild runs a rebuild in the background and returns as soon as it
// has started (or refuses, with ErrRebuildInProgress).
//
// The goroutine gets context.Background() on purpose. The manual rebuild
// is triggered from an HTTP handler and chi cancels r.Context() the moment
// the handler returns, which would kill the reindex before it processed a
// single row; the same trap applies to the boot context the automatic path
// is started from, which is cancelled on shutdown paths a rebuild has no
// reason to care about.
func (i *Index) StartRebuild(store NodeLister, opts RebuildOptions) error {
	if !i.Enabled() {
		return ErrIndexDisabled
	}
	if !i.rebuilding.CompareAndSwap(false, true) {
		return ErrRebuildInProgress
	}
	go func() {
		// Deferred, so a rebuild that returns early — a failed disk
		// check, a database that went away — cannot leave the flag stuck
		// and lock out every later rebuild.
		defer i.rebuilding.Store(false)
		if err := i.rebuild(context.Background(), store, opts); err != nil {
			slog.Error("search: rebuild failed; the existing index is still serving queries",
				slog.String("reason", opts.Reason), slog.String("err", err.Error()))
			return
		}
	}()
	return nil
}

// Rebuild runs a rebuild synchronously (tests, and any caller that wants
// to wait). Returns ErrRebuildInProgress when one is already running.
func (i *Index) Rebuild(ctx context.Context, store NodeLister, opts RebuildOptions) error {
	if !i.Enabled() {
		return ErrIndexDisabled
	}
	if !i.rebuilding.CompareAndSwap(false, true) {
		return ErrRebuildInProgress
	}
	defer i.rebuilding.Store(false)
	return i.rebuild(ctx, store, opts)
}

// rebuild does the work. The caller owns the rebuilding flag.
func (i *Index) rebuild(ctx context.Context, store NodeLister, opts RebuildOptions) error {
	started := time.Now()
	i.mu.RLock()
	live := i.bleve
	path := i.path
	hook := i.contentHook
	i.mu.RUnlock()
	if live == nil || path == "" {
		return ErrIndexDisabled
	}
	if err := i.ensureHeadroom(path); err != nil {
		return err
	}

	pending := pendingPath(path)
	if err := os.RemoveAll(pending); err != nil {
		return fmt.Errorf("search: rebuild: clear %s: %w", pending, err)
	}
	fresh, err := bleve.New(pending, bleve.NewIndexMapping())
	if err != nil {
		return fmt.Errorf("search: rebuild: create replacement index: %w", err)
	}
	stampSchemaVersion(fresh)

	// From here on every write lands in BOTH indexes, and every document
	// the write path touches is remembered so the reindex loop does not
	// overwrite it with the older row it snapshotted from the database.
	i.mu.Lock()
	i.pending = fresh
	i.mu.Unlock()
	i.beginDirtyTracking()

	swapped := false
	defer func() {
		if swapped {
			return
		}
		i.mu.Lock()
		i.pending = nil
		i.mu.Unlock()
		i.endDirtyTracking()
		_ = fresh.Close()
		if err := os.RemoveAll(pending); err != nil {
			slog.Warn("search: could not remove the abandoned replacement index",
				slog.String("dir", pending), slog.String("err", err.Error()))
		}
	}()

	nodes, err := store.AllNodesForIndex(ctx)
	if err != nil {
		return fmt.Errorf("search: rebuild: list nodes: %w", err)
	}

	written, skipped := 0, 0
	var extract []*model.Node
	chunk := make(map[string]doc, rebuildBatchDocs)
	chunkBytes := 0
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		b := fresh.NewBatch()
		for id, d := range chunk {
			// Re-checked here, not only when the entry was added: a write
			// that landed while this chunk was filling has already put a
			// newer version in the replacement index, and the snapshot row
			// would undo it.
			if i.isDirty(id) {
				skipped++
				written--
				continue
			}
			if err := b.Index(id, d); err != nil {
				return fmt.Errorf("search: rebuild: batch document %s: %w", id, err)
			}
		}
		if err := fresh.Batch(b); err != nil {
			return fmt.Errorf("search: rebuild: write batch: %w", err)
		}
		chunk = make(map[string]doc, rebuildBatchDocs)
		chunkBytes = 0
		return nil
	}

	for _, n := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := strconv.FormatInt(n.ID, 10)
		if i.isDirty(id) {
			// The write path already put a newer version of this node in
			// both indexes while we were listing. Writing the snapshot
			// row now would undo it — including undoing a delete.
			skipped++
			continue
		}
		d := docFor(n)
		// Carry the extracted text over rather than dropping it. This is
		// the line that makes an automatic rebuild acceptable at all.
		d.Content, d.ContentSig = i.liveContent(id)
		chunk[id] = d
		chunkBytes += len(d.Content) + len(d.Path) + len(d.Name)
		written++
		if hook != nil && n.Type == model.NodeTypeFile &&
			(opts.ReExtract || (opts.ExtractMissing && d.ContentSig != ContentFingerprint(n))) {
			extract = append(extract, n)
		}
		if len(chunk) >= rebuildBatchDocs || chunkBytes >= rebuildBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}

	// Verify before swapping. An empty replacement over a non-empty corpus
	// means the build did not do what it claimed, and swapping it in would
	// delete every document from search in one atomic step.
	count, err := fresh.DocCount()
	if err != nil {
		return fmt.Errorf("search: rebuild: count replacement documents: %w", err)
	}
	if written > 0 && count == 0 {
		return fmt.Errorf("search: rebuild: replacement index is empty after writing %d documents; refusing to swap it in", written)
	}

	if err := i.swap(fresh); err != nil {
		return err
	}
	swapped = true
	i.endDirtyTracking()
	if err := os.RemoveAll(retiredPath(path)); err != nil {
		slog.Warn("search: could not remove the retired index directory",
			slog.String("dir", retiredPath(path)), slog.String("err", err.Error()))
	}

	// Extraction is enqueued AFTER the swap so the jobs land in the index
	// that is now live.
	for _, n := range extract {
		hook(ctx, n)
	}

	stats := i.Stats()
	slog.Info("search: rebuilt index is live",
		slog.String("reason", opts.Reason),
		slog.Uint64("documents", count),
		slog.Int("written", written),
		slog.Int("kept_from_live_writes", skipped),
		slog.Int("content_extractions_queued", len(extract)),
		slog.Int64("index_bytes", stats.SizeBytes),
		slog.Duration("took", time.Since(started)))
	return nil
}

// swap makes the replacement index the live one.
//
// It runs under the index write lock, which is what makes it atomic from
// a caller's point of view: a concurrent search blocks for the length of
// two directory renames and a Bleve open, then runs against the new index.
// It never sees a closed index, and never an empty one.
//
// The renames are ordered so that no step destroys the only good copy: the
// live index is moved aside (not deleted), and every failure after that
// puts it back and reopens it.
func (i *Index) swap(fresh bleve.Index) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.pending = nil

	// Close the replacement first: on Windows an open index cannot be
	// renamed, and a half-flushed one should not be swapped in anywhere.
	if err := fresh.Close(); err != nil {
		return fmt.Errorf("search: rebuild: close replacement index: %w", err)
	}
	if err := i.bleve.Close(); err != nil {
		return fmt.Errorf("search: rebuild: close live index: %w", err)
	}
	retired := retiredPath(i.path)
	_ = os.RemoveAll(retired)
	if err := os.Rename(i.path, retired); err != nil {
		i.reopenLocked()
		return fmt.Errorf("search: rebuild: retire live index: %w", err)
	}
	if err := os.Rename(pendingPath(i.path), i.path); err != nil {
		_ = os.Rename(retired, i.path)
		i.reopenLocked()
		return fmt.Errorf("search: rebuild: move replacement into place: %w", err)
	}
	bx, err := bleve.Open(i.path)
	if err != nil {
		_ = os.RemoveAll(i.path)
		_ = os.Rename(retired, i.path)
		i.reopenLocked()
		return fmt.Errorf("search: rebuild: open replacement index: %w", err)
	}
	i.bleve = bx
	// Cleared HERE, not when the rebuild started: until this moment the
	// index answering queries really is the old one, and needs_rebuild
	// must not lie about that.
	i.staleSchema = false
	return nil
}

// reopenLocked reopens the live index after a failed swap closed it. Must
// be called with i.mu held. A failure here leaves the index disabled,
// which is loud in the logs and degrades to the SQL LIKE fallback rather
// than serving from a closed index.
func (i *Index) reopenLocked() {
	bx, err := bleve.Open(i.path)
	if err != nil {
		slog.Error("search: could not reopen the index after a failed swap; falling back to SQL LIKE until restart",
			slog.String("index", i.path), slog.String("err", err.Error()))
		i.bleve = nil
		return
	}
	i.bleve = bx
}

// ensureHeadroom refuses a rebuild the disk cannot hold.
//
// Two indexes exist at once for the length of a rebuild — measured at
// ~1.3x the old index for the same corpus, plus whatever Bleve's segment
// merges need on the way. A probe that cannot answer (unsupported
// platform, permissions) does NOT refuse: a guard that blocks the repair
// because it could not read a number is worse than no guard.
func (i *Index) ensureHeadroom(path string) error {
	probe := i.FreeBytes
	if probe == nil {
		probe = diskfree.Free
	}
	size, err := dirSize(path)
	if err != nil || size <= 0 {
		return nil
	}
	free, err := probe(filepath.Dir(path))
	if err != nil {
		slog.Debug("search: could not measure free disk space; rebuilding anyway",
			slog.String("err", err.Error()))
		return nil
	}
	need := uint64(float64(size)*rebuildHeadroom) + rebuildMinFree
	if free < need {
		return fmt.Errorf("%w: the current index is %d bytes and a rebuild needs about %d bytes free on %s, but only %d are available",
			ErrNoDiskSpace, size, need, filepath.Dir(path), free)
	}
	slog.Info("search: building a replacement index alongside the current one",
		slog.Int64("current_index_bytes", size),
		slog.Uint64("free_bytes", free))
	return nil
}

// liveContent reads the extracted text a document already holds in the
// LIVE index, so the replacement can carry it over.
func (i *Index) liveContent(id string) (content, sig string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.bleve == nil {
		return "", ""
	}
	return storedContent(i.bleve, id)
}

// beginDirtyTracking starts remembering which documents the write path
// touched during a rebuild. See the isDirty check in rebuild.
func (i *Index) beginDirtyTracking() {
	i.dirtyMu.Lock()
	i.dirty = map[string]struct{}{}
	i.dirtyMu.Unlock()
}

// endDirtyTracking stops it and releases the set.
func (i *Index) endDirtyTracking() {
	i.dirtyMu.Lock()
	i.dirty = nil
	i.dirtyMu.Unlock()
}

// markDirty records a document the write path wrote or deleted while a
// rebuild was running.
func (i *Index) markDirty(id string) {
	i.dirtyMu.Lock()
	if i.dirty != nil {
		i.dirty[id] = struct{}{}
	}
	i.dirtyMu.Unlock()
}

func (i *Index) isDirty(id string) bool {
	i.dirtyMu.Lock()
	defer i.dirtyMu.Unlock()
	if i.dirty == nil {
		return false
	}
	_, ok := i.dirty[id]
	return ok
}

// Thumbnail backfill — walks every persisted file node and dispatches the
// thumbnail pipeline so existing instances catch up after deps are added or
// after the cache is rebuilt.
//
// Used by:
//   - `filex thumb backfill` CLI subcommand (synchronous, prints progress)
//   - FILEX_THUMB_BACKFILL_ON_BOOT=once (background, fired from Start())
//
// Both routes share BackfillThumbs to keep the dispatch logic in one place.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// BackfillOptions tunes a backfill run.
type BackfillOptions struct {
	// StorageIDs, when non-empty, restricts the run to specific storages.
	// Empty means "every enabled storage".
	StorageIDs []int64
	// Limit caps the number of files processed across all storages
	// combined. 0 = unlimited.
	Limit int
	// RetryFailed includes nodes whose existing thumbnails row is in
	// state="failed". Without this, failed rows are skipped (so a flaky
	// office doc doesn't block every subsequent run).
	RetryFailed bool
	// RetrySkipped includes nodes whose existing thumbnails row is in
	// state="skipped". Use when the pipeline has gained coverage for
	// kinds that previously skipped (e.g. v0.1.7 generic fallback /
	// audio waveform) — without this, old rows freeze the pipeline.
	RetrySkipped bool
	// Concurrency controls the worker pool size. <=0 → 4.
	Concurrency int
	// ProgressEvery determines how many processed nodes between an
	// OnProgress callback. <=0 disables intermediate notifications.
	ProgressEvery int
	// OnProgress, if non-nil, is invoked with the running counters every
	// ProgressEvery files. CLI uses this to print to stdout; the boot
	// goroutine leaves it nil and lets the slog summary do the work.
	OnProgress func(BackfillStats)
}

// BackfillStats is the counter snapshot emitted via OnProgress and returned
// from BackfillThumbs.
type BackfillStats struct {
	Processed int
	OK        int
	Failed    int
	Skipped   int
	// NotIndexed names the storages this run REFUSED, and why. See
	// ErrNotIndexed.
	NotIndexed []NotIndexedStorage
}

// NotIndexedStorage is one storage a backfill would have reported success on
// while rendering nothing for it.
type NotIndexedStorage struct {
	ID     int64
	Name   string
	Reason string
}

// ErrNotIndexed is returned (wrapped, after every other storage has been
// processed) when at least one targeted storage's catalogue cannot be trusted
// to hold its files.
//
// ⚠⚠ Why a backfill refuses instead of doing what it can. A backfill renders
// thumbnails for NODES — rows in the catalogue — because that is what a
// thumbnail is keyed on. Files written straight onto a storage's backend have
// no node until a sync writes one, and a sync answers 202 and writes them in
// the background. So this command, run against a storage nobody had synced, or
// one whose sync was still going, walked an empty (or half-written) catalogue
// and printed `{processed: 0, ok: 0, failed: 0, skipped: 0}` with exit 0.
// Measured 2026-09-14, and it is how a screenshot run produced a grid with no
// thumbnails while every step it took said "ok". A command that does nothing
// and reports success is worse than one that fails: nobody goes looking.
//
// It cannot simply sync first: the CLI runs beside a live server, and two
// processes syncing one storage into one database is its own bug. So it says
// what is missing and what to do, and exits non-zero.
var ErrNotIndexed = errors.New("thumb backfill: storage catalogue not ready")

// catalogueState is what catalogueGap reads. Narrow on purpose, so the rule
// is testable without a server.
type catalogueState interface {
	GetLastSyncRun(ctx context.Context, storageID int64) (*model.SyncRun, error)
	StorageStats(ctx context.Context, storageID int64) (fileCount int64, totalBytes int64, err error)
}

// catalogueGap says why a storage's catalogue cannot be trusted to hold the
// files on its backend, or "" when it can.
//
//   - a sync is running → the rows it has not reached yet do not exist, so a
//     backfill now leaves exactly those files without a thumbnail (the
//     shots run: `{processed: 22, ok: 22}` and a grid of generic icons);
//   - the last sync was interrupted (`aborted`) → the same half-built
//     catalogue, with nothing left running to finish it;
//   - never synced, the catalogue holds no file, and the backend's root is
//     not empty → there is literally nothing to render.
//
// ⚠ Never synced BUT holding files is not refused: a storage filled through
// uploads has a node for everything it was given, and "run a backfill after
// installing ffmpeg" is exactly this command's job there. It is logged,
// because files placed on its backend directly would still be missed.
func catalogueGap(ctx context.Context, store catalogueState, st *model.Storage, drv storage.Driver) string {
	if run, err := store.GetLastSyncRun(ctx, st.ID); err == nil && run != nil {
		switch run.Status {
		case "running":
			return fmt.Sprintf("a sync is still running (started %s UTC): the files it has not reached "+
				"are not in the catalogue yet, so they would get no thumbnail. Wait for it to finish and run "+
				"backfill again (if the server restarted mid-sync and nothing is running, start a new sync)",
				run.StartedAt.UTC().Format("2006-01-02 15:04:05"))
		case "aborted":
			// ⚠ The same half-built catalogue as a running sync, with nothing
			// left to finish it: the run was cut short (closed as aborted when
			// the server next started). Until this was said out loud, the
			// closed row read "not running" and a backfill went ahead over it.
			return fmt.Sprintf("the last sync was interrupted (started %s UTC) and never finished: the files "+
				"it had not reached are not in the catalogue, so they would get no thumbnail. Start a new sync "+
				"(Storages → Sync, or POST /api/admin/storages/%d/sync), wait for it to finish, then run backfill again",
				run.StartedAt.UTC().Format("2006-01-02 15:04:05"), st.ID)
		}
	}
	if st.LastSyncAt != nil {
		return ""
	}
	files, _, err := store.StorageStats(ctx, st.ID)
	if err != nil || files > 0 {
		if files > 0 {
			slog.Warn("thumb backfill: storage has never been synced — only files uploaded through filex are in the catalogue",
				slog.String("storage", st.Name),
				slog.Int64("files", files),
				slog.String("hint", "files placed on its backend directly get no thumbnail until a sync indexes them"))
		}
		return ""
	}
	if drv == nil {
		return ""
	}
	objs, err := drv.List(ctx, "/")
	if err != nil {
		return ""
	}
	for _, o := range objs {
		if internalBackendEntry(o.Name) {
			continue
		}
		return fmt.Sprintf("it has never been synced: its files are on the backend but not in the "+
			"catalogue, so there is nothing to render thumbnails for. Sync it first (Storages → Sync, "+
			"or POST /api/admin/storages/%d/sync), wait for the sync to finish, then run backfill again", st.ID)
	}
	return "" // an empty storage: nothing to do is the truth
}

// internalBackendEntry reports the backend-root names filex keeps for itself,
// which say nothing about whether the storage holds anybody's files.
//
// ⚠ syspath.IsName — this was the one hand-written copy that DID know
// `.filex-open`, and that is exactly how the others went on missing it since
// open-with shipped (0.29.0) without anyone noticing: there was no single list
// to compare them with.
func internalBackendEntry(name string) bool {
	return syspath.IsName(strings.TrimPrefix(name, "/"))
}

// BackfillThumbs walks every file node in scope and (re)dispatches the
// thumbnail pipeline. Safe to call concurrently with normal operation —
// the pipeline itself is idempotent and uses UpsertThumbnail.
func (s *Server) BackfillThumbs(ctx context.Context, opts BackfillOptions) (BackfillStats, error) {
	if s.pipeline == nil {
		return BackfillStats{}, errors.New("thumb backfill: pipeline unavailable")
	}
	if s.resolver == nil {
		return BackfillStats{}, errors.New("thumb backfill: storage resolver unavailable")
	}

	conc := opts.Concurrency
	if conc <= 0 {
		conc = 4
	}

	// Resolve target storages.
	var targets []*model.Storage
	if len(opts.StorageIDs) > 0 {
		for _, id := range opts.StorageIDs {
			st, err := s.store.GetStorage(ctx, id)
			if err != nil {
				return BackfillStats{}, fmt.Errorf("thumb backfill: storage %d: %w", id, err)
			}
			targets = append(targets, st)
		}
	} else {
		list, err := s.store.ListEnabledStorages(ctx)
		if err != nil {
			return BackfillStats{}, fmt.Errorf("thumb backfill: list storages: %w", err)
		}
		targets = list
	}

	var (
		processed atomic.Int64
		okCnt     atomic.Int64
		failCnt   atomic.Int64
		skipCnt   atomic.Int64
	)

	// Producer: walks nodes; emits to jobs channel.
	jobs := make(chan *model.Node, conc*2)
	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for node := range jobs {
				if ctx.Err() != nil {
					return
				}
				err := s.pipeline.GenerateThumb(ctx, node)
				switch {
				case err == nil:
					okCnt.Add(1)
				case errors.Is(err, thumb.ErrSkipped):
					skipCnt.Add(1)
				default:
					failCnt.Add(1)
					slog.Debug("thumb backfill: node failed",
						slog.Int64("node", node.ID),
						slog.String("path", node.Path),
						slog.String("err", err.Error()))
				}
				p := processed.Add(1)
				if opts.OnProgress != nil && opts.ProgressEvery > 0 && p%int64(opts.ProgressEvery) == 0 {
					opts.OnProgress(BackfillStats{
						Processed: int(p),
						OK:        int(okCnt.Load()),
						Failed:    int(failCnt.Load()),
						Skipped:   int(skipCnt.Load()),
					})
				}
			}
		}()
	}

	// Walk each storage's tree. The walk is cooperative — it stops as
	// soon as the global emitted counter hits opts.Limit. emitted is
	// only incremented from the producer goroutine (this goroutine), so
	// no atomics needed.
	emitted := 0
	walker := &backfillWalker{
		store:        s.store,
		retryFailed:  opts.RetryFailed,
		retrySkipped: opts.RetrySkipped,
		limit:        opts.Limit,
		emitted:      &emitted,
	}
	var notIndexed []NotIndexedStorage
	walkErr := func() error {
		for _, st := range targets {
			// Pre-warm the driver so the pipeline's AttachStorage map is
			// populated. resolver returns the cached driver when present.
			drv, err := s.resolver(st.ID)
			if err != nil {
				slog.Warn("thumb backfill: resolve storage",
					slog.String("name", st.Name),
					slog.String("err", err.Error()))
				continue
			}
			// Refuse, per storage, a catalogue that cannot hold its files yet —
			// and keep going for the others. See ErrNotIndexed.
			if reason := catalogueGap(ctx, s.store, st, drv); reason != "" {
				notIndexed = append(notIndexed, NotIndexedStorage{ID: st.ID, Name: st.Name, Reason: reason})
				slog.Warn("thumb backfill: storage refused",
					slog.String("storage", st.Name),
					slog.String("reason", reason))
				continue
			}
			err = walker.walk(ctx, st.ID, nil, jobs)
			if errors.Is(err, errLimitReached) {
				break
			}
			if err != nil {
				return err
			}
		}
		return nil
	}()
	close(jobs)
	wg.Wait()

	stats := BackfillStats{
		Processed:  int(processed.Load()),
		OK:         int(okCnt.Load()),
		Failed:     int(failCnt.Load()),
		Skipped:    int(skipCnt.Load()),
		NotIndexed: notIndexed,
	}
	if walkErr == nil && len(notIndexed) > 0 {
		parts := make([]string, 0, len(notIndexed))
		for _, n := range notIndexed {
			parts = append(parts, fmt.Sprintf("%q: %s", n.Name, n.Reason))
		}
		walkErr = fmt.Errorf("%w — %s", ErrNotIndexed, strings.Join(parts, "; "))
	}
	return stats, walkErr
}

// backfillWalker holds the per-run walk state. Its fields are owned by the
// single producer goroutine, so the methods are not goroutine-safe — they
// don't need to be.
type backfillWalker struct {
	store interface {
		ListNodesByParent(ctx context.Context, storageID int64, parentID *int64) ([]*model.Node, error)
		GetThumbnail(ctx context.Context, nodeID int64) (*model.Thumbnail, error)
	}
	retryFailed  bool
	retrySkipped bool
	limit        int  // 0 = unlimited
	emitted      *int // pointer so we can mutate across recursive calls
}

// errLimitReached is the sentinel that aborts the walk once limit is hit.
// It's swallowed at the BackfillThumbs caller.
var errLimitReached = errors.New("thumb backfill: limit reached")

func (w *backfillWalker) walk(ctx context.Context, storageID int64, parentID *int64, jobs chan<- *model.Node) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if w.limit > 0 && *w.emitted >= w.limit {
		return errLimitReached
	}
	nodes, err := w.store.ListNodesByParent(ctx, storageID, parentID)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.limit > 0 && *w.emitted >= w.limit {
			return errLimitReached
		}
		if n.DeletedAt != nil {
			continue
		}
		// Skip filex's own directories — the same syspath.Hidden rule the
		// listing projectors (manager.go) apply, so backfill renders for what
		// the UI shows and nothing else. (It used to skip only the trash, so a
		// backfill also rendered thumbnails for version snapshots and for the
		// desktop's open-with working copies, which nobody can ever see.)
		if syspath.Hidden(n.Path) {
			continue
		}
		switch n.Type {
		case model.NodeTypeDirectory:
			id := n.ID
			if err := w.walk(ctx, storageID, &id, jobs); err != nil {
				return err
			}
		case model.NodeTypeFile:
			if !w.shouldProcess(ctx, n) {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case jobs <- copyNode(n):
				*w.emitted++
			}
		}
	}
	return nil
}

// shouldProcess returns true when the node deserves a thumbnail dispatch.
//
//   - retryFailed=false: emit when no row exists OR row is "pending" (likely
//     leftover from a crash). Skip "ready" / "skipped" / "failed".
//   - retryFailed=true:  emit when no row exists OR row is "pending" / "failed".
//     Skip "ready" / "skipped".
func (w *backfillWalker) shouldProcess(ctx context.Context, n *model.Node) bool {
	existing, err := w.store.GetThumbnail(ctx, n.ID)
	if err != nil || existing == nil {
		return true
	}
	switch existing.State {
	case "ready":
		return false
	case "skipped":
		return w.retrySkipped
	case "failed":
		return w.retryFailed
	default:
		return true // pending or unknown — re-run.
	}
}

// copyNode returns a shallow copy so the worker goroutine doesn't share
// the same pointer with the next loop iteration's reuse. ListNodesByParent
// already returns fresh pointers per row but defence-in-depth.
func copyNode(n *model.Node) *model.Node {
	cp := *n
	return &cp
}

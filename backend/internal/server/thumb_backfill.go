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
	walkErr := func() error {
		for _, st := range targets {
			// Pre-warm the driver so the pipeline's AttachStorage map is
			// populated. resolver returns the cached driver when present.
			if _, err := s.resolver(st.ID); err != nil {
				slog.Warn("thumb backfill: resolve storage",
					slog.String("name", st.Name),
					slog.String("err", err.Error()))
				continue
			}
			err := walker.walk(ctx, st.ID, nil, jobs)
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
		Processed: int(processed.Load()),
		OK:        int(okCnt.Load()),
		Failed:    int(failCnt.Load()),
		Skipped:   int(skipCnt.Load()),
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
		// Skip the trash bucket — same heuristic used by projectFileNodes
		// (manager.go) so backfill matches what the UI sees.
		if strings.HasPrefix(n.Path, "/.filex-trash") || strings.HasPrefix(n.Path, ".filex-trash") || n.Name == ".filex-trash" {
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

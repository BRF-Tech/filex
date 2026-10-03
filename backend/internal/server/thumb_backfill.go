// Thumbnail backfill and repair — walks the file nodes in scope and draws the
// ones a selection asks for (thumb.Pipeline.Wanted).
//
// ⚠⚠ ONE walk, three front ends:
//   - `filex thumb backfill` (synchronous, prints progress);
//   - FILEX_THUMB_BACKFILL_ON_BOOT=once (background, fired from Start());
//   - Admin → Tools → Thumbnail repair, an ops job (thumb_repair.go) with
//     progress, cancellation and a result.
//
// Nothing else walks nodes to draw thumbnails: a second copy of this walk is
// how the CLI and the admin tool would come to disagree about which files
// "need repair".
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
	"github.com/brf-tech/filex/backend/internal/pathkey"
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
	// Stale includes ready thumbnails drawn from other content (the file
	// changed after its render), and failures or skips recorded on other
	// content. `filex thumb backfill` turns it on unless --no-stale.
	Stale bool
	// All draws every file in scope, whatever its row says ("Rebuild").
	All bool
	// Path narrows the run to one file, or one folder with everything in it:
	// storage-relative ("/Photos/2024"). "" or "/" is the whole storage. Only
	// with exactly one storage in StorageIDs; a path that has no row answers
	// ErrPathNotFound.
	Path string
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
	// Code is Reason for a client to say in its own language: one of the
	// Gap* constants.
	Code string
}

// Why a storage's catalogue cannot be trusted to hold its files (catalogueGap).
const (
	GapSyncRunning = "sync_running"
	GapSyncAborted = "sync_aborted"
	GapNeverSynced = "never_synced"
)

// ErrPathNotFound: BackfillOptions.Path names nothing in the catalogue.
var ErrPathNotFound = errors.New("thumb backfill: no file or folder at that path")

// ErrPathNeedsOneStorage: a Path was given without exactly one storage.
var ErrPathNeedsOneStorage = errors.New("thumb backfill: a path needs exactly one storage")

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
	_, reason := catalogueGapCode(ctx, store, st, drv)
	return reason
}

// catalogueGapCode is catalogueGap with the reason's code (Gap*) beside it.
func catalogueGapCode(ctx context.Context, store catalogueState, st *model.Storage, drv storage.Driver) (code, reason string) {
	if run, err := store.GetLastSyncRun(ctx, st.ID); err == nil && run != nil {
		switch run.Status {
		case "running":
			return GapSyncRunning, fmt.Sprintf("a sync is still running (started %s UTC): the files it has not reached "+
				"are not in the catalogue yet, so they would get no thumbnail. Wait for it to finish and run "+
				"backfill again (if the server restarted mid-sync and nothing is running, start a new sync)",
				run.StartedAt.UTC().Format("2006-01-02 15:04:05"))
		case "aborted":
			// ⚠ The same half-built catalogue as a running sync, with nothing
			// left to finish it: the run was cut short (closed as aborted when
			// the server next started). Until this was said out loud, the
			// closed row read "not running" and a backfill went ahead over it.
			return GapSyncAborted, fmt.Sprintf("the last sync was interrupted (started %s UTC) and never finished: the files "+
				"it had not reached are not in the catalogue, so they would get no thumbnail. Start a new sync "+
				"(Storages → Sync, or POST /api/admin/storages/%d/sync), wait for it to finish, then run backfill again",
				run.StartedAt.UTC().Format("2006-01-02 15:04:05"), st.ID)
		}
	}
	if st.LastSyncAt != nil {
		return "", ""
	}
	files, _, err := store.StorageStats(ctx, st.ID)
	if err != nil || files > 0 {
		if files > 0 {
			slog.Warn("thumb backfill: storage has never been synced - only files uploaded through filex are in the catalogue",
				slog.String("storage", st.Name),
				slog.Int64("files", files),
				slog.String("hint", "files placed on its backend directly get no thumbnail until a sync indexes them"))
		}
		return "", ""
	}
	if drv == nil {
		return "", ""
	}
	objs, err := drv.List(ctx, "/")
	if err != nil {
		return "", ""
	}
	for _, o := range objs {
		if internalBackendEntry(o.Name) {
			continue
		}
		return GapNeverSynced, fmt.Sprintf("it has never been synced: its files are on the backend but not in the "+
			"catalogue, so there is nothing to render thumbnails for. Sync it first (Storages → Sync, "+
			"or POST /api/admin/storages/%d/sync), wait for the sync to finish, then run backfill again", st.ID)
	}
	return "", "" // an empty storage: nothing to do is the truth
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

// BackfillThumbs walks the file nodes in scope and draws the ones opts
// selects (thumb.Pipeline.Wanted). Safe to call concurrently with normal
// operation — the pipeline itself is idempotent and uses UpsertThumbnail.
func (s *Server) BackfillThumbs(ctx context.Context, opts BackfillOptions) (BackfillStats, error) {
	return s.backfill(ctx, opts, false)
}

// CountBackfill is the number of files BackfillThumbs(opts) would draw right
// now: the same walk, drawing nothing. The repair tool shows it as the total.
func (s *Server) CountBackfill(ctx context.Context, opts BackfillOptions) (int, error) {
	st, err := s.backfill(ctx, opts, true)
	return st.Processed, err
}

func (s *Server) backfill(ctx context.Context, opts BackfillOptions, countOnly bool) (BackfillStats, error) {
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
	scoped := strings.Trim(opts.Path, "/") != ""
	if scoped && len(opts.StorageIDs) != 1 {
		return BackfillStats{}, ErrPathNeedsOneStorage
	}

	var (
		processed atomic.Int64
		okCnt     atomic.Int64
		failCnt   atomic.Int64
		skipCnt   atomic.Int64
	)
	snapshot := func() BackfillStats {
		return BackfillStats{
			Processed: int(processed.Load()),
			OK:        int(okCnt.Load()),
			Failed:    int(failCnt.Load()),
			Skipped:   int(skipCnt.Load()),
		}
	}

	// Producer: walks nodes; emits to jobs channel. Counting draws nothing.
	jobs := make(chan *model.Node, conc*2)
	var wg sync.WaitGroup
	if !countOnly {
		for i := 0; i < conc; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for node := range jobs {
					if ctx.Err() != nil {
						continue // drain: the producer is stopping too
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
						opts.OnProgress(snapshot())
					}
				}
			}()
		}
	}

	sel := thumb.Selection{All: opts.All, Stale: opts.Stale, Failed: opts.RetryFailed, Skipped: opts.RetrySkipped}
	// The walk is cooperative — it stops as soon as the emitted counter hits
	// opts.Limit. emitted is only touched by this goroutine.
	emitted := 0
	walker := &backfillWalker{
		store: s.store,
		want: func(n *model.Node, t *model.Thumbnail) bool {
			return s.pipeline.Wanted(n, t, sel)
		},
		limit:   opts.Limit,
		emitted: &emitted,
		emit: func(ctx context.Context, n *model.Node) error {
			if countOnly {
				processed.Add(1)
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case jobs <- copyNode(n):
				return nil
			}
		},
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
			if code, reason := catalogueGapCode(ctx, s.store, st, drv); reason != "" {
				notIndexed = append(notIndexed, NotIndexedStorage{ID: st.ID, Name: st.Name, Reason: reason, Code: code})
				if !countOnly {
					slog.Warn("thumb backfill: storage refused",
						slog.String("storage", st.Name),
						slog.String("reason", reason))
				}
				continue
			}
			if scoped {
				err = walker.from(ctx, st.ID, opts.Path)
			} else {
				err = walker.walk(ctx, st.ID, nil)
			}
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

	stats := snapshot()
	stats.NotIndexed = notIndexed
	if walkErr == nil && len(notIndexed) > 0 {
		parts := make([]string, 0, len(notIndexed))
		for _, n := range notIndexed {
			parts = append(parts, fmt.Sprintf("%q: %s", n.Name, n.Reason))
		}
		walkErr = fmt.Errorf("%w - %s", ErrNotIndexed, strings.Join(parts, "; "))
	}
	if walkErr == nil && ctx.Err() != nil {
		walkErr = ctx.Err()
	}
	return stats, walkErr
}

// backfillWalker holds the per-run walk state. Its fields are owned by the
// single producer goroutine, so the methods are not goroutine-safe — they
// don't need to be.
type backfillWalker struct {
	store interface {
		ListNodesByParent(ctx context.Context, storageID int64, parentID *int64) ([]*model.Node, error)
		GetNodeByPath(ctx context.Context, storageID int64, pathHash string) (*model.Node, error)
		GetThumbnails(ctx context.Context, ids []int64) (map[int64]*model.Thumbnail, error)
	}
	// want is the selection (thumb.Pipeline.Wanted).
	want    func(n *model.Node, t *model.Thumbnail) bool
	limit   int  // 0 = unlimited
	emitted *int // pointer so we can mutate across recursive calls
	emit    func(ctx context.Context, n *model.Node) error
}

// errLimitReached is the sentinel that aborts the walk once limit is hit.
// It's swallowed at the BackfillThumbs caller.
var errLimitReached = errors.New("thumb backfill: limit reached")

// from walks one path: a file alone, or a folder with everything in it.
func (w *backfillWalker) from(ctx context.Context, storageID int64, rel string) error {
	n, err := w.store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, rel))
	if err != nil || n == nil || n.DeletedAt != nil || syspath.Hidden(n.Path) {
		return ErrPathNotFound
	}
	if n.Type == model.NodeTypeDirectory {
		id := n.ID
		return w.walk(ctx, storageID, &id)
	}
	return w.consider(ctx, []*model.Node{n})
}

func (w *backfillWalker) walk(ctx context.Context, storageID int64, parentID *int64) error {
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
	var files []*model.Node
	for _, n := range nodes {
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
			if err := w.walk(ctx, storageID, &id); err != nil {
				return err
			}
		case model.NodeTypeFile:
			files = append(files, n)
		}
	}
	return w.consider(ctx, files)
}

// consider emits the files of one folder the selection wants, judged against
// their rows read in ONE query (it was one query per file).
func (w *backfillWalker) consider(ctx context.Context, files []*model.Node) error {
	if len(files) == 0 {
		return nil
	}
	ids := make([]int64, len(files))
	for i, n := range files {
		ids[i] = n.ID
	}
	rows, err := w.store.GetThumbnails(ctx, ids)
	if err != nil {
		return err
	}
	for _, n := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if w.limit > 0 && *w.emitted >= w.limit {
			return errLimitReached
		}
		if !w.want(n, rows[n.ID]) {
			continue
		}
		if err := w.emit(ctx, n); err != nil {
			return err
		}
		*w.emitted++
	}
	return nil
}

// copyNode returns a shallow copy so the worker goroutine doesn't share
// the same pointer with the next loop iteration's reuse. ListNodesByParent
// already returns fresh pointers per row but defence-in-depth.
func copyNode(n *model.Node) *model.Node {
	cp := *n
	return &cp
}

// Package sync implements the storage-to-DB sync worker.
//
// Each enabled storage gets one supervisor goroutine that picks the right
// strategy (poll vs fsnotify) and drives a per-run tombstone-guarded
// reconciliation against the storage backend.
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/scanrule"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// DefaultPollInterval is the cadence a polled storage falls back to when its
// own sync_interval_s is unset (or under the 5 s floor). FILEX_SYNC_INTERVAL
// overrides it; see NewWithInterval.
const DefaultPollInterval = 15 * time.Minute

// Worker is the top-level sync supervisor — one instance per server.
type Worker struct {
	store db.Store
	index *search.Index // optional — when set, sync upserts feed Bleve
	// avScan, when set, enqueues an antivirus scan for a file the walk has
	// just catalogued or whose content drifted. See AttachAntivirus.
	avScan func(ctx context.Context, n *model.Node)
	// fallback is the global poll cadence for storages with no interval of
	// their own (FILEX_SYNC_INTERVAL).
	fallback time.Duration

	// emit is the realtime chain the lazy catalogue announces through
	// (AttachEmitter); an *emitterBox, read at announce time.
	emit atomic.Value
	// pairRoots answers which folders of a storage a desktop sync pair
	// mirrors right now (AttachPairRoots); a *pairRootsBox.
	pairRoots atomic.Value

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc // storageID → cancel
	syncers map[int64]*storageSyncer
	stopWg  sync.WaitGroup
	stopped bool
}

// New constructs a Worker with the built-in fallback cadence. Call Start to
// spawn syncers.
func New(store db.Store) *Worker { return NewWithInterval(store, DefaultPollInterval) }

// NewWithInterval is New with an explicit global fallback cadence — what
// FILEX_SYNC_INTERVAL sets.
//
// ⚠ Fallback ONLY: a storage row with its own sync_interval_s still wins. That
// is exactly what docs/CONFIGURATION.md has always claimed the variable did.
// It did not — the value was parsed into config and then read by nothing,
// while the real fallback was a hardcoded 15m literal in loopPoll that
// happened to be the same number, which is why the dead knob was invisible.
func NewWithInterval(store db.Store, fallback time.Duration) *Worker {
	if fallback <= 0 {
		fallback = DefaultPollInterval
	}
	return &Worker{
		store:    store,
		fallback: fallback,
		cancels:  map[int64]context.CancelFunc{},
		syncers:  map[int64]*storageSyncer{},
	}
}

// AttachIndex wires a Bleve index into the worker so each sync upsert
// also lands as a search document. Without this, search only knows
// about whatever the admin's `Rebuild` button has flushed.
func (w *Worker) AttachIndex(idx *search.Index) {
	w.index = idx
}

// AttachAntivirus wires the antivirus enqueue so files that arrive ON a
// storage — rather than through a filex write surface — are scanned too.
//
// A file dropped into a bucket with `aws s3 cp`, written on a mounted disk by
// another process, or simply already there when the storage was added, is
// discovered by the walk, catalogued and content-indexed. Until this hook it
// was never handed to ClamAV, which is precisely the place an operator who
// turned scanning on assumes a scan happens.
//
// fn is queue.AntivirusScanner.EnqueueDiscovered bound to the queue driver: it
// is best-effort by contract (an enqueue failure is logged, never returned)
// and it applies Supports()/Eligible() itself, so directories, oversized files
// and anything under `.filex-trash/` or `.versions/` are refused there rather
// than second-guessed here. nil (no ClamAV binary, or no persistent queue)
// leaves the walk byte for byte as it was.
func (w *Worker) AttachAntivirus(fn func(ctx context.Context, n *model.Node)) {
	w.avScan = fn
}

// Start launches one syncer per enabled storage. ctx is the parent
// shutdown context.
//
// ⚠ First it closes every sync_runs row still open. A run records its own end,
// and a process that stopped in the middle of one — restarted, killed, out of
// memory — never did: its row said `running` for ever, on panels, to the
// thumbnail backfill's "is a sync running?" check, and in the history. Nothing
// of this process can be running yet, so an open row here belongs to one that
// is gone.
//
// This is Start's job and not server.New's on purpose: `filex thumb backfill`
// builds a whole Server beside a live one (it never calls Start), and closing
// rows there would declare the live server's scan dead while it walks.
func (w *Worker) Start(ctx context.Context) error {
	if n, err := w.store.AbortUnfinishedSyncRuns(ctx, AbortedAtStartup); err != nil {
		slog.Warn("sync: could not close the runs a previous process left open", slog.String("err", err.Error()))
	} else if n > 0 {
		slog.Info("sync: closed runs a previous process left open as aborted", slog.Int64("runs", n))
	}
	storages, err := w.store.ListEnabledStorages(ctx)
	if err != nil {
		return fmt.Errorf("sync: list storages: %w", err)
	}
	for _, st := range storages {
		w.startOne(ctx, st)
	}
	slog.Info("sync worker started", slog.Int("count", len(storages)))
	return nil
}

// AddStorage launches a syncer for a newly-created storage row.
func (w *Worker) AddStorage(ctx context.Context, st *model.Storage) error {
	w.startOne(ctx, st)
	return nil
}

// RemoveStorage stops the syncer for a deleted storage.
func (w *Worker) RemoveStorage(id int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if cancel, ok := w.cancels[id]; ok {
		cancel()
		delete(w.cancels, id)
		delete(w.syncers, id)
	}
}

// Trigger forces an immediate sync for a single storage. Returns when the
// run completes or ctx is cancelled.
func (w *Worker) Trigger(ctx context.Context, storageID int64) error {
	w.mu.Lock()
	syncer, ok := w.syncers[storageID]
	w.mu.Unlock()
	if !ok {
		return errors.New("sync: no syncer for storage")
	}
	return syncer.RunOnce(ctx)
}

// Known reports whether a storage has a registered syncer — what Trigger would
// otherwise only discover after the caller had stopped listening.
func (w *Worker) Known(storageID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.syncers[storageID]
	return ok
}

// Stop cancels every syncer and waits for them to exit.
func (w *Worker) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	cancels := make([]context.CancelFunc, 0, len(w.cancels))
	for _, c := range w.cancels {
		cancels = append(cancels, c)
	}
	w.cancels = nil
	w.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	w.stopWg.Wait()
}

func (w *Worker) startOne(parent context.Context, st *model.Storage) {
	if st == nil || !st.Enabled {
		return
	}
	driver, err := storage.Get(st.Driver)
	if err != nil {
		slog.Error("sync: unknown driver", slog.String("driver", st.Driver), slog.String("err", err.Error()))
		return
	}
	cfg := map[string]any{}
	if len(st.ConfigJSON) > 0 {
		_ = jsonToMap(st.ConfigJSON, &cfg)
	}
	if err := driver.Init(parent, cfg); err != nil {
		slog.Error("sync: driver init failed", slog.String("storage", st.Name), slog.String("err", err.Error()))
		return
	}
	ctx, cancel := context.WithCancel(parent)
	syncer := &storageSyncer{
		store:    w.store,
		index:    w.index,
		avScan:   w.avScan,
		storage:  st,
		driver:   driver,
		rule:     ruleFor(st, cfg),
		ctx:      ctx,
		fallback: w.fallback,
		emitter:  w.currentEmitter,
		pairs:    w.currentPairRoots,
		// A full scan of a lazy storage leaves its per-folder state as
		// complete as the catalogue it rebuilt (dirListed).
		recordFolders: st.SyncMode == model.SyncModeLazy,
	}
	w.mu.Lock()
	w.cancels[st.ID] = cancel
	w.syncers[st.ID] = syncer
	w.mu.Unlock()

	w.stopWg.Add(1)
	go func() {
		defer w.stopWg.Done()
		syncer.Loop()
	}()
}

// storageSyncer drives a single Storage's sync loop.
type storageSyncer struct {
	store   db.Store
	index   *search.Index
	avScan  func(ctx context.Context, n *model.Node)
	storage *model.Storage
	driver  storage.Driver
	// rule says which paths the walk does not enter: filex's own trees and
	// the storage's scan exclusions (issue #44). See internal/scanrule.
	rule *scanrule.Rule
	ctx  context.Context
	// fallback is the cadence used when this storage states none of its own.
	fallback time.Duration
	// failures counts CONSECUTIVE failed runs. See noteRun.
	failures int
	// runMu admits one RunOnce at a time; inFlight is the same fact for a
	// reader that must not block (Worker.Running, the admin's 409).
	runMu    sync.Mutex
	inFlight atomic.Bool
	// lazy is the running lazy catalogue of a `lazy` storage (nil
	// otherwise); emitter returns the realtime chain it announces through.
	lazy    lazyPointer
	emitter func() ChangeEmitter
	// pairs lists the folders of this storage a desktop sync pair mirrors
	// (Worker.AttachPairRoots); the lazy catalogue keeps them current.
	pairs func(storageID int64) []string
	// recordFolders: the walk records every folder it lists in the lazy
	// catalogue's per-folder state (dirListed).
	recordFolders bool
}

// AbortedAtStartup is the error recorded on a sync_runs row the worker closes
// at start because the process that opened it is gone.
const AbortedAtStartup = "interrupted: the server stopped during the scan"

// ErrRunInProgress is what RunOnce (and so Worker.Trigger) returns when this
// storage is already being walked. It is not a failure of the run — the run
// the caller wanted is the one in progress — and noteRun does not count it.
var ErrRunInProgress = errors.New("sync: a run is already in progress for this storage")

// Running reports whether a sync run is in flight for the storage right now.
func (w *Worker) Running(storageID int64) bool {
	w.mu.Lock()
	syncer, ok := w.syncers[storageID]
	w.mu.Unlock()
	return ok && syncer.inFlight.Load()
}

// FailureReportThreshold is how many runs in a row must fail before a failure
// is reported as a WARNING (and so reaches the error tracker) rather than
// noted at INFO.
//
// # Why a single failed run is not an error
//
// A poll run reads the backend's listing. Object stores answer a transient
// 503/504 under load, and when the retry budget is spent the run gives up —
// but nothing is lost: the catalogue is simply not refreshed until the next
// tick, minutes later, which normally succeeds.
//
// Measured on fm.example.com: "sync: run failed … ListObjectsV2 … 504" fired 15
// times in six weeks against Hetzner Object Storage, every one of them
// followed by a successful run. Reporting each hiccup buys nothing and costs
// the thing that matters — an error tracker where a real outage stands out.
// Three in a row is roughly 45 minutes of a storage genuinely not answering,
// which IS worth waking up for.
const FailureReportThreshold = 3

// noteRun records the outcome of one run and says how it should be logged.
//
// It deliberately keeps reporting once the threshold is crossed rather than
// warning once: the tracker groups by message, so a sustained outage shows up
// as a rising count on one issue, which is the signal an operator wants.
func (s *storageSyncer) noteRun(err error) {
	if errors.Is(err, ErrRunInProgress) {
		// The tick found the previous run still walking. Not a failure, and
		// not a reason to wait: the next tick asks again.
		slog.Info("sync: tick skipped, the previous run is still in progress",
			slog.String("storage", s.storage.Name))
		return
	}
	if err == nil {
		if s.failures >= FailureReportThreshold {
			slog.Info("sync: recovered",
				slog.String("storage", s.storage.Name),
				slog.Int("after_failures", s.failures))
		}
		s.failures = 0
		return
	}
	s.failures++
	if s.failures >= FailureReportThreshold {
		slog.Warn("sync: run failed",
			slog.String("storage", s.storage.Name),
			slog.Int("consecutive", s.failures),
			slog.String("err", err.Error()))
		return
	}
	slog.Info("sync: run failed, will retry on the next tick",
		slog.String("storage", s.storage.Name),
		slog.Int("consecutive", s.failures),
		slog.String("err", err.Error()))
}

// Loop dispatches to the appropriate strategy.
func (s *storageSyncer) Loop() {
	switch s.storage.SyncMode {
	case model.SyncModeFSNotify:
		s.loopFSNotify()
	case model.SyncModeOnDemand:
		// only Trigger() invocations.
		<-s.ctx.Done()
	case model.SyncModeLazy:
		// folder by folder, as people open them (docs/LAZY-CATALOGUE.md).
		s.loopLazy()
	default:
		// ⚠ The default is the poll loop, which is right for "poll" and for
		// an unset mode — and is a silent lie for anything else. A row
		// written before ValidateSyncMode existed (notably `push`, an enum
		// member that never had a branch) keeps working, but the operator
		// reads their own configuration and believes something else is
		// happening. Say it out loud once, at start, per storage.
		if !s.storage.SyncMode.Implemented() {
			slog.Warn("sync: unsupported sync_mode, falling back to poll",
				slog.String("storage", s.storage.Name),
				slog.String("sync_mode", string(s.storage.SyncMode)),
				slog.String("fallback", string(model.SyncModePoll)))
		}
		s.loopPoll()
	}
}

func (s *storageSyncer) loopPoll() {
	// A storage that states its own cadence wins. Anything under the 5 s floor
	// — including 0, i.e. "not set" — falls back to the configured global,
	// which is what FILEX_SYNC_INTERVAL sets.
	interval := time.Duration(s.storage.SyncIntervalS) * time.Second
	if interval < 5*time.Second {
		interval = s.fallback
		if interval <= 0 {
			interval = DefaultPollInterval
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	s.noteRun(s.RunOnce(s.ctx))
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.noteRun(s.RunOnce(s.ctx))
		}
	}
}

// ruleFor compiles a storage's scan exclusions from its config.
//
// ⚠ A setting that does not compile scans EVERYTHING, loudly. The admin API
// refuses such a value on write, so this is a row written some other way (a
// hand edit, an older client); walking a path the operator wanted skipped
// costs time, while guessing which half of a broken list they meant could
// skip something they did not.
func ruleFor(st *model.Storage, cfg map[string]any) *scanrule.Rule {
	if cfg == nil {
		cfg = map[string]any{}
		if len(st.ConfigJSON) > 0 {
			_ = jsonToMap(st.ConfigJSON, &cfg)
		}
	}
	r, err := scanrule.FromConfig(cfg)
	if err != nil {
		slog.Warn("sync: the storage's scan exclusions do not compile; scanning everything",
			slog.String("storage", st.Name), slog.String("err", err.Error()))
		return &scanrule.Rule{}
	}
	return r
}

// jsonToMap is a tiny helper to avoid pulling in encoding/json all over.
func jsonToMap(b []byte, out *map[string]any) error {
	return decodeJSON(b, out)
}

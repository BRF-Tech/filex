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
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
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
func (w *Worker) Start(ctx context.Context) error {
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

// QueueDepth returns the number of currently active syncer goroutines.
//
// Used by the dashboard handler. A 0 here doesn't mean "no work" — it means
// no storage is enabled or all syncers have stopped (shutdown).
func (w *Worker) QueueDepth() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.syncers)
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
		ctx:      ctx,
		fallback: w.fallback,
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
	ctx     context.Context
	// fallback is the cadence used when this storage states none of its own.
	fallback time.Duration
	// failures counts CONSECUTIVE failed runs. See noteRun.
	failures int
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
	default:
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

// jsonToMap is a tiny helper to avoid pulling in encoding/json all over.
func jsonToMap(b []byte, out *map[string]any) error {
	return decodeJSON(b, out)
}

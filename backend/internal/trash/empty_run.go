package trash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/tenant"
)

// ErrBusy is StartEmpty's answer while another purge sweep — an earlier
// empty, or the nightly retention run — holds the trash.
var ErrBusy = errors.New("trash: a purge is already running")

// EmptyStatus is what an EmptyRun reports: what was asked for, how far it has
// got, and — once Running is false — how it ended. The counters keep the names
// POST /api/admin/trash/empty has always answered with.
type EmptyStatus struct {
	StorageID     int64 `json:"storage_id,omitempty"`
	OlderThanDays int   `json:"older_than_days"`
	Running       bool  `json:"running"`
	// Total and TotalBytes are the rows in the run's scope when it started,
	// and the bytes their files hold. A folder purged together with its
	// contents takes rows the sweep never reaches with it, so Purged can
	// finish below Total; Running false is the end, not Purged == Total.
	Total      int        `json:"total"`
	TotalBytes int64      `json:"total_bytes"`
	Scanned    int        `json:"scanned"`
	Purged     int        `json:"purged"`
	Failed     int        `json:"failed"`
	Bytes      int64      `json:"bytes"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// EmptyRun is one "empty the trash now", running or finished.
type EmptyRun struct {
	key  string
	done chan struct{}
	mu   sync.Mutex
	st   EmptyStatus
}

// Status is a snapshot of the run.
func (r *EmptyRun) Status() EmptyStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

// Done is closed once the run is over and its Status final.
func (r *EmptyRun) Done() <-chan struct{} { return r.done }

func (r *EmptyRun) progress(res PurgeResult) {
	r.mu.Lock()
	r.st.Scanned, r.st.Purged, r.st.Failed, r.st.Bytes = res.Scanned, res.Deleted, res.Failed, res.Bytes
	r.mu.Unlock()
}

func (r *EmptyRun) finish(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.st.Running = false
	r.st.FinishedAt = &now
	if err != nil {
		r.st.Error = err.Error()
	}
}

// StartEmpty begins "empty the trash now" — EmptyOlderThan's purge, same
// arguments, same scope — and returns while it runs. Done says when it ends;
// Status says how far it has got.
//
// ⚠⚠ It exists because the purge used to run inside the request. Every row
// costs a storage delete and a few database writes, so a trash of 61,844
// files needed the better part of an hour, and the proxy in front gave the
// request sixty seconds: a 504, the request's context cancelled, the purge
// abandoned mid-batch — and an admin page that showed nothing, because its
// own HTTP client had already given up at thirty. The run now takes the
// caller's values (the tenant scope decides what it may purge) but not the
// caller's cancellation, so nothing between the browser and the server can
// cut it short.
//
// One sweep at a time (see Service.sweep): while any other holds the trash
// this returns ErrBusy and starts nothing.
func (s *Service) StartEmpty(ctx context.Context, olderThanDays int, storageID int64) (*EmptyRun, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("trash: service not initialised")
	}
	if !s.sweep.TryLock() {
		return nil, ErrBusy
	}
	cutoff := emptyCutoff(olderThanDays)
	total, totalBytes, err := s.tally(ctx, cutoff, storageID)
	if err != nil {
		s.sweep.Unlock()
		return nil, err
	}
	run := &EmptyRun{key: runKey(ctx), done: make(chan struct{}), st: EmptyStatus{
		StorageID:     storageID,
		OlderThanDays: olderThanDays,
		Running:       true,
		Total:         total,
		TotalBytes:    totalBytes,
		StartedAt:     time.Now().UTC(),
	}}
	s.runsMu.Lock()
	if s.runs == nil {
		s.runs = map[string]*EmptyRun{}
	}
	s.runs[run.key] = run
	s.runsMu.Unlock()

	bg := context.WithoutCancel(ctx)
	go func() {
		// Deferred in this order so a caller woken by Done finds the final
		// status AND a free sweep: finish, then unlock, then close.
		defer close(run.done)
		defer s.sweep.Unlock()
		var err error
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("trash: empty stopped: %v", p)
				slog.Error("trash empty panicked", slog.Any("panic", p))
			}
			run.finish(err)
			st := run.Status()
			slog.Info("trash empty finished",
				slog.Int64("storage_id", storageID),
				slog.Int("older_than_days", olderThanDays),
				slog.Int("total", st.Total),
				slog.Int("purged", st.Purged),
				slog.Int("failed", st.Failed),
				slog.Int64("bytes", st.Bytes),
				slog.Duration("took", time.Since(st.StartedAt)),
				slog.String("err", st.Error))
		}()
		_, err = s.purgeOlderThan(bg, cutoff, storageID, run.progress)
	}()
	return run, nil
}

// LastEmpty returns the latest StartEmpty begun under ctx's tenant, running
// or finished. A run is visible to the tenant that started it and to no one
// else: its counts describe that tenant's trash.
func (s *Service) LastEmpty(ctx context.Context) (*EmptyRun, bool) {
	if s == nil {
		return nil, false
	}
	s.runsMu.Lock()
	defer s.runsMu.Unlock()
	r, ok := s.runs[runKey(ctx)]
	return r, ok
}

// runKey names whose run it is: the tenant (provider) of a scoped caller. An
// unscoped caller and a supertenant — which reach every storage — share the
// empty key, which is every single-tenant install.
func runKey(ctx context.Context) string {
	if scope, ok := tenant.FromContext(ctx); ok && scope != nil && !scope.IsSupertenant {
		return "provider:" + strconv.FormatInt(scope.ProviderID, 10)
	}
	return ""
}

// tally counts what a run for ctx, narrowed to storageID, will walk.
func (s *Service) tally(ctx context.Context, cutoff time.Time, storageID int64) (int, int64, error) {
	per, err := s.Store.CountTrashedExpired(ctx, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("trash: count: %w", err)
	}
	var n int
	var b int64
	for sid, t := range per {
		if reaches(ctx, storageID, sid) {
			n += t.Count
			b += t.Bytes
		}
	}
	return n, b, nil
}

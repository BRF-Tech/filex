package trash

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/tenant"
)

// EmptyJob is one "empty the trash now": what it purges, decided when it was
// asked for and carried unchanged to the end — through the ops queue, and
// across a restart that interrupts it.
//
// ⚠⚠ Before is fixed at the moment of asking, never "now" at each step. The
// purge runs in the background for as long as a large trash takes (tens of
// minutes on S3), and a moving cutoff would purge, for good, a file somebody
// deleted by mistake WHILE it ran — a file the confirmation never counted and
// the person believes is still in the trash, waiting to be restored.
type EmptyJob struct {
	// Before: rows deleted before this instant are purged.
	Before time.Time
	// StorageID narrows the run to one storage; 0 is every storage in Reach.
	StorageID int64
	// Reach is the storages the run may touch: nil is every storage (a
	// single-tenant install, the supertenant, the platform), otherwise the
	// caller's tenant's storages — and an empty, non-nil slice is none.
	Reach []int64
}

// ErrNotStarted is RunEmpty's answer when `started` declined: the run was
// cancelled while it waited for its turn, and nothing was purged.
var ErrNotStarted = errors.New("trash: the empty was withdrawn before it started")

// Reach is what a request may empty: nil when the caller is unscoped or the
// supertenant (every storage), otherwise a copy of its tenant's storages —
// non-nil even when that is none, so it can never read as "no restriction".
func Reach(ctx context.Context) []int64 {
	sc, ok := tenant.FromContext(ctx)
	if !ok || sc == nil || sc.IsSupertenant {
		return nil
	}
	return append([]int64{}, sc.StorageIDs...)
}

// scope is the set of storages the job may touch, for the SQL and for the
// check on every row: nil is every storage, an empty slice is none.
func (j EmptyJob) scope() []int64 { return narrow(j.StorageID, j.Reach) }

// narrow combines a named storage with a reach (see EmptyJob).
func narrow(storageID int64, reach []int64) []int64 {
	switch {
	case storageID != 0 && reach != nil:
		if slices.Contains(reach, storageID) {
			return []int64{storageID}
		}
		return []int64{}
	case storageID != 0:
		return []int64{storageID}
	case reach != nil:
		return append([]int64{}, reach...)
	default:
		return nil
	}
}

// reaches reports whether a sweep narrowed to scope (see narrow) may purge a
// trashed row of storage sid. The sweep and the total the run reports both ask
// it, so what is counted is what is purged.
//
// ⚠⚠ Tenancy is decided here, inside the sweep, because the service walks the
// whole trash itself in batches and no handler has a list it could filter.
// POST /api/admin/trash/empty is a legitimate tenant feature ("empty my
// trash"), so it is scoped rather than gated: unscoped it was permanent,
// irreversible destruction of EVERY tenant's deleted files by an admin of any
// one of them. The scope is resolved from the caller when the run is asked
// for (Reach) and travels with the job — the background run has no caller of
// its own to ask.
func reaches(scope []int64, sid int64) bool {
	return scope == nil || slices.Contains(scope, sid)
}

// sweepLock admits one purge sweep at a time — the nightly retention run and
// every "empty the trash now". Two sweeps over the same rows each read a row,
// delete it and release its bytes from the owner's quota, so the second is not
// merely wasted work: it bills the owner back for the bytes twice. A channel
// rather than a mutex so a run that is cancelled while it waits stops waiting.
type sweepLock struct {
	once sync.Once
	ch   chan struct{}
}

func (l *sweepLock) acquire(ctx context.Context) error {
	l.once.Do(func() { l.ch = make(chan struct{}, 1) })
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *sweepLock) release() { <-l.ch }

// Tally counts what the job will walk: the rows in its scope deleted before
// its cutoff, and the bytes their files hold. One grouped read; the same
// reaches() the sweep asks decides what is counted.
func (s *Service) Tally(ctx context.Context, job EmptyJob) (int, int64, error) {
	if s == nil || s.Store == nil {
		return 0, 0, errors.New("trash: service not initialised")
	}
	per, err := s.Store.CountTrashedExpired(ctx, job.Before)
	if err != nil {
		return 0, 0, fmt.Errorf("trash: count: %w", err)
	}
	scope := job.scope()
	var n int
	var b int64
	for sid, t := range per {
		if reaches(scope, sid) {
			n += t.Count
			b += t.Bytes
		}
	}
	return n, b, nil
}

// RunEmpty purges what job names. It waits its turn behind any other sweep
// (the nightly retention run, another empty), then calls started — which may
// decline, when the run was withdrawn while it waited (ErrNotStarted) — and
// reports the running totals to progress after every row.
//
// It ends with ctx: a cancelled run stops at the next row and returns the
// context's error with what it had done by then.
func (s *Service) RunEmpty(ctx context.Context, job EmptyJob, started func() bool, progress func(PurgeResult)) (PurgeResult, error) {
	if s == nil || s.Store == nil {
		return PurgeResult{}, errors.New("trash: service not initialised")
	}
	if err := s.sweep.acquire(ctx); err != nil {
		return PurgeResult{}, err
	}
	defer s.sweep.release()
	if started != nil && !started() {
		return PurgeResult{}, ErrNotStarted
	}
	return s.purgeOlderThan(ctx, job.Before, job.scope(), progress)
}

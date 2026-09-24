package ops

// A delete job's items, several at a time.
//
// Every other job kind still runs its sources one after another, and jobs
// still run one at a time in the order they were queued: claimNext and the
// queue order are untouched. Only the items INSIDE one delete job overlap.
//
// Why deletes: putting a file in the trash on an object store is several round
// trips (the copy into `.filex-trash/`, the delete, the lookups around them),
// and almost all of that time is waiting on the network. One item at a time, a
// delete of tens of thousands of files ran for hours — and because the queue
// has a single worker, every job queued after it (a paste, the transfer of a
// staged upload) waited for all of it.
//
// Why it is safe:
//
//   - each item is its own trash.Put under a fresh trash key and its own row
//     update (DBSync), so two items never write the same key or the same row;
//   - an item nested inside another item of the SAME job would be exactly
//     that collision (the folder's trash move and the file's own), so nested
//     items are dropped before anything starts and counted with the item that
//     takes them along — which also keeps a trashed folder whole in the trash;
//   - the counters are shared, so they are atomic, and the progress row is
//     written under one lock at most about once a second instead of after
//     every item.

import (
	"context"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// DefaultDeleteWorkers is the pool size when nobody sets one
// (config: FILEX_OPS_DELETE_WORKERS).
const DefaultDeleteWorkers = 4

// SetDeleteWorkers sets how many items of one delete job are trashed at the
// same time. Below 1 means 1. Call before Run.
func (s *Service) SetDeleteWorkers(n int) {
	if n < 1 {
		n = 1
	}
	s.deleteWorkers = n
}

// runDeletes trashes a delete job's items with a bounded pool, updates
// op.Done and op.Failed, and returns the last error seen (the job's error
// message when anything failed).
func (s *Service) runDeletes(ctx context.Context, drv, dstDrv storage.Driver, op *Op) error {
	top, nested := topMostSources(op.Sources)
	workers := s.deleteWorkers
	if workers < 1 {
		workers = 1
	}
	if workers > len(top) {
		workers = len(top)
	}

	var done, failed atomic.Int64
	var errMu sync.Mutex
	var lastErr error
	gate := &progressGate{every: time.Second, now: time.Now}
	report := func() {
		gate.maybe(func() {
			_, _ = s.db.ExecContext(ctx, s.q(`UPDATE pending_ops SET done=?, failed=? WHERE id=?`),
				done.Load(), failed.Load(), op.ID)
		})
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				// The item plus everything it takes along with it.
				weight := int64(1 + nested[i])
				if err := s.runOne(ctx, drv, dstDrv, op, top[i]); err != nil {
					failed.Add(weight)
					errMu.Lock()
					lastErr = err
					errMu.Unlock()
					slog.Warn("ops: step failed",
						slog.Int64("op", op.ID),
						slog.String("kind", op.Kind),
						slog.String("src", top[i]),
						slog.String("err", err.Error()))
				} else {
					done.Add(weight)
				}
				report()
			}
		}()
	}
	for i := range top {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	op.Done, op.Failed = int(done.Load()), int(failed.Load())
	return lastErr
}

// topMostSources drops every source that lies inside another source of the
// same list (or repeats one), and says how many were dropped under each one
// kept. The kept sources keep their original spelling and order.
//
// Paths are compared normalised (normOpPath), so "/docs/", "docs" and
// "docs//a.txt" agree. A name that merely starts like a folder ("docs2",
// "docs.txt") is not inside it. The storage root is never treated as a folder
// that takes everything along: it cannot be trashed, and the other items must
// still be tried on their own.
func topMostSources(srcs []string) ([]string, []int) {
	norm := make([]string, len(srcs))
	for i, s := range srcs {
		norm[i] = normOpPath(s)
	}
	// Shallowest first, so a folder is settled before anything inside it.
	order := make([]int, len(srcs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return strings.Count(norm[order[a]], "/") < strings.Count(norm[order[b]], "/")
	})

	owner := make([]int, len(srcs)) // -1: kept; otherwise the index it is inside
	kept := map[string]int{}
	for _, i := range order {
		owner[i] = -1
		if k, ok := kept[norm[i]]; ok { // the same source again
			owner[i] = k
			continue
		}
		for q := parentOf(norm[i]); q != ""; q = parentOf(q) {
			if k, ok := kept[q]; ok {
				owner[i] = k
				break
			}
		}
		if owner[i] == -1 {
			kept[norm[i]] = i
		}
	}

	var top []string
	var nested []int
	pos := map[int]int{}
	for i, s := range srcs {
		if owner[i] == -1 {
			pos[i] = len(top)
			top = append(top, s)
			nested = append(nested, 0)
		}
	}
	for i := range srcs {
		if owner[i] != -1 {
			nested[pos[owner[i]]]++
		}
	}
	return top, nested
}

// parentOf is "" for a top-level name, so the ancestor walk stops before the
// storage root.
func parentOf(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// progressGate lets a progress write through at most once per `every`. The
// first call writes at once; the write itself runs under the gate's lock, so
// two workers can never land their counts out of order.
type progressGate struct {
	mu    sync.Mutex
	every time.Duration
	now   func() time.Time
	last  time.Time
}

func (g *progressGate) maybe(write func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.now()
	if !g.last.IsZero() && t.Sub(g.last) < g.every {
		return
	}
	g.last = t
	write()
}

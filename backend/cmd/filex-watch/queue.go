package main

import (
	"sort"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// queue is what the agent has seen and not yet delivered: folders whose
// contents changed, subtrees to re-read whole, and whether filex should bring
// the whole storage up to date. It is BOUNDED - a disk being rewritten
// wholesale must not grow the agent's memory without end - and past its
// bound it gives the folders up for a resync, which says the same thing in
// one word.
//
// Safe for concurrent use: the watcher adds from its goroutine while the
// sender takes and puts back from another.
type queue struct {
	mu       sync.Mutex
	max      int
	folders  map[string]struct{}
	subtrees map[string]struct{}
	resync   bool
	// first and last are when the current burst started and last grew.
	first, last time.Time
	// overflows counts the times the bound was hit (logged by the agent).
	overflows int
}

// batch is one report's worth of the queue.
type batch struct {
	folders  []string
	subtrees []string
	resync   bool
}

func (b batch) empty() bool { return len(b.folders) == 0 && len(b.subtrees) == 0 && !b.resync }

func newQueue(limit int) *queue {
	return &queue{max: limit, folders: map[string]struct{}{}, subtrees: map[string]struct{}{}}
}

func (q *queue) touch(now time.Time) {
	if q.emptyLocked() {
		q.first = now
	}
	q.last = now
}

func (q *queue) emptyLocked() bool {
	return len(q.folders) == 0 && len(q.subtrees) == 0 && !q.resync
}

// addFolder queues a folder whose direct contents changed.
func (q *queue) addFolder(p string, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.touch(now)
	q.folders[p] = struct{}{}
	q.bound()
}

// addSubtree queues a folder to be re-read whole.
func (q *queue) addSubtree(p string, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.touch(now)
	q.subtrees[p] = struct{}{}
	q.bound()
}

// markResync asks for the whole storage.
func (q *queue) markResync(now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.touch(now)
	q.resync = true
}

// bound gives the folders up for a resync once there are more than max.
func (q *queue) bound() {
	if len(q.folders)+len(q.subtrees) <= q.max {
		return
	}
	q.folders = map[string]struct{}{}
	q.subtrees = map[string]struct{}{}
	q.resync = true
	q.overflows++
}

// due reports whether a report should go now: something is waiting, and its
// burst has been quiet for quiet or has lasted maxWait.
func (q *queue) due(now time.Time, quiet, maxWait time.Duration) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.emptyLocked() {
		return false
	}
	return now.Sub(q.last) >= quiet || now.Sub(q.first) >= maxWait
}

// take removes one report's worth: the resync flag and at most limit folders
// and subtrees together, sorted. What does not fit stays, due at once.
func (q *queue) take(limit int) batch {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := batch{resync: q.resync}
	q.resync = false
	for _, p := range sortedKeys(q.subtrees) {
		if len(b.subtrees) >= limit {
			break
		}
		b.subtrees = append(b.subtrees, p)
		delete(q.subtrees, p)
	}
	for _, p := range sortedKeys(q.folders) {
		if len(b.folders)+len(b.subtrees) >= limit {
			break
		}
		b.folders = append(b.folders, p)
		delete(q.folders, p)
	}
	return b
}

// putBack returns a batch that could not be delivered, merged with whatever
// came in meanwhile, inside the bound.
func (q *queue) putBack(b batch, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if b.empty() {
		return
	}
	q.touch(now)
	q.resync = q.resync || b.resync
	for _, p := range b.folders {
		q.folders[p] = struct{}{}
	}
	for _, p := range b.subtrees {
		q.subtrees[p] = struct{}{}
	}
	q.bound()
}

// takeOverflows reports how many times the bound was hit since the last call.
func (q *queue) takeOverflows() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := q.overflows
	q.overflows = 0
	return n
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reportLimit is how many folders one report carries (watchproto.MaxFolders).
const reportLimit = watchproto.MaxFolders

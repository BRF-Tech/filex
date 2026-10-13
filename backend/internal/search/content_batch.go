package search

// Content writes, grouped, and held while the index is over its memory budget
// (issue #115).
//
// # What went wrong
//
// Every extracted text used to be written on its own: one Bleve Index() call
// per file. scorch turns every write into a segment of its own and waits for
// it to reach the disk, and its merger then rewrites those one-document
// segments again and again as it folds them together. Cataloguing a large
// existing tree enqueues every file at once, so the merger never caught up.
// Measured on 6,000 mixed documents: about 290 GB allocated in seven minutes
// (some 50 MB per document), 300 to 1,000 segment files alive at any moment,
// and a resident size that climbed with the index. On the reporter's RAID
// volume it reached 63 GB and took the host down.
//
// # What it does now
//
//   - Writes are grouped. A content write joins a queue; one writer drains it
//     into a single Bleve batch (up to contentBatchDocs documents or
//     contentBatchBytes of text) and answers every caller with that batch's
//     result. The caller still returns only once its document is in the
//     index, so a queue job is acknowledged after the write, as before.
//   - Writes wait for room. While the process holds more memory than its
//     baseline plus the search budget (FILEX_SEARCH_MEMORY_PERCENT of the
//     memory filex was given, see internal/sysmem), a new content write and a
//     rebuild batch wait, and the merges already running finish and give the
//     memory back. Nothing is dropped and nothing is killed.

import (
	"context"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blevesearch/bleve/v2"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/sysmem"
)

const (
	// contentBatchDocs and contentBatchBytes bound one grouped write.
	contentBatchDocs  = 64
	contentBatchBytes = 8 << 20
	// contentLinger is how long the writer waits for company before it
	// writes a batch: long enough for the extraction workers finishing at the
	// same time to share a segment, short enough not to be noticed.
	contentLinger = 25 * time.Millisecond

	// roomPoll is how often a held write looks at the memory again;
	// roomCollectEvery how often, while it waits, the garbage is collected
	// and handed back (with writes held, nothing else would trigger it).
	roomPoll         = 250 * time.Millisecond
	roomCollectEvery = 5 * time.Second
)

// roomMaxWait lets one write through after this long, so memory that
// something other than search is holding slows indexing to a trickle instead
// of stopping it for good. A variable so a test can shorten it.
var roomMaxWait = time.Minute

// contentWrite is one document waiting to be written.
type contentWrite struct {
	id   string
	doc  doc
	done chan error
}

// contentQueue is the shared queue the content writer drains.
type contentQueue struct {
	mu      sync.Mutex
	pending []*contentWrite
	running bool
	// beforeDrain, when set, runs before the writer takes a batch: a test's
	// hook to know every writer has queued, instead of guessing with a clock.
	beforeDrain func()
}

// memoryGate is the search budget.
type memoryGate struct {
	// limit is the memory, in bytes, the process may hold before content
	// writes wait: its baseline plus the budget. 0 = no budget.
	limit atomic.Uint64
	// inUse reads the memory the process holds (sysmem.InUse; tests swap it).
	inUse func() uint64
	// collect hands garbage back while writes are held (tests swap it).
	collect func()
	// holding is set while a write is held, so the pause is logged once.
	holding atomic.Bool
	// onHold and onRelease, when set, run as a write starts being held and as
	// it is let go: tests synchronise on them rather than on a clock, which a
	// loaded machine breaks.
	onHold, onRelease func()
}

// SetMemoryBudget sets the search budget: content writes and rebuild batches
// wait while the process holds more than its memory right now (its baseline,
// taken before indexing starts) plus budget bytes. 0 removes the budget.
func (i *Index) SetMemoryBudget(budget uint64) {
	if budget == 0 {
		i.mem.limit.Store(0)
		return
	}
	i.mem.limit.Store(i.memInUse() + budget)
}

// MemoryLimit is the memory, in bytes, the process may hold before content
// writes wait (its baseline plus the budget); 0 when there is no budget.
func (i *Index) MemoryLimit() uint64 { return i.mem.limit.Load() }

func (i *Index) memInUse() uint64 {
	if i.mem.inUse != nil {
		return i.mem.inUse()
	}
	return sysmem.InUse()
}

// waitForRoom holds the caller while the process is over the search budget.
// It returns ctx's error if the caller gives up first.
func (i *Index) waitForRoom(ctx context.Context) error {
	limit := i.mem.limit.Load()
	if limit == 0 || i.memInUse() <= limit {
		return nil
	}
	collect := i.mem.collect
	if collect == nil {
		collect = debug.FreeOSMemory
	}
	if i.mem.holding.CompareAndSwap(false, true) {
		slog.Info("search: over the memory budget, indexing waits for the merges to finish",
			slog.Uint64("in_use_mb", i.memInUse()>>20), slog.Uint64("limit_mb", limit>>20))
		defer i.mem.holding.Store(false)
	}
	collect()
	if i.mem.onRelease != nil {
		defer i.mem.onRelease()
	}
	if i.mem.onHold != nil {
		i.mem.onHold()
	}
	start, lastCollect := time.Now(), time.Now()
	t := time.NewTicker(roomPoll)
	defer t.Stop()
	for i.memInUse() > limit {
		if time.Since(start) >= roomMaxWait {
			slog.Warn("search: still over the memory budget after a minute; letting one write through",
				slog.Uint64("in_use_mb", i.memInUse()>>20), slog.Uint64("limit_mb", limit>>20))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if time.Since(lastCollect) >= roomCollectEvery {
			collect()
			lastCollect = time.Now()
		}
	}
	return nil
}

// IndexNodeContent updates the node's document with extracted content. The
// metadata fields are re-supplied from n (the authoritative row) so the
// content update never clobbers them — Bleve has no partial update, the
// whole doc is replaced. Metadata indexing stays synchronous elsewhere;
// this lands later, from the content_index queue job.
//
// It returns once the document is in the index, written together with
// whatever other content writes were waiting (see the top of this file).
func (i *Index) IndexNodeContent(ctx context.Context, n *model.Node, content string) error {
	if !i.Enabled() {
		return nil
	}
	if err := i.waitForRoom(ctx); err != nil {
		return err
	}
	d := docFor(n)
	d.Content = content
	d.ContentSig = ContentFingerprint(n)
	w := &contentWrite{id: strconv.FormatInt(n.ID, 10), doc: d, done: make(chan error, 1)}

	i.cq.mu.Lock()
	i.cq.pending = append(i.cq.pending, w)
	if !i.cq.running {
		i.cq.running = true
		go i.writeContentLoop()
	}
	i.cq.mu.Unlock()

	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		// The write still lands; only the caller stops waiting for it.
		return ctx.Err()
	}
}

// writeContentLoop drains the content queue, one batch at a time, and ends
// when the queue is empty.
func (i *Index) writeContentLoop() {
	for {
		time.Sleep(contentLinger)
		if i.cq.beforeDrain != nil {
			i.cq.beforeDrain()
		}
		i.cq.mu.Lock()
		if len(i.cq.pending) == 0 {
			i.cq.running = false
			i.cq.mu.Unlock()
			return
		}
		take, size := 0, 0
		for take < len(i.cq.pending) && take < contentBatchDocs && (take == 0 || size < contentBatchBytes) {
			size += len(i.cq.pending[take].doc.Content)
			take++
		}
		batch := i.cq.pending[:take:take]
		i.cq.pending = i.cq.pending[take:]
		i.cq.mu.Unlock()

		ids := make([]string, len(batch))
		docs := make([]doc, len(batch))
		for k, w := range batch {
			ids[k], docs[k] = w.id, w.doc
		}
		i.mu.RLock()
		err := i.writeDocsLocked(i.bleve, i.pending, ids, docs)
		i.mu.RUnlock()
		for _, w := range batch {
			w.done <- err
		}
	}
}

// writeDocsLocked writes docs under ids into the live index as ONE batch and,
// while a rebuild runs, into its replacement as well. The caller holds
// i.mu.RLock, so a rebuild's swap cannot close either index under it. A nil
// live index (closed, or never opened) writes nothing.
func (i *Index) writeDocsLocked(bx, pending bleve.Index, ids []string, docs []doc) error {
	if bx == nil || len(ids) == 0 {
		return nil
	}
	b := bx.NewBatch()
	for k := range ids {
		if err := b.Index(ids[k], docs[k]); err != nil {
			return err
		}
	}
	if err := bx.Batch(b); err != nil {
		return err
	}
	if pending != nil {
		i.dualWrite(ids[0], pending, func() error {
			pb := pending.NewBatch()
			for k := range ids {
				i.markDirty(ids[k])
				if err := pb.Index(ids[k], docs[k]); err != nil {
					return err
				}
			}
			return pending.Batch(pb)
		})
	}
	return nil
}

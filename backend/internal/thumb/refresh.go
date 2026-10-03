package thumb

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// NodeGetter is what the refresher reads a node back through before drawing
// it: the listing's copy may be minutes old by then.
type NodeGetter interface {
	GetNode(ctx context.Context, id int64) (*model.Node, error)
}

// Refresher draws, in the background, the thumbnails a listing or the storage
// sync found missing or stale (Assess).
//
// ⚠⚠ Consider never makes its caller wait. It runs inside every listing, and
// a listing is the one request a person is looking at while it loads. The
// queue is bounded and held in memory: at most one entry per node, dropped
// when full. That is enough because the catalogue is the durable record of
// what is stale — the next listing that shows the file asks again. The
// persistent queue would add a database write per file to the listing and
// make nothing more durable (docs/thumbnails.md, Design notes).
type Refresher struct {
	p       *Pipeline
	nodes   NodeGetter
	jobs    chan *model.Node
	mu      sync.Mutex
	queued  map[int64]struct{}
	onDone  func(n *model.Node)
	now     atomic.Value // func() time.Time
	timeout time.Duration
	dropped atomic.Int64
}

// NewRefresher builds a refresher with room for capacity waiting files.
func NewRefresher(p *Pipeline, nodes NodeGetter, capacity int) *Refresher {
	if capacity <= 0 {
		capacity = 4096
	}
	r := &Refresher{
		p:       p,
		nodes:   nodes,
		jobs:    make(chan *model.Node, capacity),
		queued:  map[int64]struct{}{},
		timeout: 2 * time.Minute,
	}
	r.now.Store(time.Now)
	return r
}

// OnRendered is called after every render the refresher ran, whatever it
// ended in. The server announces a derived change to the file's folder from
// here, so an explorer showing it reloads and gets the new picture.
func (r *Refresher) OnRendered(fn func(n *model.Node)) { r.onDone = fn }

// SetClock replaces the clock Assess is asked with (tests).
func (r *Refresher) SetClock(now func() time.Time) { r.now.Store(now) }

func (r *Refresher) clock() time.Time { return r.now.Load().(func() time.Time)() }

// Consider assesses n's thumbnail row t and, when it should be drawn, queues
// a copy of n. It returns the verdict. Nil-safe: a nil refresher (thumbnails
// off) leaves everything.
func (r *Refresher) Consider(n *model.Node, t *model.Thumbnail) Verdict {
	if r == nil || r.p == nil {
		return Leave
	}
	v := r.p.Assess(n, t, r.clock())
	if v != Render {
		return v
	}
	r.mu.Lock()
	if _, dup := r.queued[n.ID]; dup {
		r.mu.Unlock()
		return v
	}
	cp := *n
	cp.Thumb = nil
	select {
	case r.jobs <- &cp:
		r.queued[n.ID] = struct{}{}
	default:
		r.dropped.Add(1)
	}
	r.mu.Unlock()
	return v
}

// ConsiderAll is Consider for many files at once, reading their rows in one
// batched query: what the storage sync hands over after a batch of entries
// drifted (sync.Worker.AttachThumbs). filex's own folders are left alone.
func (r *Refresher) ConsiderAll(ctx context.Context, nodes []*model.Node) {
	if r == nil || r.p == nil || len(nodes) == 0 {
		return
	}
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.Type == model.NodeTypeFile && n.ID > 0 {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := r.p.store.GetThumbnails(ctx, ids)
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n == nil || n.Type != model.NodeTypeFile || n.ID <= 0 || syspath.Hidden(n.Path) {
			continue
		}
		r.Consider(n, rows[n.ID])
	}
}

// Queued reports whether a render of id is waiting or running.
func (r *Refresher) Queued(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.queued[id]
	return ok
}

// Len is the number of renders waiting or running.
func (r *Refresher) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queued)
}

// Run draws queued files with the given number of workers until ctx ends.
func (r *Refresher) Run(ctx context.Context, workers int) {
	if r == nil {
		return
	}
	if workers <= 0 {
		workers = 2
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case n := <-r.jobs:
					r.render(ctx, n)
				}
			}
		}()
	}
	wg.Wait()
	if d := r.dropped.Load(); d > 0 {
		slog.Debug("thumb refresher: files dropped because the queue was full", slog.Int64("dropped", d))
	}
}

// render draws one queued file.
//
// The node is read back first: the listing's copy says what the listing SAW
// (its size, date and etag, which is what the row must record so that the
// next listing agrees), but the file may have been moved, renamed or deleted
// since. Moved: draw it from where it is. Deleted: nothing to draw. And the
// row is assessed again, because another path (an upload, the repair tool)
// may have drawn it in the meantime.
func (r *Refresher) render(ctx context.Context, seen *model.Node) {
	defer func() {
		r.mu.Lock()
		delete(r.queued, seen.ID)
		r.mu.Unlock()
	}()
	cur, err := r.nodes.GetNode(ctx, seen.ID)
	if err != nil || cur == nil || cur.DeletedAt != nil {
		return
	}
	use := seen
	if cur.Path != seen.Path || cur.StorageID != seen.StorageID || cur.Type != seen.Type || cur.Name != seen.Name {
		use = cur
	}
	row, _ := r.p.store.GetThumbnail(ctx, use.ID)
	if r.p.Assess(use, row, r.clock()) != Render {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, r.timeout)
	_ = r.p.GenerateThumb(rctx, use)
	cancel()
	if r.onDone != nil {
		r.onDone(use)
	}
}

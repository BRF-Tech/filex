package sync

import (
	"context"
	"log/slog"
	"path"
	"strings"
	gosync "sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/realtime"
)

// ChangeEmitter is the realtime surface the refresher publishes through. The
// hub (*realtime.Hub) satisfies it.
type ChangeEmitter interface {
	EmitChange(storageID int64, dir string, ev realtime.ChangeEvent)
}

// SizeRefresher recomputes a storage's folder aggregates (RecomputeFolderSizes)
// shortly after its catalogue changed, instead of waiting for the next sync.
//
// Issue #27: after a move the source and destination folders kept their old
// sizes. Folder sizes were only ever recomputed at the end of a sync pass, and
// a move, an upload or a delete through the explorer — or through WebDAV, S3,
// SFTP, NFS — changes the catalogue rows without touching those totals. On a
// storage that syncs rarely (or manually) the wrong size stood for hours.
//
// Every mutation already announces itself to open explorers through the
// realtime emitter, so the refresher sits in front of that emitter (Wrap) and
// is told about every change in one place, whatever surface made it.
//
// ⚠ Debounced WITH a ceiling: a pure trailing debounce never fires while a
// stream keeps arriving (filex lesson #84) — a 5000-file upload would show the
// old sizes until its very end. `quiet` is the gap that ends a burst, `maxWait`
// the longest a burst may postpone a recompute.
//
// ⚠ The listing refresh the refresher sends goes to the INNER emitter, never
// through Wrap: otherwise every recompute would schedule the next one.
type SizeRefresher struct {
	recompute func(ctx context.Context, storageID int64) error
	emit      ChangeEmitter
	quiet     time.Duration
	maxWait   time.Duration
	timeout   time.Duration

	mu      gosync.Mutex
	pending map[int64]*sizeBurst
	running map[int64]bool
	rerun   map[int64]map[string]struct{}
}

type sizeBurst struct {
	first time.Time
	timer *time.Timer
	dirs  map[string]struct{}
}

// NewSizeRefresher builds a refresher over recompute (normally
// RecomputeFolderSizes bound to the store). emit may be nil (no listing
// refresh is sent then).
func NewSizeRefresher(recompute func(ctx context.Context, storageID int64) error, emit ChangeEmitter, quiet, maxWait time.Duration) *SizeRefresher {
	return &SizeRefresher{
		recompute: recompute,
		emit:      emit,
		quiet:     quiet,
		maxWait:   maxWait,
		timeout:   5 * time.Minute,
		pending:   map[int64]*sizeBurst{},
		running:   map[int64]bool{},
		rerun:     map[int64]map[string]struct{}{},
	}
}

// Touch records that dir in storageID changed and schedules a recompute.
func (r *SizeRefresher) Touch(storageID int64, dir string) {
	if r == nil || storageID <= 0 {
		return
	}
	dir = cleanDir(dir)
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running[storageID] {
		// A recompute is walking this storage right now and may already have
		// read the rows this change touched: run once more when it ends.
		set := r.rerun[storageID]
		if set == nil {
			set = map[string]struct{}{}
			r.rerun[storageID] = set
		}
		set[dir] = struct{}{}
		return
	}

	now := time.Now()
	b := r.pending[storageID]
	if b == nil {
		b = &sizeBurst{first: now, dirs: map[string]struct{}{dir: {}}}
		b.timer = time.AfterFunc(r.quiet, func() { r.fire(storageID) })
		r.pending[storageID] = b
		return
	}
	b.dirs[dir] = struct{}{}
	wait := r.quiet
	if left := b.first.Add(r.maxWait).Sub(now); left < wait {
		wait = left
	}
	if wait < 0 {
		wait = 0
	}
	b.timer.Reset(wait)
}

func (r *SizeRefresher) fire(storageID int64) {
	r.mu.Lock()
	b := r.pending[storageID]
	delete(r.pending, storageID)
	if b == nil || r.running[storageID] {
		r.mu.Unlock()
		return
	}
	r.running[storageID] = true
	r.mu.Unlock()

	dirs := b.dirs
	for {
		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		err := r.recompute(ctx, storageID)
		cancel()
		if err != nil {
			slog.Warn("folder size refresh failed", "storage_id", storageID, "err", err)
		} else {
			r.announce(storageID, dirs)
		}

		r.mu.Lock()
		more := r.rerun[storageID]
		delete(r.rerun, storageID)
		if len(more) == 0 {
			delete(r.running, storageID)
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		dirs = more
	}
}

// announce tells explorers showing a changed folder, or any folder above it,
// to reload: an aggregate changes all the way up to the storage root.
func (r *SizeRefresher) announce(storageID int64, dirs map[string]struct{}) {
	if r.emit == nil {
		return
	}
	seen := map[string]struct{}{}
	for d := range dirs {
		for {
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				r.emit.EmitChange(storageID, d, realtime.ChangeEvent{Action: "modify"})
			}
			if d == "" {
				break
			}
			parent := path.Dir(d)
			if parent == "." || parent == "/" {
				parent = ""
			}
			d = parent
		}
	}
}

// Wrap returns an emitter that forwards every change to inner and tells the
// refresher about it. Install the wrapped emitter wherever mutations publish.
func (r *SizeRefresher) Wrap(inner ChangeEmitter) ChangeEmitter {
	return sizeTouchingEmitter{inner: inner, sizes: r}
}

type sizeTouchingEmitter struct {
	inner ChangeEmitter
	sizes *SizeRefresher
}

func (e sizeTouchingEmitter) EmitChange(storageID int64, dir string, ev realtime.ChangeEvent) {
	if e.inner != nil {
		e.inner.EmitChange(storageID, dir, ev)
	}
	e.sizes.Touch(storageID, dir)
}

func cleanDir(dir string) string {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" || dir == "." {
		return ""
	}
	return path.Clean(dir)
}

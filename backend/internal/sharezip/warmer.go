package sharezip

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// DefaultWarmInterval is how often the background warmer re-checks folder
// shares for changes.
const DefaultWarmInterval = 5 * time.Minute

// DefaultShareRefreshInterval is how often the view of active folder shares is
// refreshed.
//
// ⚠ It is refreshed by its own loop, not by the warm pass, and this is the
// whole point: a warm pass can sit inside a single multi-hour build, and a
// running build asks this view whether to give up. Refreshing only between
// passes would mean never refreshing during the one build that most needs it —
// exactly the build that read a 16.7 GB folder for three hours for a share
// that had already expired.
const DefaultShareRefreshInterval = time.Minute

// DirShare is the minimal view of an active folder share the warmer needs. The
// caller supplies these via a list function so this package stays decoupled
// from the db/model layers.
type DirShare struct {
	StorageID int64
	Path      string
	NodeID    int64
}

// ActiveShares is the single answer to "which nodes have a live folder share
// right now". Everything that needs that answer reads it here: the warmer
// (what to pre-build), the sweeper (what may be deleted) and a running build
// (whether to abandon itself). One list function, one definition of active.
type ActiveShares struct {
	list func(context.Context) ([]DirShare, error)

	call sync.Mutex // serialises list calls
	mu   sync.Mutex
	// nodes is replaced wholesale, never mutated, so Snapshot may hand it
	// out by reference. Callers must not write to it.
	nodes map[int64]struct{}
	at    time.Time
	ok    bool
}

// NewActiveShares returns a view backed by list. A nil list yields nil, and
// every reader treats that as "I know nothing" rather than "nothing is active".
func NewActiveShares(list func(context.Context) ([]DirShare, error)) *ActiveShares {
	if list == nil {
		return nil
	}
	return &ActiveShares{list: list}
}

// Refresh re-lists the active folder shares and replaces the snapshot. On
// error the previous snapshot is kept: a database hiccup must not read as
// "every share is gone", which would abandon live builds and sweep live
// archives.
func (a *ActiveShares) Refresh(ctx context.Context) ([]DirShare, error) {
	if a == nil {
		return nil, errors.New("sharezip: no share list configured")
	}
	a.call.Lock()
	defer a.call.Unlock()

	rows, err := a.list(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make(map[int64]struct{}, len(rows))
	for _, s := range rows {
		nodes[s.NodeID] = struct{}{}
	}
	a.mu.Lock()
	a.nodes, a.at, a.ok = nodes, time.Now(), true
	a.mu.Unlock()
	return rows, nil
}

// Snapshot returns the node set, when it was taken, and whether a listing has
// ever succeeded. The map is read-only for callers.
func (a *ActiveShares) Snapshot() (map[int64]struct{}, time.Time, bool) {
	if a == nil {
		return nil, time.Time{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nodes, a.at, a.ok
}

// Warmer periodically pre-generates (or refreshes) the cached ZIP for every
// active folder share, so a downloader almost always hits a warm cache instead
// of waiting for an on-demand build. A changed folder is detected cheaply via
// the content signature — an unchanged folder costs one metadata listing and no
// re-compression.
//
// It is also where the cache is garbage-collected: each pass sweeps the
// archives of shares that no longer exist (see Cache.Sweep).
type Warmer struct {
	cache    *Cache
	active   *ActiveShares
	resolver func(int64) (storage.Driver, error)
	interval time.Duration
	logf     func(format string, args ...any)
	// tooLarge remembers the nodes already reported as over the warm ceiling,
	// so a big folder is logged once rather than on every pass for as long as
	// its share lives. Cleared when the node stops being listed.
	tooLarge map[int64]struct{}
}

// NewWarmer builds a Warmer. interval<=0 uses DefaultWarmInterval; logf==nil
// discards logs. It also wires the cache to the share view it builds, so the
// sweeper and any running build judge "active" by this same list function.
func NewWarmer(cache *Cache, list func(ctx context.Context) ([]DirShare, error), resolver func(int64) (storage.Driver, error), interval time.Duration, logf func(string, ...any)) *Warmer {
	if interval <= 0 {
		interval = DefaultWarmInterval
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	w := &Warmer{cache: cache, active: NewActiveShares(list), resolver: resolver, interval: interval, logf: logf, tooLarge: map[int64]struct{}{}}
	if cache != nil && w.active != nil {
		cache.Track(w.active)
	}
	return w
}

// Start launches the warm loop in a goroutine until ctx is cancelled. It is a
// no-op when caching is disabled or no list function was provided.
func (w *Warmer) Start(ctx context.Context) {
	if w.cache == nil || !w.cache.Enabled() || w.active == nil || w.resolver == nil {
		return
	}
	go w.refreshLoop(ctx)
	go func() {
		t := time.NewTicker(w.interval)
		defer t.Stop()
		w.runOnce(ctx) // warm on boot too
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.runOnce(ctx)
			}
		}
	}()
}

// refreshLoop keeps the active-share view current independently of the warm
// pass, which may be blocked inside a single long build.
func (w *Warmer) refreshLoop(ctx context.Context) {
	every := DefaultShareRefreshInterval
	if w.interval < every {
		every = w.interval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := w.active.Refresh(ctx); err != nil {
				w.logf("sharezip warmer: share refresh failed: %v", err)
			}
		}
	}
}

// runOnce sweeps dead archives and then warms every active folder share once,
// sequentially (a household has a handful of shares; sequential keeps
// object-storage load gentle).
func (w *Warmer) runOnce(ctx context.Context) {
	shares, err := w.active.Refresh(ctx)
	if err != nil {
		w.logf("sharezip warmer: list failed: %v", err)
		return
	}
	// Sweep first: free the disk before filling more of it, and do it even
	// if a warm below blocks for a long time.
	if n, freed := w.cache.Sweep(); n > 0 {
		w.logf("sharezip warmer: swept %d dead folder-share zip(s), freed %d bytes", n, freed)
	}
	regenerated := 0
	listed := make(map[int64]struct{}, len(shares))
	for _, s := range shares {
		listed[s.NodeID] = struct{}{}
		select {
		case <-ctx.Done():
			return
		default:
		}
		drv, err := w.resolver(s.StorageID)
		if err != nil {
			continue
		}
		did, err := w.cache.Warm(ctx, drv, s.Path, s.NodeID)
		if err != nil {
			if errors.Is(err, ErrShareGone) {
				// Not a failure: the share died while we built for it.
				continue
			}
			if errors.Is(err, ErrTooLargeToWarm) {
				// Not a failure either: deliberately left for a visitor to
				// start. Said once per share, not once per pass.
				if _, said := w.tooLarge[s.NodeID]; !said {
					w.tooLarge[s.NodeID] = struct{}{}
					w.logf("sharezip warmer: node %d is over the warm ceiling (%d bytes); its archive builds on demand", s.NodeID, w.cache.WarmMaxBytes)
				}
				continue
			}
			w.logf("sharezip warmer: warm node %d failed: %v", s.NodeID, err)
			continue
		}
		if did {
			regenerated++
		}
	}
	for id := range w.tooLarge {
		if _, still := listed[id]; !still {
			delete(w.tooLarge, id)
		}
	}
	if regenerated > 0 {
		w.logf("sharezip warmer: (re)generated %d folder-share zip(s)", regenerated)
	}
}

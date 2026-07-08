package sharezip

import (
	"context"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// DefaultWarmInterval is how often the background warmer re-checks folder
// shares for changes.
const DefaultWarmInterval = 5 * time.Minute

// DirShare is the minimal view of an active folder share the warmer needs. The
// caller supplies these via a list function so this package stays decoupled
// from the db/model layers.
type DirShare struct {
	StorageID int64
	Path      string
	NodeID    int64
}

// Warmer periodically pre-generates (or refreshes) the cached ZIP for every
// active folder share, so a downloader almost always hits a warm cache instead
// of waiting for an on-demand build. A changed folder is detected cheaply via
// the content signature — an unchanged folder costs one metadata listing and no
// re-compression.
type Warmer struct {
	cache    *Cache
	list     func(ctx context.Context) ([]DirShare, error)
	resolver func(int64) (storage.Driver, error)
	interval time.Duration
	logf     func(format string, args ...any)
}

// NewWarmer builds a Warmer. interval<=0 uses DefaultWarmInterval; logf==nil
// discards logs.
func NewWarmer(cache *Cache, list func(ctx context.Context) ([]DirShare, error), resolver func(int64) (storage.Driver, error), interval time.Duration, logf func(string, ...any)) *Warmer {
	if interval <= 0 {
		interval = DefaultWarmInterval
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Warmer{cache: cache, list: list, resolver: resolver, interval: interval, logf: logf}
}

// Start launches the warm loop in a goroutine until ctx is cancelled. It is a
// no-op when caching is disabled or no list function was provided.
func (w *Warmer) Start(ctx context.Context) {
	if w.cache == nil || !w.cache.Enabled() || w.list == nil || w.resolver == nil {
		return
	}
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

// runOnce warms every active folder share once, sequentially (a household has a
// handful of shares; sequential keeps object-storage load gentle).
func (w *Warmer) runOnce(ctx context.Context) {
	shares, err := w.list(ctx)
	if err != nil {
		w.logf("sharezip warmer: list failed: %v", err)
		return
	}
	regenerated := 0
	for _, s := range shares {
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
			w.logf("sharezip warmer: warm node %d failed: %v", s.NodeID, err)
			continue
		}
		if did {
			regenerated++
		}
	}
	if regenerated > 0 {
		w.logf("sharezip warmer: (re)generated %d folder-share zip(s)", regenerated)
	}
}

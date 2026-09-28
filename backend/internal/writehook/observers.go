package writehook

import (
	"context"
	"log/slog"
	"sync"

	"github.com/brf-tech/filex/backend/internal/model"
)

// WriteObserver sees a finished file write (replaced: a file was there
// before). It cannot refuse anything — the bytes have landed. Today the
// observers are E2E bookkeeping: the server's own audit row for a rewritten
// single encrypted file (e2e/fxewatch).
type WriteObserver func(ctx context.Context, storageID int64, node *model.Node, origin string, replaced bool)

type namedObserver struct {
	name string
	f    WriteObserver
}

var (
	observersMu sync.RWMutex
	observers   []namedObserver
)

// ObserveWrites installs the after-write observer called name, replacing the
// one installed under that name before — so a router built twice in one
// process (every test binary) does not run it twice. nil removes it. Named
// rather than single-slot so that independent observers never overwrite each
// other's registration.
func ObserveWrites(name string, f WriteObserver) {
	observersMu.Lock()
	defer observersMu.Unlock()
	out := observers[:0:0]
	for _, o := range observers {
		if o.name != name {
			out = append(out, o)
		}
	}
	if f != nil {
		out = append(out, namedObserver{name: name, f: f})
	}
	observers = out
}

// notifyObservers runs every installed observer, in the order they were
// installed. One that panics is logged and skipped: the write it observes has
// already succeeded and must not be turned into a failure by bookkeeping.
func notifyObservers(ctx context.Context, storageID int64, node *model.Node, origin string, replaced bool) {
	observersMu.RLock()
	list := observers
	observersMu.RUnlock()
	for _, o := range list {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					slog.Warn("writehook: observer panic", slog.String("observer", o.name), slog.Any("recover", rec))
				}
			}()
			o.f(ctx, storageID, node, origin, replaced)
		}()
	}
}

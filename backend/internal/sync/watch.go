package sync

import (
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// loopDriverWatch drives a storage from the driver's OWN change stream
// (storage.Watcher) instead of polling it.
//
// # Why this exists
//
// `fsnotify` sync mode used to mean one thing: the local driver, watched with
// inotify. Any other driver that asked for it was quietly downgraded to
// polling — and `storage.Watcher`, which the driver interface has declared
// for years, had no consumer anywhere in filex. A plugin could implement a
// change stream, filex would advertise the capability, and nothing would ever
// subscribe. The documentation said it bought "change events without
// polling"; it bought nothing.
//
// Now a storage in `fsnotify` mode uses, in order:
//
//  1. inotify, when the driver is local (unchanged — it is cheaper and it
//     sees changes made outside filex on the same disk);
//  2. the driver's own stream, when it implements storage.Watcher;
//  3. polling.
//
// ⚠ The stream is a HINT, not a ledger. Events are coalesced and each batch
// triggers the same RunOnce a poll would, so a missed or duplicated event
// costs a scan, never a wrong index. A backend that streams perfectly and one
// that streams sloppily therefore differ in latency, not in correctness.
func (s *storageSyncer) loopDriverWatch(w storage.Watcher) {
	// The initial scan is what makes the index true; the stream only makes it
	// current sooner.
	s.noteRun(s.RunOnce(s.ctx))

	events, err := w.Subscribe(s.ctx)
	if err != nil {
		slog.Warn("sync: driver watch failed, falling back to poll",
			slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
		s.loopPoll()
		return
	}

	// Same debounce as the inotify loop: an unpacking archive is one scan,
	// not one per file.
	const debounceFor = 2 * time.Second
	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	// ⚠ A stream that ends is not the end of the storage. A plugin restarts,
	// a connection drops; when that happens we fall back to polling rather
	// than leaving the storage frozen with a stale index — the failure mode
	// that would look like "filex stopped seeing my files".
	for {
		select {
		case <-s.ctx.Done():
			return

		case ev, ok := <-events:
			if !ok {
				slog.Info("sync: driver watch stream ended, falling back to poll",
					slog.String("storage", s.storage.Name))
				s.loopPoll()
				return
			}
			slog.Debug("sync: driver event",
				slog.String("storage", s.storage.Name),
				slog.String("op", ev.Op), slog.String("path", ev.Path))
			if !pending {
				pending = true
				debounce.Reset(debounceFor)
			}

		case <-debounce.C:
			pending = false
			if err := s.RunOnce(s.ctx); err != nil {
				slog.Warn("sync: run after driver event failed",
					slog.String("storage", s.storage.Name), slog.String("err", err.Error()))
			}
		}
	}
}

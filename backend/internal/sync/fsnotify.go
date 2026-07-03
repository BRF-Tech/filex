package sync

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// loopFSNotify uses inotify (Linux) / kqueue (BSD) / ReadDirectoryChangesW
// (Windows) to watch a local FS storage and trigger an immediate RunOnce
// after each batch of events. We coalesce events with a 2-second debouncer
// so a `tar -xf` or `cp -r` doesn't trigger N runs.
//
// Falls back to polling if the configured driver is not local.
func (s *storageSyncer) loopFSNotify() {
	localDrv, ok := s.driver.(*local.Driver)
	if !ok {
		slog.Warn("sync: fsnotify mode requested but driver is not local — falling back to poll", slog.String("storage", s.storage.Name))
		s.loopPoll()
		return
	}
	root := localDrv.Root()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("sync: fsnotify new watcher", slog.String("err", err.Error()))
		s.loopPoll()
		return
	}
	defer w.Close()

	if err := addRecursive(w, root); err != nil {
		slog.Warn("sync: fsnotify add roots", slog.String("err", err.Error()))
	}

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	// Initial full scan.
	if err := s.RunOnce(s.ctx); err != nil {
		slog.Warn("sync: initial run failed", slog.String("err", err.Error()))
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// Add new directories on the fly.
			if ev.Has(fsnotify.Create) {
				_ = w.Add(ev.Name)
			}
			if !pending {
				pending = true
				debounce.Reset(2 * time.Second)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Warn("sync: fsnotify error", slog.String("err", err.Error()))
		case <-debounce.C:
			pending = false
			if err := s.RunOnce(s.ctx); err != nil {
				slog.Warn("sync: debounced run failed", slog.String("err", err.Error()))
			}
		}
	}
}

// addRecursive walks root and adds every directory to the watcher.
func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip
		}
		if info.IsDir() && !strings.HasPrefix(filepath.Base(p), ".") {
			_ = w.Add(p)
		}
		return nil
	})
}

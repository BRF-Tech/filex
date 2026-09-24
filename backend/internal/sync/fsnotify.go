package sync

import (
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/brf-tech/filex/backend/internal/scanrule"
	"github.com/brf-tech/filex/backend/internal/storage"
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
		// Not local: the driver may still have a change stream of its own
		// (storage.Watcher — a plugin, typically). Falling straight through
		// to polling is what made that interface dead code for years.
		if w, hasWatch := s.driver.(storage.Watcher); hasWatch {
			slog.Info("sync: driver is not local but streams its own changes — using them",
				slog.String("storage", s.storage.Name))
			s.loopDriverWatch(w)
			return
		}
		slog.Warn("sync: fsnotify mode requested but the driver is not local and streams no changes — falling back to poll", slog.String("storage", s.storage.Name))
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

	if err := addRecursive(w, root, s.rule); err != nil {
		slog.Warn("sync: fsnotify add roots", slog.String("err", err.Error()))
	}

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	// Initial full scan.
	s.noteRun(s.RunOnce(s.ctx))

	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// A change the walk would not look at is not a reason to walk
			// (issue #44): a download client filling `incomplete/`, or git
			// rewriting `.git/index`, would otherwise start a full scan every
			// two seconds for as long as it runs.
			if !watched(root, ev.Name, s.rule) {
				continue
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
				s.noteRun(err)
			}
		}
	}
}

// addRecursive adds every directory under root the walk would enter to the
// watcher — the same scanrule the scan asks, so what is watched and what is
// scanned cannot drift apart.
//
// ⚠ It used to skip every folder whose NAME began with a dot and still
// descend into it: `.git` itself went unwatched while every folder inside it
// was watched, and `.versions/<id>/` — filex's own version history — raised
// a full scan on every snapshot. A folder the rule skips is now skipped whole
// (SkipDir), and a hidden folder is watched like any other unless the
// storage's exclusions say otherwise (`.*` does).
func addRecursive(w *fsnotify.Watcher, root string, rule *scanrule.Rule) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && !watched(root, p, rule) {
			return filepath.SkipDir
		}
		_ = w.Add(p)
		return nil
	})
}

// watched reports whether abs, a path under the storage's root on disk, is
// something the walk looks at.
func watched(root, abs string, rule *scanrule.Rule) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return true
	}
	return !rule.Skips(rel)
}

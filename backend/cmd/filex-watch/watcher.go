package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// treeWatcher watches a folder and everything under it - one operating
// system watch per folder, since none of the three systems watches a tree in
// one go the same way - and turns what it sees into the queue's words: this
// folder changed, re-read that subtree, re-read everything.
//
// It never tells filex WHAT changed, only WHERE: filex lists the folder and
// decides for itself (a report is a hint, not a ledger).
type treeWatcher struct {
	root string
	w    *fsnotify.Watcher
	q    *queue
	log  *slog.Logger

	mu sync.Mutex
	// unwatched are the subtrees the operating system would not let us watch
	// (its watch limit): filex re-reads them on a timer instead.
	unwatched map[string]struct{}
	// limitSaid: the watch limit is logged once, not once per folder.
	limitSaid bool
	// now is the clock; nil = time.Now.
	now func() time.Time
}

func newTreeWatcher(root string, q *queue, log *slog.Logger) (*treeWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &treeWatcher{root: root, w: w, q: q, log: log, unwatched: map[string]struct{}{}}, nil
}

func (t *treeWatcher) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *treeWatcher) close() { _ = t.w.Close() }

// rel is abs as a storage path ("/a/b"), ok false outside the root or for a
// name the protocol cannot carry.
func (t *treeWatcher) rel(abs string) (string, bool) {
	r, err := filepath.Rel(t.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	if r == "." {
		return "/", true
	}
	p, err := watchproto.CleanFolder("/" + filepath.ToSlash(r))
	if err != nil {
		return "", false
	}
	return p, true
}

// skipped reports a folder the agent leaves alone: filex's own trees on the
// storage (its trash, version history, thumbnails and drafts), which filex
// never catalogues by path and which filex itself is usually writing.
func skipped(p string) bool { return p != "/" && syspath.Sealed(strings.TrimPrefix(p, "/")) }

// addTree watches dir and every folder under it. With report, every folder
// it finds is queued as changed: a folder that just appeared may have had
// files written into it before its watch was in place.
func (t *treeWatcher) addTree(dir string, report bool) {
	now := t.clock()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			t.log.Debug("a folder could not be read", slog.String("path", p), slog.String("err", err.Error()))
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rp, ok := t.rel(p)
		if !ok || skipped(rp) {
			return filepath.SkipDir
		}
		if err := t.w.Add(p); err != nil {
			if isWatchLimit(err) {
				t.unwatchable(rp)
				return filepath.SkipDir
			}
			t.log.Warn("a folder could not be watched; changes in it reach filex at its next scan",
				slog.String("path", p), slog.String("err", err.Error()))
			return filepath.SkipDir
		}
		if report {
			t.q.addFolder(rp, now)
		}
		return nil
	})
}

// isWatchLimit reports the operating system's "no more watches" (inotify's
// max_user_watches: ENOSPC; kqueue's open files: EMFILE).
func isWatchLimit(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE)
}

// unwatchable records a subtree the system would not let us watch, queues it
// to be re-read now, and says so once.
func (t *treeWatcher) unwatchable(rp string) {
	t.mu.Lock()
	t.unwatched[rp] = struct{}{}
	first := !t.limitSaid
	t.limitSaid = true
	t.mu.Unlock()
	t.q.addSubtree(rp, t.clock())
	if first {
		t.log.Warn("the operating system's watch limit is reached: the folders past it are re-read by filex on a timer (subtree_resync) instead of watched. On Linux raise fs.inotify.max_user_watches; on macOS the open files limit (ulimit -n)",
			slog.String("first", rp))
	}
}

// unwatchedList is the subtrees filex re-reads on a timer.
func (t *treeWatcher) unwatchedList() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return sortedKeys(t.unwatched)
}

// loop reads the operating system's events until stop closes.
func (t *treeWatcher) loop(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case ev, ok := <-t.w.Events:
			if !ok {
				return
			}
			t.event(ev)
		case err, ok := <-t.w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// The system dropped events: what changed is unknown.
				t.log.Warn("the operating system dropped change events; asking filex for a full resync")
				t.q.markResync(t.clock())
				continue
			}
			t.log.Warn("watch error", slog.String("err", err.Error()))
		}
	}
}

// event turns one operating system event into the queue's words.
//
//   - Something inside a folder was made, written, removed or renamed: the
//     folder that holds it changed.
//   - A folder was made (or renamed into place): it is watched, with
//     everything under it, and each of its folders is reported too.
//   - A folder was removed or renamed away: its watches go. Where it went
//     to, if it stayed under the root, arrives as a creation.
//   - The root itself went away: everything is unknown; resync.
//
// A change of permissions alone (chmod) is not a change the catalogue keeps.
func (t *treeWatcher) event(ev fsnotify.Event) {
	if ev.Op == fsnotify.Chmod {
		return
	}
	now := t.clock()
	if filepath.Clean(ev.Name) == t.root {
		if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
			t.log.Warn("the watched folder itself was removed or renamed; asking filex for a full resync")
			t.q.markResync(now)
		}
		return
	}
	rp, ok := t.rel(ev.Name)
	if !ok || skipped(rp) {
		return
	}
	parent := watchproto.Parent(rp)
	if skipped(parent) {
		return
	}
	t.q.addFolder(parent, now)
	switch {
	case ev.Has(fsnotify.Create):
		if fi, err := os.Lstat(ev.Name); err == nil && fi.IsDir() {
			t.addTree(ev.Name, true)
		}
	case ev.Has(fsnotify.Remove), ev.Has(fsnotify.Rename):
		t.forget(ev.Name)
	}
}

// forget drops the watches on abs and under it: a folder that was removed is
// gone, and one that was renamed would otherwise keep reporting under its old
// name.
func (t *treeWatcher) forget(abs string) {
	prefix := abs + string(filepath.Separator)
	for _, p := range t.w.WatchList() {
		if p == abs || strings.HasPrefix(p, prefix) {
			_ = t.w.Remove(p)
		}
	}
	if rp, ok := t.rel(abs); ok {
		t.mu.Lock()
		for u := range t.unwatched {
			if watchproto.Within(u, rp) {
				delete(t.unwatched, u)
			}
		}
		t.mu.Unlock()
	}
}

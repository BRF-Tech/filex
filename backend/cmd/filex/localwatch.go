package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/brf-tech/filex/backend/internal/filesync"
)

// localWatcher turns file-system events in the synced folders into "this
// folder changed" notes for the live loop, and changes to pairs.json into
// "re-read the pairs".
//
// ⚠ fsnotify watches ONE directory per watch on every platform (its recursive
// mode is internal), so every folder of every pair is added individually and
// new folders are added as they appear. On Windows and Linux that is one
// handle / one inotify watch per FOLDER. On macOS the kqueue backend opens a
// descriptor per FILE as well, so a large tree there would run the process out
// of descriptors — and a sync engine that cannot open a socket is worse than
// one that notices local edits 30 s late. Past kqueueBudget entries a pair is
// therefore left to the interval poll, and the watcher says so once, for that
// pair (errTooLargeToWatch) — the desktop app shows it under the folder.
//
// Events are only hints. Everything decided about a file is decided by a pass
// that reads the disk and the server again; a missed event costs latency (the
// poll still finds it), never correctness.
type localWatcher struct {
	w        *fsnotify.Watcher
	storeDir string
	onChange func(pairID, dir string)
	onPairs  func()
	// report says, per pair, that its folder cannot be watched (err) — or,
	// with err == nil, that a folder reported before is watched again. Only
	// transitions are reported, never the steady state.
	report func(pairID string, err error)

	mu     sync.Mutex
	pairs  map[string]watchedPair // pair id → what is watched for it
	failed map[string]string      // pair id → why its folder is not watched (retried on every Sync)
	told   map[string]bool        // pair ids whose failure was reported (so a recovery is too)
}

type watchedPair struct {
	id   string
	root string // absolute local folder (a file pair: the folder holding the file)
	file string // file pair: the file's name; "" for a folder pair
}

// kqueueBudget is how many files+folders one pair may have before macOS local
// watching is skipped for it (see localWatcher). Variables, not constants, so
// a test on any platform can exercise the budget.
var (
	kqueueBudget  = 4000
	budgetApplies = runtime.GOOS == "darwin"
)

// applyWatchBudgetEnv honours FILEX_SYNC_WATCH_BUDGET: a cap on how many items
// a pair may have for its folder to be watched, on ANY platform (a Linux box
// short of inotify watches, or proving the "not watched" state end to end on
// a machine that is not a Mac). Anything but a positive number is ignored.
func applyWatchBudgetEnv(getenv func(string) string) {
	if n, err := strconv.Atoi(strings.TrimSpace(getenv("FILEX_SYNC_WATCH_BUDGET"))); err == nil && n > 0 {
		kqueueBudget, budgetApplies = n, true
	}
}

// errTooLargeToWatch is a pair left to the full check because watching it
// would cost more descriptors than the process can spare (macOS, kqueue).
var errTooLargeToWatch = errors.New("too large to watch")

func newLocalWatcher(storeDir string, onChange func(pairID, dir string), onPairs func(), report func(pairID string, err error)) (*localWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	lw := &localWatcher{w: w, storeDir: storeDir, onChange: onChange, onPairs: onPairs, report: report,
		pairs: map[string]watchedPair{}, failed: map[string]string{}, told: map[string]bool{}}
	if storeDir != "" {
		if err := os.MkdirAll(storeDir, 0o700); err == nil {
			_ = w.Add(storeDir)
		}
	}
	return lw, nil
}

// Sync makes the watch set match the pairs: new pairs are walked and added,
// removed ones dropped, and pairs whose folder could not be watched before are
// tried again.
//
// ⚠ The retry is not optional: a freshly paired folder usually does not EXIST
// yet when the watcher first sees the pair — the pair's first pass creates it.
// Without a retry after that pass, the folder the desktop app just started
// keeping would only ever be synced by the interval poll, silently.
func (lw *localWatcher) Sync(pairs []filesync.Pair) {
	want := map[string]watchedPair{}
	for _, p := range pairs {
		if p.Paused {
			continue
		}
		wp := watchedPair{id: p.ID, root: filepath.Clean(p.Local)}
		if p.File {
			wp.root, wp.file = filepath.Dir(wp.root), filepath.Base(wp.root)
		}
		want[p.ID] = wp
	}
	lw.mu.Lock()
	var add, drop []watchedPair
	for id, wp := range want {
		old, ok := lw.pairs[id]
		_, retry := lw.failed[id]
		if !ok || old != wp || retry {
			add = append(add, wp)
			if ok && old != wp {
				drop = append(drop, old)
			}
		}
	}
	for id := range lw.failed {
		if _, ok := want[id]; !ok {
			delete(lw.failed, id)
			delete(lw.told, id)
		}
	}
	for id, old := range lw.pairs {
		if _, ok := want[id]; !ok {
			drop = append(drop, old)
		}
	}
	lw.pairs = want
	lw.mu.Unlock()

	for _, wp := range drop {
		lw.unwatchTree(wp)
	}
	for _, wp := range add {
		err := lw.watchTree(wp)
		lw.mu.Lock()
		prev, wasFailed := lw.failed[wp.id]
		told := lw.told[wp.id]
		if err != nil {
			lw.failed[wp.id] = err.Error()
		} else {
			delete(lw.failed, wp.id)
			delete(lw.told, wp.id)
		}
		notMissing := err != nil && !errors.Is(err, fs.ErrNotExist)
		if notMissing && (!wasFailed || prev != err.Error()) {
			lw.told[wp.id] = true
		}
		lw.mu.Unlock()
		switch {
		case err != nil && !notMissing:
			// Not there yet — the pair's first pass creates it. Quietly retried.
		case notMissing && (!wasFailed || prev != err.Error()):
			lw.report(wp.id, err)
		case err == nil && wasFailed:
			if told {
				lw.report(wp.id, nil)
			}
			// Now watched: anything written before the watch existed is only
			// visible by looking.
			lw.onChange(wp.id, "")
		}
	}
}

func (lw *localWatcher) watchTree(wp watchedPair) error {
	if wp.file != "" {
		// One file: its folder is enough.
		return lw.w.Add(wp.root)
	}
	if budgetApplies {
		n := 0
		_ = filepath.WalkDir(wp.root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if filesync.IgnoredName(d.Name()) && d.IsDir() {
				return filepath.SkipDir
			}
			n++
			if n > kqueueBudget {
				return errors.New("budget")
			}
			return nil
		})
		if n > kqueueBudget {
			return fmt.Errorf("%w: more than %d items (on macOS every file costs a descriptor; FILEX_SYNC_WATCH_BUDGET)", errTooLargeToWatch, kqueueBudget)
		}
	}
	return lw.addDirs(wp.root)
}

// addDirs watches dir and every folder under it the engine syncs.
func (lw *localWatcher) addDirs(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir && filesync.IgnoredName(d.Name()) {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		if err := lw.w.Add(p); err != nil {
			return err
		}
		return nil
	})
}

func (lw *localWatcher) unwatchTree(wp watchedPair) {
	for _, p := range lw.w.WatchList() {
		if p == wp.root || strings.HasPrefix(p, wp.root+string(filepath.Separator)) {
			if p == lw.storeDir {
				continue
			}
			_ = lw.w.Remove(p)
		}
	}
}

// Run delivers events until ctx ends.
func (lw *localWatcher) Run(ctx context.Context) {
	defer lw.w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-lw.w.Events:
			if !ok {
				return
			}
			lw.handle(ev)
		case err, ok := <-lw.w.Errors:
			if !ok {
				return
			}
			// An overflowed event queue means events were LOST: say which
			// folders may be stale by asking for a pass of every pair.
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				lw.mu.Lock()
				ids := make([]string, 0, len(lw.pairs))
				for id := range lw.pairs {
					ids = append(ids, id)
				}
				lw.mu.Unlock()
				for _, id := range ids {
					lw.onChange(id, "")
				}
			}
		}
	}
}

func (lw *localWatcher) handle(ev fsnotify.Event) {
	name := filepath.Clean(ev.Name)
	parent := filepath.Dir(name)
	base := filepath.Base(name)
	if lw.storeDir != "" && parent == filepath.Clean(lw.storeDir) {
		if base == "pairs.json" && lw.onPairs != nil {
			lw.onPairs()
		}
		return
	}
	if filesync.IgnoredName(base) {
		return
	}

	lw.mu.Lock()
	var hits []watchedPair
	for _, wp := range lw.pairs {
		if wp.file != "" {
			if parent == wp.root && base == wp.file {
				hits = append(hits, wp)
			}
			continue
		}
		if name == wp.root || strings.HasPrefix(name, wp.root+string(filepath.Separator)) {
			hits = append(hits, wp)
		}
	}
	lw.mu.Unlock()

	for _, wp := range hits {
		if wp.file != "" {
			lw.onChange(wp.id, "")
			continue
		}
		rel, err := filepath.Rel(wp.root, name)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			// The pair's own folder was renamed or removed: a full pass is
			// what knows how to refuse that safely (guardMissingMirror).
			lw.onChange(wp.id, "")
			continue
		}
		ignored := false
		for _, seg := range strings.Split(rel, "/") {
			if filesync.IgnoredName(seg) {
				ignored = true
				break
			}
		}
		if ignored {
			continue
		}
		dir := ""
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			dir = rel[:i]
		}
		lw.onChange(wp.id, dir)
		// A folder that just appeared needs watching itself — and anything
		// already inside it (a folder moved or unpacked in) is only visible
		// by looking, so its own contents are a change too.
		if ev.Has(fsnotify.Create) {
			if info, err := os.Lstat(name); err == nil && info.IsDir() {
				_ = lw.addDirs(name)
				lw.onChange(wp.id, rel)
			}
		}
	}
}

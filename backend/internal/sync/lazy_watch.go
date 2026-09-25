package sync

import (
	"container/list"
	"context"
	"errors"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	gosync "sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// watchSet is the lazy catalogue's fsnotify budget: watches on VISITED
// folders only, at most cfg.maxWatches of them (least recently opened out
// first), none on a folder nobody has opened for cfg.watchTTL.
//
// ⚠ A watch that goes — evicted, expired, refused by the kernel, or lost with
// the process — never takes a catalogue row with it. The folder is marked
// "reconcile on next open" and the next listing of it comes from the disk
// until that reconcile has run.
type watchSet struct {
	lc   *lazyCatalogue
	root string // the storage root on disk

	mu      gosync.Mutex
	w       *fsnotify.Watcher
	byDir   map[string]*list.Element
	lru     *list.List // front: most recently opened
	refused bool       // the kernel said no once; logged once

	// pending folders with events, waiting out the quiet period.
	pmu     gosync.Mutex
	pending map[string]time.Time // folder -> first event of its burst
	last    map[string]time.Time // folder -> latest event
}

type watchEntry struct {
	dir       string
	abs       string
	lastUsed  time.Time
	confirmed bool // its reconcile has run and the row says watched
}

func newWatchSet(lc *lazyCatalogue) *watchSet {
	ws := &watchSet{
		lc:      lc,
		byDir:   map[string]*list.Element{},
		lru:     list.New(),
		pending: map[string]time.Time{},
		last:    map[string]time.Time{},
	}
	if d, ok := lc.s.driver.(*local.Driver); ok {
		ws.root = d.Root()
	}
	return ws
}

func (ws *watchSet) start() error {
	if ws.root == "" {
		return errors.New("the storage has no local root")
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	ws.mu.Lock()
	ws.w = w
	ws.mu.Unlock()
	return nil
}

func (ws *watchSet) close() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.w != nil {
		_ = ws.w.Close()
		ws.w = nil
	}
	ws.byDir = map[string]*list.Element{}
	ws.lru.Init()
	metrics.LazyWatches.WithLabelValues(ws.lc.label).Set(0)
}

func (ws *watchSet) abs(dir string) string {
	rel := strings.TrimPrefix(dir, "/")
	if rel == "" {
		return ws.root
	}
	return filepath.Join(ws.root, filepath.FromSlash(rel))
}

// add places a watch on dir (or refreshes its place in the LRU), evicting the
// least recently opened folder when the budget is full. The watch is not yet
// CONFIRMED: has() stays false until its reconcile has run (confirm).
func (ws *watchSet) add(dir string) {
	ws.mu.Lock()
	if ws.w == nil {
		ws.mu.Unlock()
		return
	}
	if el, ok := ws.byDir[dir]; ok {
		el.Value.(*watchEntry).lastUsed = time.Now()
		ws.lru.MoveToFront(el)
		ws.mu.Unlock()
		return
	}
	var evicted []*watchEntry
	for ws.lru.Len() >= ws.lc.cfg.maxWatches {
		back := ws.lru.Back()
		if back == nil {
			break
		}
		evicted = append(evicted, ws.dropLocked(back))
	}
	e := &watchEntry{dir: dir, abs: ws.abs(dir), lastUsed: time.Now()}
	if err := ws.w.Add(e.abs); err != nil {
		refusedNow := !ws.refused && (errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE))
		if refusedNow {
			ws.refused = true
		}
		ws.mu.Unlock()
		if refusedNow {
			slog.Warn("sync: lazy: the kernel refused another watch; treating the watch budget as full (raise fs.inotify.max_user_watches, or lower lazy_max_watches)",
				slog.String("storage", ws.lc.s.storage.Name), slog.String("err", err.Error()))
		}
		ws.persistEvicted(evicted)
		return
	}
	ws.byDir[dir] = ws.lru.PushFront(e)
	n := ws.lru.Len()
	ws.mu.Unlock()
	metrics.LazyWatches.WithLabelValues(ws.lc.label).Set(float64(n))
	ws.persistEvicted(evicted)
}

// confirm marks dir's watch as backing a reconciled folder and records it.
func (ws *watchSet) confirm(ctx context.Context, dir string) {
	ws.mu.Lock()
	el, ok := ws.byDir[dir]
	if ok {
		el.Value.(*watchEntry).confirmed = true
	}
	ws.mu.Unlock()
	if !ok {
		return
	}
	st := ws.lc.s.storage
	now := time.Now().UTC()
	_ = ws.lc.s.store.SetCatalogueFolderWatch(context.WithoutCancel(ctx), st.ID, pathkey.Hash(st.ID, dir), &now, false)
}

// has reports whether dir is watched by a confirmed watch.
func (ws *watchSet) has(dir string) bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	el, ok := ws.byDir[dir]
	return ok && el.Value.(*watchEntry).confirmed
}

// touch moves dir to the front of the LRU.
func (ws *watchSet) touch(dir string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if el, ok := ws.byDir[dir]; ok {
		el.Value.(*watchEntry).lastUsed = time.Now()
		ws.lru.MoveToFront(el)
	}
}

func (ws *watchSet) count() int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.lru.Len()
}

// remove drops dir's watch and marks it "reconcile on next open".
func (ws *watchSet) remove(dir string) {
	ws.mu.Lock()
	el, ok := ws.byDir[dir]
	var gone *watchEntry
	if ok {
		gone = ws.dropLocked(el)
	}
	n := ws.lru.Len()
	ws.mu.Unlock()
	if gone != nil {
		metrics.LazyWatches.WithLabelValues(ws.lc.label).Set(float64(n))
		ws.persistEvicted([]*watchEntry{gone})
	}
}

// removeUnder drops the watches on dir and every folder below it — a folder
// the delete pass confirmed gone. Their rows went with it, so nothing is
// recorded.
func (ws *watchSet) removeUnder(dir string) {
	ws.mu.Lock()
	for d, el := range ws.byDir {
		if d == dir || strings.HasPrefix(d, dir+"/") {
			ws.dropLocked(el)
		}
	}
	n := ws.lru.Len()
	ws.mu.Unlock()
	metrics.LazyWatches.WithLabelValues(ws.lc.label).Set(float64(n))
}

// dropLocked removes one watch from the kernel and the maps. Caller holds mu.
func (ws *watchSet) dropLocked(el *list.Element) *watchEntry {
	e := el.Value.(*watchEntry)
	ws.lru.Remove(el)
	delete(ws.byDir, e.dir)
	if ws.w != nil {
		_ = ws.w.Remove(e.abs)
	}
	return e
}

// persistEvicted records watches that went as "reconcile on next open". The
// rows stay; only the promise that they are current is withdrawn.
func (ws *watchSet) persistEvicted(evicted []*watchEntry) {
	st := ws.lc.s.storage
	for _, e := range evicted {
		if !e.confirmed {
			continue
		}
		_ = ws.lc.s.store.SetCatalogueFolderWatch(context.Background(), st.ID, pathkey.Hash(st.ID, e.dir), nil, true)
		slog.Debug("sync: lazy: watch released", slog.String("storage", st.Name), slog.String("path", e.dir))
	}
}

// sweep releases every watch on a folder nobody has opened for watchTTL.
func (ws *watchSet) sweep(now time.Time) {
	ttl := ws.lc.cfg.watchTTL
	ws.mu.Lock()
	var expired []*watchEntry
	// The whole list, not "from the back until the first fresh one": at most
	// max_watches entries once a minute, and it cannot be fooled by an entry
	// whose clock and place in the list disagree.
	for el := ws.lru.Back(); el != nil; {
		prev := el.Prev()
		if now.Sub(el.Value.(*watchEntry).lastUsed) >= ttl {
			expired = append(expired, ws.dropLocked(el))
		}
		el = prev
	}
	n := ws.lru.Len()
	ws.mu.Unlock()
	if len(expired) > 0 {
		metrics.LazyWatches.WithLabelValues(ws.lc.label).Set(float64(n))
		ws.persistEvicted(expired)
	}
}

// loop turns fsnotify events into folder reconciles: every event in a
// watched folder marks that folder, and a folder whose burst has gone quiet
// for LazyWatchQuiet (or has lasted LazyWatchMaxWait) is reconciled once.
// It also runs the TTL sweep.
func (ws *watchSet) loop(ctx context.Context) {
	ws.mu.Lock()
	w := ws.w
	ws.mu.Unlock()
	sweepEvery := ws.lc.cfg.watchTTL / 4
	if sweepEvery > time.Minute {
		sweepEvery = time.Minute
	}
	if sweepEvery < time.Second {
		sweepEvery = time.Second
	}
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	flush := time.NewTicker(LazyWatchQuiet / 4)
	defer flush.Stop()
	var (
		events <-chan fsnotify.Event
		errs   <-chan error
	)
	if w != nil {
		events, errs = w.Events, w.Errors
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-sweep.C:
			ws.sweep(now)
		case now := <-flush.C:
			ws.flushDue(now)
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			ws.event(ev)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			slog.Warn("sync: lazy: fsnotify error", slog.String("storage", ws.lc.s.storage.Name), slog.String("err", err.Error()))
		}
	}
}

// event maps one fsnotify event to the watched folder it concerns.
//
// ⚠⚠ A watched folder that ITSELF went away is not deleted here: its watch is
// released and the rows stay. Whether they go is its parent's reconcile's
// decision, made from a listing of the parent — the deletion-safety invariant.
func (ws *watchSet) event(ev fsnotify.Event) {
	rel, err := filepath.Rel(ws.root, ev.Name)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	p := db.CatalogueFolderPath(filepath.ToSlash(rel))
	// A change the catalogue would not look at is not a reason to look
	// (issue #44): `.git`, a download client's `incomplete/`, filex's own
	// trees.
	if p != "/" && ws.lc.s.rule.Skips(p) {
		return
	}
	ws.mu.Lock()
	_, selfWatched := ws.byDir[p]
	parent := path.Dir(p)
	_, parentWatched := ws.byDir[parent]
	ws.mu.Unlock()
	if selfWatched && (ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)) {
		ws.remove(p)
	}
	if p != "/" && parentWatched {
		ws.mark(parent)
	}
}

func (ws *watchSet) mark(dir string) {
	now := time.Now()
	ws.pmu.Lock()
	if _, ok := ws.pending[dir]; !ok {
		ws.pending[dir] = now
	}
	ws.last[dir] = now
	ws.pmu.Unlock()
}

// flushDue hands every folder whose burst is over to the request queue.
func (ws *watchSet) flushDue(now time.Time) {
	var due []string
	ws.pmu.Lock()
	for dir, first := range ws.pending {
		if now.Sub(ws.last[dir]) >= LazyWatchQuiet || now.Sub(first) >= LazyWatchMaxWait {
			due = append(due, dir)
			delete(ws.pending, dir)
			delete(ws.last, dir)
		}
	}
	ws.pmu.Unlock()
	for _, dir := range due {
		ws.lc.request(dir, reasonWatch)
	}
}

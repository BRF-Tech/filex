package sync

// lazy.go — the lazy catalogue: sync_mode `lazy` for local storages (issue
// #45, idea by Alex / @ahjephson). Read docs/LAZY-CATALOGUE.md first; this
// file is its implementation.
//
// The one operation everything is built from is reconcileOnce: list ONE
// folder from disk, apply that listing to the catalogue with the walk's own
// per-entry code (catalogueEntry), and — only for that folder, only for its
// direct children, only once its state row says this listing was applied —
// remove what is confirmed gone. Opening a folder, an fsnotify event, the
// background filler and a desktop sync pair all end up here.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// lazyReason says what asked for a folder reconcile. It is the label of
// filex_lazy_reconciles_total and it decides what the reconcile announces.
type lazyReason string

const (
	reasonOpen    lazyReason = "open"    // somebody listed the folder
	reasonWatch   lazyReason = "watch"   // fsnotify saw a change in a watched folder
	reasonFill    lazyReason = "fill"    // the background filler (behaviour A)
	reasonRefresh lazyReason = "refresh" // the filler, after convergence
	reasonPair    lazyReason = "pair"    // a desktop sync pair's subtree
)

// Tunables. Package variables rather than constants so the tests can run the
// filler and the watch debounce at test speed; nothing else changes them.
var (
	// LazyFillIdlePause is the filler's breath between two folders while
	// nobody is using the storage.
	LazyFillIdlePause = 2 * time.Millisecond
	// LazyFillBusyPause is the pause while somebody is: every listing, search
	// or open of the storage in the last LazyActiveWindow slows the filler to
	// this, and every open request runs before its next folder regardless.
	LazyFillBusyPause = 250 * time.Millisecond
	// LazyActiveWindow is how long one request counts as "somebody is here".
	LazyActiveWindow = 15 * time.Second
	// LazyWatchQuiet ends a burst of fsnotify events for a folder;
	// LazyWatchMaxWait is the longest a burst may postpone its reconcile.
	LazyWatchQuiet   = 2 * time.Second
	LazyWatchMaxWait = 10 * time.Second
	// LazySizeCheckpoint is how often folder sizes are recomputed while the
	// filler works (it does not announce each folder, so nothing else would).
	LazySizeCheckpoint = 30 * time.Second
	// LazyFillBatch is how many frontier rows the filler reads at a time.
	LazyFillBatch = 64
	// LazyPairRefreshFloor is the shortest interval at which the subtrees of
	// desktop sync pairs are walked again (pairLoop); the storage's own sync
	// interval is used when it is longer.
	LazyPairRefreshFloor = 30 * time.Second
	// lazyRetryFailed keeps the filler away from a folder whose listing failed
	// (permissions, I/O) for this long, so one unreadable folder cannot pin it.
	lazyRetryFailed = 10 * time.Minute
	// lazyIdleRecheck is how long a converged filler with nothing due waits
	// before it looks again.
	lazyIdleRecheck = time.Minute
	// lazyVisitWrite limits visited_at writes: a folder listed every second
	// by an open explorer is one write a minute, not sixty.
	lazyVisitWrite = time.Minute
)

// folderGuardFloor: fewer children than this vanishing at once is ordinary
// life (somebody deleted a few files over SMB) and never trips the per-folder
// ratio guard. See folderGuardOK.
const folderGuardFloor = 10

// errNotLazyFolder refuses a folder the lazy catalogue never catalogues: one
// the storage's scan rule skips (filex's own trees, scan_exclude).
var errNotLazyFolder = errors.New("sync: the folder is excluded from the catalogue")

// errAbandoned is a listing whose encrypted-folder marker row could not be
// written: nothing of it is recorded, so the next reconcile starts afresh.
var errAbandoned = errors.New("sync: the folder's encrypted-folder marker could not be catalogued")

// lazyConfig is one storage's lazy settings, read from its config map.
type lazyConfig struct {
	fill       string
	maxWatches int
	watchTTL   time.Duration
	// refreshAge: behaviour A re-reconciles an unwatched folder whose last
	// listing is older than this; a pair skips folders younger than it.
	refreshAge time.Duration
}

func lazyConfigOf(st *model.Storage, cfg map[string]any, fallback time.Duration) lazyConfig {
	c := lazyConfig{
		fill:       storage.LazyFillBackground,
		maxWatches: storage.LazyMaxWatchesDefault,
		watchTTL:   storage.LazyWatchTTLDefault * time.Minute,
		refreshAge: time.Duration(st.SyncIntervalS) * time.Second,
	}
	if c.refreshAge < 5*time.Second {
		c.refreshAge = fallback
		if c.refreshAge <= 0 {
			c.refreshAge = DefaultPollInterval
		}
	}
	if v := storage.ConfigString(cfg, storage.LazyFillKey); v == storage.LazyFillOnOpen {
		c.fill = storage.LazyFillOnOpen
	}
	if n, ok := configInt(cfg, storage.LazyMaxWatchesKey); ok && n > 0 {
		c.maxWatches = n
	}
	if n, ok := configInt(cfg, storage.LazyWatchTTLKey); ok && n > 0 {
		c.watchTTL = time.Duration(n) * time.Minute
	}
	return c
}

// configInt reads an integer setting the way JSON and a form deliver it: a
// number, or a numeric string.
func configInt(cfg map[string]any, key string) (int, bool) {
	v, ok := storage.ConfigLookup(cfg, key)
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	}
	return 0, false
}

// folderResult is what one folder reconcile did.
type folderResult struct {
	Path     string
	Added    int
	Updated  int
	Removed  int
	HeldBack int
	Entries  int
	// First: the folder had never been catalogued before this reconcile.
	First bool
	// Gone: the folder is not on the storage (any more).
	Gone bool
	// Deduped: the folder was already being reconciled; that run was told
	// to go round once more instead.
	Deduped bool
	// ChildDirs are the subfolders the listing held (canonical paths).
	ChildDirs []string
}

// folderRun is the per-folder lock's record of one running reconcile.
type folderRun struct {
	dirty  bool // another request arrived while it ran: go round once more
	opened bool // somebody opened the folder: watch it afterwards
}

type lazyItem struct {
	dir string
	why lazyReason
}

// lazyCatalogue is one lazily catalogued storage's engine.
type lazyCatalogue struct {
	s     *storageSyncer
	cfg   lazyConfig
	ps    *protocolsync.Syncer
	watch *watchSet
	// emit returns the realtime chain the server wired (Worker.AttachEmitter),
	// read at announce time because the hub is built after the syncers start.
	emit  func() ChangeEmitter
	label string

	mu       gosync.Mutex
	running  map[string]*folderRun
	queued   map[string]lazyReason
	queue    []lazyItem
	heldBack map[string]bool
	pairs    map[string]bool
	failed   map[string]time.Time
	visits   map[string]time.Time
	wake     chan struct{}

	lastActivity atomic.Int64
	filler       atomic.Value // string
	converged    atomic.Bool
	converging   atomic.Bool // checkConverged's once-only claim
	reconciles   atomic.Int64

	countsMu gosync.Mutex
	counts   model.CatalogueCounts
	countsAt time.Time

	wg gosync.WaitGroup
}

func newLazyCatalogue(s *storageSyncer, emit func() ChangeEmitter, fallback time.Duration) *lazyCatalogue {
	cfg := map[string]any{}
	if len(s.storage.ConfigJSON) > 0 {
		_ = jsonToMap(s.storage.ConfigJSON, &cfg)
	}
	lc := &lazyCatalogue{
		s:   s,
		cfg: lazyConfigOf(s.storage, cfg, fallback),
		// No write hook is ever fired through this Syncer (EnsureDirChainQuiet
		// only), so its origin is a log prefix, not a writehook constant.
		ps:       &protocolsync.Syncer{Store: s.store, Index: s.index, Origin: "sync: lazy"},
		emit:     emit,
		label:    metrics.StorageLabel(s.storage.ID),
		running:  map[string]*folderRun{},
		queued:   map[string]lazyReason{},
		heldBack: map[string]bool{},
		pairs:    map[string]bool{},
		failed:   map[string]time.Time{},
		visits:   map[string]time.Time{},
		wake:     make(chan struct{}, 1),
	}
	lc.filler.Store("off")
	lc.watch = newWatchSet(lc)
	return lc
}

// loopLazy is Loop for a `lazy` storage.
func (s *storageSyncer) loopLazy() {
	if _, ok := s.driver.(*local.Driver); !ok {
		slog.Warn("sync: lazy mode is for local storages only; falling back to poll",
			slog.String("storage", s.storage.Name), slog.String("driver", s.storage.Driver))
		s.loopPoll()
		return
	}
	lc := newLazyCatalogue(s, s.emitter, s.fallback)
	s.lazy.Store(lc)
	defer s.lazy.Store(nil)
	lc.run(s.ctx)
}

func (lc *lazyCatalogue) run(ctx context.Context) {
	st := lc.s.storage
	// Watches die with the process that placed them; whatever changed while
	// nobody watched is exactly what a reconcile on the next open is for.
	if n, err := lc.s.store.ResetCatalogueWatches(ctx, st.ID); err != nil {
		slog.Warn("sync: lazy: could not reset the previous process's watches",
			slog.String("storage", st.Name), slog.String("err", err.Error()))
	} else if n > 0 {
		slog.Info("sync: lazy: folders watched by the previous process will be reconciled when next opened",
			slog.String("storage", st.Name), slog.Int64("folders", n))
	}
	if err := lc.watch.start(); err != nil {
		slog.Warn("sync: lazy: no fsnotify watcher; visited folders are reconciled on every open instead",
			slog.String("storage", st.Name), slog.String("err", err.Error()))
	}
	slog.Info("sync: lazy catalogue started",
		slog.String("storage", st.Name),
		slog.String("fill", lc.cfg.fill),
		slog.Int("max_watches", lc.cfg.maxWatches),
		slog.Duration("watch_ttl", lc.cfg.watchTTL))

	for i := 0; i < 2; i++ {
		lc.wg.Add(1)
		go func() {
			defer lc.wg.Done()
			lc.serve(ctx)
		}()
	}
	lc.wg.Add(1)
	go func() {
		defer lc.wg.Done()
		lc.watch.loop(ctx)
	}()
	lc.wg.Add(1)
	go func() {
		defer lc.wg.Done()
		lc.pairLoop(ctx)
	}()
	if lc.cfg.fill == storage.LazyFillBackground {
		lc.wg.Add(1)
		go func() {
			defer lc.wg.Done()
			lc.fillLoop(ctx)
		}()
	}
	<-ctx.Done()
	lc.wg.Wait()
	lc.watch.close()
}

// ── the per-folder reconcile ────────────────────────────────────────────

// reconcile runs reconcileOnce for dir under the folder's lock: two
// reconciles of the same folder never run at once, and a request that
// arrives while one runs makes it go round once more when it finishes.
//
// ⚠ It takes no storage-wide lock. A full scan (RunOnce holds runMu) and any
// number of folder reconciles run side by side, and every create on both sides
// tolerates losing the race to the other (catalogueEntry, EnsureDirChain).
func (lc *lazyCatalogue) reconcile(ctx context.Context, dir string, why lazyReason) (folderResult, error) {
	dir = db.CatalogueFolderPath(dir)
	opened := why == reasonOpen
	lc.mu.Lock()
	if r := lc.running[dir]; r != nil {
		r.dirty = true
		r.opened = r.opened || opened
		lc.mu.Unlock()
		return folderResult{Path: dir, Deduped: true}, nil
	}
	run := &folderRun{opened: opened}
	lc.running[dir] = run
	lc.mu.Unlock()

	var (
		res     folderResult
		err     error
		first   bool
		changed int
	)
	for attempt := 0; ; attempt++ {
		// A folder somebody opened is watched BEFORE it is listed, so a change
		// that lands between the listing and the watch still raises an event.
		if run.opened {
			lc.watch.add(dir)
		}
		res, err = lc.reconcileOnce(ctx, dir, why)
		if err == nil {
			first = first || res.First
			changed += res.Added + res.Updated + res.Removed
		}
		lc.mu.Lock()
		again := run.dirty && err == nil && !res.Gone && attempt < 2
		run.dirty = false
		opened = run.opened
		if !again {
			delete(lc.running, dir)
		}
		lc.mu.Unlock()
		if !again {
			break
		}
	}
	lc.reconciles.Add(1)
	metrics.LazyReconciles.WithLabelValues(lc.label, string(why)).Inc()
	if err != nil || res.Gone {
		lc.watch.remove(dir)
		return res, err
	}
	if opened {
		lc.watch.confirm(ctx, dir)
		lc.visit(ctx, dir, true)
	}
	lc.announce(dir, first, changed, opened || why == reasonPair)
	return res, nil
}

// reconcileOnce lists one folder and applies the listing. See the package
// comment and docs/LAZY-CATALOGUE.md.
func (lc *lazyCatalogue) reconcileOnce(ctx context.Context, dir string, why lazyReason) (folderResult, error) {
	s := lc.s
	st := s.storage
	res := folderResult{Path: dir}
	if dir != "/" && s.rule.Skips(dir) {
		return res, errNotLazyFolder
	}
	// Finding a file is not putting it there — see RunOnce.
	ctx = quotastore.WithActor(quotastore.WithOwner(ctx, 0), 0)

	hash := pathkey.Hash(st.ID, dir)
	prior, err := s.store.GetCatalogueFolder(ctx, st.ID, hash)
	if err != nil {
		return res, err
	}
	res.First = !prior.Catalogued()

	objs, err := s.driver.List(ctx, dir)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// ⚠ Gone from the disk, and still NOTHING is removed here: the
			// folder's rows are its parent's business, and the parent's next
			// reconcile confirms the absence before it touches them.
			res.Gone = true
			return res, nil
		}
		return res, err
	}
	var parent *int64
	if dir != "/" {
		parent, err = lc.ps.EnsureDirChainQuiet(ctx, st, dir)
		if err != nil {
			return res, err
		}
		if parent == nil {
			return res, fmt.Errorf("sync: lazy: no catalogue row for %s", dir)
		}
	}

	markerFirst(objs)
	stamp := time.Now().UTC().Truncate(time.Second)
	c := &walkCounts{}
	seen := make(map[string]bool, len(objs))
	var found []model.CatalogueFolder
	for _, obj := range objs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if s.rule.Skips(obj.Path) {
			continue
		}
		seen[obj.Name] = true
		n, isDir, abandon := s.catalogueEntry(ctx, dir, parent, obj, c)
		if abandon {
			return res, errAbandoned
		}
		if n != nil && isDir {
			p := db.CatalogueFolderPath(obj.Path)
			found = append(found, model.CatalogueFolder{
				PathHash: pathkey.Hash(st.ID, p), Path: p, Depth: db.CatalogueFolderDepth(p),
			})
			res.ChildDirs = append(res.ChildDirs, p)
		}
	}
	if len(found) > 0 {
		if err := s.store.DiscoverCatalogueFolders(ctx, st.ID, found); err != nil {
			return res, err
		}
	}

	// The listing is applied: say so BEFORE the delete pass, which reads it
	// back and refuses to run on anything else.
	rec := &model.CatalogueFolder{
		StorageID: st.ID, PathHash: hash, Path: dir, Depth: db.CatalogueFolderDepth(dir),
		State: model.FolderCatalogued, ReconciledAt: &stamp, Entries: len(seen),
	}
	if prior != nil {
		rec.VisitedAt = prior.VisitedAt
		if lc.watch.has(dir) {
			rec.State, rec.WatchedAt = model.FolderWatched, prior.WatchedAt
		}
	}
	if err := s.store.RecordCatalogueFolder(ctx, rec); err != nil {
		return res, err
	}
	removed, held := lc.deletePass(ctx, dir, parent, seen, stamp)
	if held > 0 {
		rec.HeldBack = held
		if err := s.store.RecordCatalogueFolder(ctx, rec); err != nil {
			return res, err
		}
	}
	lc.setHeldBack(dir, held > 0)

	res.Added, res.Updated, res.Removed, res.HeldBack, res.Entries = c.added, c.updated, removed, held, len(seen)
	slog.Debug("sync: lazy: folder reconciled",
		slog.String("storage", st.Name), slog.String("path", dir), slog.String("reason", string(why)),
		slog.Int("entries", res.Entries), slog.Int("added", res.Added), slog.Int("updated", res.Updated),
		slog.Int("removed", res.Removed), slog.Int("held_back", res.HeldBack))
	return res, nil
}

// deletePass removes, from the catalogue, the direct children of dir that the
// listing just applied did not hold and that are confirmed gone. It returns
// how many rows went and how many candidates the guard held back.
//
// ⚠⚠ THE DELETION-SAFETY INVARIANT (docs/LAZY-CATALOGUE.md): a row is only
// ever removed because the folder that directly contains it was just listed,
// completely and successfully, and the row's entry was not in that listing and
// is confirmed gone. A folder that was never visited or reconciled is never
// treated as deleted, however old its rows are. Every clause below is one part
// of that sentence; the tests in lazy_safety_test.go break each one.
func (lc *lazyCatalogue) deletePass(ctx context.Context, dir string, parent *int64, seen map[string]bool, stamp time.Time) (removed, held int) {
	s := lc.s
	st := s.storage
	// 1. Only a folder whose state row says THIS listing was applied to it.
	// A caller that reaches here any other way — an event, a cleanup, a later
	// refactor — removes nothing.
	row, err := s.store.GetCatalogueFolder(ctx, st.ID, pathkey.Hash(st.ID, dir))
	if err != nil || !row.Catalogued() || !row.ReconciledAt.Equal(stamp) {
		slog.Warn("sync: lazy: refusing a delete pass on a folder this listing did not reconcile",
			slog.String("storage", st.Name), slog.String("path", dir))
		return 0, 0
	}
	// 2. Only the folder's DIRECT children. Rows further down belong to
	// folders of their own, listed or not.
	children, err := s.store.ListNodesByParent(ctx, st.ID, parent)
	if err != nil {
		return 0, 0
	}
	var candidates []*model.Node
	baseline := 0
	for _, n := range children {
		if n.DeletedAt != nil || s.rule.Skips(n.Path) {
			// Excluded and internal paths are never listed, so never seen:
			// "unseen" says nothing about them (issue #44).
			continue
		}
		baseline++
		if !seen[n.Name] {
			candidates = append(candidates, n)
		}
	}
	if len(candidates) == 0 {
		return 0, 0
	}
	// 3. The guard: a listing that came back short removes nothing.
	if !folderGuardOK(dir == "/", len(seen), baseline, len(candidates)) {
		metrics.LazyHeldBack.WithLabelValues(lc.label).Add(float64(len(candidates)))
		slog.Warn("sync: lazy: deletions held back, the listing saw too little of this folder",
			slog.String("storage", st.Name), slog.String("path", dir),
			slog.Int("listed", len(seen)), slog.Int("catalogued", baseline), slog.Int("missing", len(candidates)))
		return 0, len(candidates)
	}
	for _, n := range candidates {
		// 4. Each candidate confirmed gone by its own Stat — folders too.
		if !lc.confirmGone(ctx, n) {
			continue
		}
		batch := []*model.Node{n}
		if n.Type == model.NodeTypeDirectory {
			// 5. A candidate's subtree goes with it only because the
			// candidate itself is gone.
			if below, err := s.store.ListNodesUnder(ctx, st.ID, n.Path, false); err == nil && len(below) > 0 {
				batch = below
			}
		}
		// 6. In place, through the tombstone pass's own function: never a
		// path the scan rule skips, the file rows each Stat-confirmed again,
		// and never bytes — the trash purge leaves a path alone whose row was
		// deleted where it stood (trash.ownsBytesAt).
		removed += s.tombstone(ctx, batch)
		if n.Type == model.NodeTypeDirectory {
			p := db.CatalogueFolderPath(n.Path)
			_ = s.store.DeleteCatalogueFoldersUnder(ctx, st.ID, p)
			lc.watch.removeUnder(p)
			lc.setHeldBackUnder(p)
		}
	}
	return removed, 0
}

// folderGuardOK is the per-folder guard. isRoot: the storage root. seen: what
// the listing held; baseline: the live catalogued children; gone: how many of
// those the listing did not hold.
//
//   - The root listing EMPTY while the catalogue holds children is the
//     signature of an unmounted mount point, and removes nothing.
//   - Fewer than folderGuardFloor children vanishing is ordinary life.
//   - Otherwise the full scan's own ratio applies to the folder: a listing
//     that saw under 70% of what the catalogue holds (a network filesystem's
//     readdir coming back short) removes nothing.
func folderGuardOK(isRoot bool, seen, baseline, gone int) bool {
	if isRoot && seen == 0 && baseline > 0 {
		return false
	}
	if gone < folderGuardFloor {
		return true
	}
	return guardOK(seen, baseline)
}

// confirmGone is the delete pass's second opinion: an unstored (in-flight)
// upload is never a candidate, and anything else is gone only when its own
// Stat answers ErrNotFound. Unlike the full scan's confirmGone it asks for
// folders too — a local driver can stat a directory.
func (lc *lazyCatalogue) confirmGone(ctx context.Context, n *model.Node) bool {
	if n.TransferState != "" && n.TransferState != model.TransferStateStored {
		return false
	}
	key := n.StorageKey
	if key == "" {
		key = n.Path
	}
	_, err := lc.s.driver.Stat(ctx, key)
	return errors.Is(err, storage.ErrNotFound)
}

// announce tells whoever is looking what a reconcile found.
//
//   - The FIRST catalogue of a folder changed nothing on the storage: its
//     explorer was showing it from the disk already. A derived frame makes that
//     explorer re-list (now from the catalogue, with owners and thumbnails),
//     while mirrors and the change log ignore it. Only when a person or a pair
//     asked: the filler catalogues folders nobody is looking at.
//   - A later reconcile that added, changed or removed something found a real
//     change made outside filex: an ordinary frame, for everyone.
func (lc *lazyCatalogue) announce(dir string, first bool, changed int, someoneAsked bool) {
	var e ChangeEmitter
	if lc.emit != nil {
		e = lc.emit()
	}
	if e == nil {
		return
	}
	rel := strings.TrimPrefix(dir, "/")
	switch {
	case first && someoneAsked:
		e.EmitChange(lc.s.storage.ID, rel, realtime.ChangeEvent{Action: "modify", Derived: true})
	case !first && changed > 0:
		e.EmitChange(lc.s.storage.ID, rel, realtime.ChangeEvent{Action: "modify"})
	}
}

func (lc *lazyCatalogue) setHeldBack(dir string, held bool) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if held {
		lc.heldBack[dir] = true
	} else {
		delete(lc.heldBack, dir)
	}
}

func (lc *lazyCatalogue) setHeldBackUnder(dir string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	for d := range lc.heldBack {
		if d == dir || strings.HasPrefix(d, dir+"/") {
			delete(lc.heldBack, d)
		}
	}
}

func (lc *lazyCatalogue) isHeldBack(dir string) bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.heldBack[dir]
}

// visit stamps visited_at, at most once per lazyVisitWrite per folder unless
// forced.
func (lc *lazyCatalogue) visit(ctx context.Context, dir string, force bool) {
	now := time.Now().UTC()
	lc.mu.Lock()
	last, ok := lc.visits[dir]
	if !force && ok && now.Sub(last) < lazyVisitWrite {
		lc.mu.Unlock()
		return
	}
	lc.visits[dir] = now
	lc.mu.Unlock()
	st := lc.s.storage
	_ = lc.s.store.TouchCatalogueFolderVisit(context.WithoutCancel(ctx), st.ID, pathkey.Hash(st.ID, dir), now)
}

// ── requests: open, watch, pair ─────────────────────────────────────────

// noteActivity records that somebody used the storage just now.
func (lc *lazyCatalogue) noteActivity() {
	lc.lastActivity.Store(time.Now().UnixNano())
}

func (lc *lazyCatalogue) active() bool {
	last := lc.lastActivity.Load()
	return last > 0 && time.Since(time.Unix(0, last)) < LazyActiveWindow
}

// opened is a listing of dir. A folder that is watched and whose last
// reconcile removed everything it had to is current: its LRU position moves
// and that is all. Anything else is queued for a reconcile.
func (lc *lazyCatalogue) opened(ctx context.Context, dir string) {
	dir = db.CatalogueFolderPath(dir)
	lc.noteActivity()
	if lc.current(dir) {
		lc.watch.touch(dir)
		lc.visit(ctx, dir, false)
		return
	}
	lc.request(dir, reasonOpen)
}

// current reports whether the catalogue vouches for dir right now: it is
// watched, and its last reconcile held nothing back.
func (lc *lazyCatalogue) current(dir string) bool {
	return lc.watch.has(db.CatalogueFolderPath(dir)) && !lc.isHeldBack(db.CatalogueFolderPath(dir))
}

// rank orders requests: an open beats a watch event beats a pair folder.
func rank(why lazyReason) int {
	switch why {
	case reasonOpen:
		return 3
	case reasonWatch:
		return 2
	default:
		return 1
	}
}

// request queues a reconcile for the serve workers; a folder already queued is
// not queued twice (the stronger reason wins).
func (lc *lazyCatalogue) request(dir string, why lazyReason) {
	lc.mu.Lock()
	if prev, ok := lc.queued[dir]; ok {
		if rank(why) > rank(prev) {
			lc.queued[dir] = why
		}
		lc.mu.Unlock()
		return
	}
	lc.queued[dir] = why
	lc.queue = append(lc.queue, lazyItem{dir: dir, why: why})
	lc.mu.Unlock()
	select {
	case lc.wake <- struct{}{}:
	default:
	}
}

// next pops the highest-ranked queued request, or reports none.
func (lc *lazyCatalogue) next() (lazyItem, bool) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	best := -1
	for i, it := range lc.queue {
		if best < 0 || rank(lc.queued[it.dir]) > rank(lc.queued[lc.queue[best].dir]) {
			best = i
		}
	}
	if best < 0 {
		return lazyItem{}, false
	}
	it := lc.queue[best]
	it.why = lc.queued[it.dir]
	lc.queue = append(lc.queue[:best], lc.queue[best+1:]...)
	delete(lc.queued, it.dir)
	return it, true
}

func (lc *lazyCatalogue) pending() bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return len(lc.queue) > 0
}

// serve is one request worker. Two run per storage, so one huge folder does
// not hold up the next person's open.
func (lc *lazyCatalogue) serve(ctx context.Context) {
	for {
		it, ok := lc.next()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-lc.wake:
			case <-time.After(time.Second):
			}
			continue
		}
		if _, err := lc.reconcile(ctx, it.dir, it.why); err != nil && ctx.Err() == nil {
			slog.Info("sync: lazy: folder reconcile failed",
				slog.String("storage", lc.s.storage.Name), slog.String("path", it.dir),
				slog.String("reason", string(it.why)), slog.String("err", err.Error()))
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// subtree makes sure every folder under dir is catalogued — what a desktop
// sync pair needs, because the upload precondition it sends is checked against
// the catalogue. Breadth-first, one folder at a time through the same
// reconcile, ahead of the filler and behind every open; folders reconciled
// within the refresh interval are not listed again, only descended into.
func (lc *lazyCatalogue) subtree(ctx context.Context, dir string) {
	dir = db.CatalogueFolderPath(dir)
	if dir != "/" && lc.s.rule.Skips(dir) {
		return
	}
	lc.mu.Lock()
	if lc.pairs[dir] {
		lc.mu.Unlock()
		return
	}
	lc.pairs[dir] = true
	lc.mu.Unlock()
	lc.wg.Add(1)
	go func() {
		defer lc.wg.Done()
		defer func() {
			lc.mu.Lock()
			delete(lc.pairs, dir)
			lc.mu.Unlock()
		}()
		started := time.Now()
		folders := 0
		queue := []string{dir}
		for len(queue) > 0 {
			if ctx.Err() != nil {
				return
			}
			for lc.pending() && ctx.Err() == nil {
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Millisecond):
				}
			}
			d := queue[0]
			queue = queue[1:]
			children, ok := lc.freshChildren(ctx, d)
			if !ok {
				res, err := lc.reconcile(ctx, d, reasonPair)
				if err != nil || res.Gone {
					continue
				}
				if res.Deduped {
					// Somebody else is listing it right now; its children are
					// known once they are done.
					children, _ = lc.freshChildren(ctx, d)
				} else {
					children = res.ChildDirs
				}
			}
			folders++
			queue = append(queue, children...)
		}
		slog.Info("sync: lazy: a sync pair's folder is fully catalogued",
			slog.String("storage", lc.s.storage.Name), slog.String("path", dir),
			slog.Int("folders", folders), slog.Duration("took", time.Since(started)))
	}()
}

// pairLoop keeps the subtrees that desktop sync pairs mirror current. Every
// refresh interval each root a client watches recursively right now is walked
// again through subtree, which lists only the folders whose last listing is
// older than that interval — for a pair, what the poll mode is for a whole
// storage. Without it a change made outside filex inside a paired folder would
// reach the desktop in behaviour B only when somebody happened to open that
// folder (its folders are not watched: the fsnotify budget is for folders
// people visit).
func (lc *lazyCatalogue) pairLoop(ctx context.Context) {
	every := lc.cfg.refreshAge
	if every < LazyPairRefreshFloor {
		every = LazyPairRefreshFloor
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			lc.refreshPairs(ctx)
		}
	}
}

// refreshPairs walks every currently mirrored root once (see pairLoop).
func (lc *lazyCatalogue) refreshPairs(ctx context.Context) {
	if lc.s.pairs == nil {
		return
	}
	for _, root := range lc.s.pairs(lc.s.storage.ID) {
		lc.subtree(ctx, root)
	}
}

// freshChildren answers dir's subfolders from the catalogue when its last
// listing is younger than the refresh interval and nothing asked for it to be
// reconciled; ok is false when the folder has to be listed again.
func (lc *lazyCatalogue) freshChildren(ctx context.Context, dir string) ([]string, bool) {
	s := lc.s
	st := s.storage
	row, err := s.store.GetCatalogueFolder(ctx, st.ID, pathkey.Hash(st.ID, dir))
	if err != nil || !row.Catalogued() || row.ReconcileOnOpen || time.Since(*row.ReconciledAt) > lc.cfg.refreshAge {
		return nil, false
	}
	var parent *int64
	if dir != "/" {
		n, err := s.store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, dir))
		if err != nil || n == nil {
			return nil, false
		}
		parent = &n.ID
	}
	kids, err := s.store.ListNodesByParent(ctx, st.ID, parent)
	if err != nil {
		return nil, false
	}
	var out []string
	for _, k := range kids {
		if k.Type == model.NodeTypeDirectory && k.DeletedAt == nil && !s.rule.Skips(k.Path) {
			out = append(out, db.CatalogueFolderPath(k.Path))
		}
	}
	return out, true
}

// ── the background filler (behaviour A) ────────────────────────────────

// fillLoop catalogues the rest of the storage, then keeps unwatched folders
// fresh. Resumable: its work list is the catalogue_folders table.
func (lc *lazyCatalogue) fillLoop(ctx context.Context) {
	st := lc.s.storage
	var (
		batch      []lazyItem
		lastSizes  = time.Now()
		dirtySizes bool
		started    = time.Now()
		filled     int
	)
	lc.filler.Store("filling")
	for {
		if ctx.Err() != nil {
			return
		}
		if len(batch) == 0 {
			batch = lc.nextFillBatch(ctx)
		}
		if len(batch) == 0 {
			if dirtySizes {
				lc.recomputeSizes(ctx)
				dirtySizes = false
			}
			// Nothing to do: after convergence that is the steady state
			// ("converged" — the storage page says "Done"); before it, the
			// frontier is empty only while failed folders wait their retry.
			if lc.converged.Load() {
				lc.filler.Store("converged")
			} else {
				lc.filler.Store("idle")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(lazyIdleRecheck):
			}
			continue
		}
		if !lc.pace(ctx) {
			return
		}
		it := batch[0]
		batch = batch[1:]
		if lc.handledSince(ctx, it) {
			// An open, a pair or a watch event got there after the batch
			// was read: listing it again would be the same work twice.
			continue
		}
		if it.why == reasonRefresh {
			lc.filler.Store("refreshing")
		} else {
			lc.filler.Store("filling")
		}
		res, err := lc.reconcile(ctx, it.dir, it.why)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errNotLazyFolder) || (err == nil && res.Gone):
			// Excluded since it was discovered, or no longer there: it leaves
			// the work list. Only the work list — no catalogue row is touched.
			_ = lc.s.store.DeleteCatalogueFoldersUnder(ctx, st.ID, it.dir)
		case err != nil:
			lc.mu.Lock()
			lc.failed[it.dir] = time.Now()
			lc.mu.Unlock()
			slog.Info("sync: lazy: the filler could not list a folder; it will try again later",
				slog.String("storage", st.Name), slog.String("path", it.dir), slog.String("err", err.Error()))
		default:
			filled++
			if res.Added+res.Updated+res.Removed > 0 {
				dirtySizes = true
			}
		}
		if dirtySizes && time.Since(lastSizes) >= LazySizeCheckpoint {
			lc.recomputeSizes(ctx)
			lastSizes, dirtySizes = time.Now(), false
		}
		if len(batch) == 0 && !lc.converged.Load() {
			if lc.checkConverged(ctx) {
				slog.Info("sync: lazy: the whole storage is catalogued",
					slog.String("storage", st.Name), slog.Int("folders", filled), slog.Duration("took", time.Since(started)))
				dirtySizes = false
				lastSizes = time.Now()
			}
		}
	}
}

// nextFillBatch is the filler's next work: the root if it was never
// catalogued, then the frontier shallowest first, then (once converged) the
// unwatched folders whose last listing is older than the refresh interval.
func (lc *lazyCatalogue) nextFillBatch(ctx context.Context) []lazyItem {
	st := lc.s.storage
	counts, err := lc.s.store.CountCatalogueFolders(ctx, st.ID)
	if err != nil {
		return nil
	}
	if !counts.RootCatalogued {
		return []lazyItem{{dir: "/", why: reasonFill}}
	}
	rows, err := lc.s.store.ListCatalogueFolders(ctx, st.ID, model.CatalogueFolderFilter{
		State: model.FolderUncatalogued, Limit: LazyFillBatch * 4,
	})
	if err == nil {
		if items := lc.fillItems(rows, reasonFill); len(items) > 0 {
			return items
		}
	}
	if !lc.converged.Load() {
		lc.checkConverged(ctx)
	}
	cutoff := time.Now().UTC().Add(-lc.cfg.refreshAge)
	rows, err = lc.s.store.ListCatalogueFolders(ctx, st.ID, model.CatalogueFolderFilter{
		ReconciledBefore: &cutoff, Limit: LazyFillBatch,
	})
	if err != nil {
		return nil
	}
	return lc.fillItems(rows, reasonRefresh)
}

// handledSince reports whether a filler item was taken care of by somebody
// else after its batch was read — the batch is up to LazyFillBatch folders,
// and every open runs ahead of the filler, so the folder a person just opened
// is often in it. A frontier folder is done once it is catalogued; a refresh
// once its listing is younger than the refresh interval, or it is watched.
func (lc *lazyCatalogue) handledSince(ctx context.Context, it lazyItem) bool {
	st := lc.s.storage
	row, err := lc.s.store.GetCatalogueFolder(ctx, st.ID, pathkey.Hash(st.ID, it.dir))
	if err != nil || row == nil {
		return false
	}
	if it.why == reasonRefresh {
		return row.State == model.FolderWatched ||
			(row.ReconciledAt != nil && time.Since(*row.ReconciledAt) < lc.cfg.refreshAge)
	}
	return row.Catalogued()
}

// fillItems turns rows into work, leaving out folders that failed recently.
func (lc *lazyCatalogue) fillItems(rows []*model.CatalogueFolder, why lazyReason) []lazyItem {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	out := make([]lazyItem, 0, len(rows))
	for _, r := range rows {
		if t, ok := lc.failed[r.Path]; ok && time.Since(t) < lazyRetryFailed {
			continue
		}
		out = append(out, lazyItem{dir: r.Path, why: why})
		if len(out) >= LazyFillBatch {
			break
		}
	}
	return out
}

// checkConverged stamps the storage's last-synced time and recomputes folder
// sizes the first time nothing is left to catalogue.
func (lc *lazyCatalogue) checkConverged(ctx context.Context) bool {
	st := lc.s.storage
	counts, err := lc.s.store.CountCatalogueFolders(ctx, st.ID)
	if err != nil || !counts.RootCatalogued || counts.Uncatalogued > 0 {
		return false
	}
	// ⚠ `converged` is set LAST: whoever reads it (the filler's next batch, a
	// test waiting for convergence) may take it to mean the storage is stamped
	// and its folder sizes are computed. Set first, a reader stopped the
	// storage between the flag and the recompute and saw folders of size 0.
	if lc.converged.Load() || !lc.converging.CompareAndSwap(false, true) {
		return false
	}
	_ = lc.s.store.UpdateStorageSyncCursor(ctx, st.ID, time.Now().UTC().Truncate(time.Second), "")
	lc.recomputeSizes(ctx)
	lc.filler.Store("converged")
	lc.converged.Store(true)
	return true
}

// pace waits before the filler's next folder: behind every queued request,
// then a short breath while nobody is using the storage and a long one while
// somebody is. False when the storage is shutting down.
func (lc *lazyCatalogue) pace(ctx context.Context) bool {
	for lc.pending() {
		lc.filler.Store("paused")
		select {
		case <-ctx.Done():
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
	pause := LazyFillIdlePause
	if lc.active() {
		pause = LazyFillBusyPause
		lc.filler.Store("paused")
	}
	if pause <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(pause):
		return true
	}
}

func (lc *lazyCatalogue) recomputeSizes(ctx context.Context) {
	if err := RecomputeFolderSizes(ctx, lc.s.store, lc.s.storage.ID); err != nil && ctx.Err() == nil {
		slog.Warn("sync: lazy: folder-size recompute",
			slog.String("storage", lc.s.storage.Name), slog.String("err", err.Error()))
	}
}

// ── coverage ────────────────────────────────────────────────────────────

// countsNow reads the per-state counts, at most every two seconds.
func (lc *lazyCatalogue) countsNow(ctx context.Context) model.CatalogueCounts {
	lc.countsMu.Lock()
	defer lc.countsMu.Unlock()
	if time.Since(lc.countsAt) < 2*time.Second {
		return lc.counts
	}
	c, err := lc.s.store.CountCatalogueFolders(ctx, lc.s.storage.ID)
	if err != nil {
		return lc.counts
	}
	lc.counts, lc.countsAt = c, time.Now()
	metrics.LazyFolders.WithLabelValues(lc.label, string(model.FolderUncatalogued)).Set(float64(c.Uncatalogued))
	metrics.LazyFolders.WithLabelValues(lc.label, string(model.FolderCatalogued)).Set(float64(c.Catalogued))
	metrics.LazyFolders.WithLabelValues(lc.label, string(model.FolderWatched)).Set(float64(c.Watched))
	return c
}

// coverage describes how much of the storage the catalogue covers.
func (lc *lazyCatalogue) coverage(ctx context.Context) *CatalogueCoverage {
	c := lc.countsNow(ctx)
	cov := &CatalogueCoverage{
		Mode:              string(model.SyncModeLazy),
		Fill:              lc.cfg.fill,
		CataloguedFolders: c.Catalogued + c.Watched,
		PendingFolders:    c.Uncatalogued,
		WatchedFolders:    lc.watch.count(),
		MaxWatches:        lc.cfg.maxWatches,
		Filler:            lc.filler.Load().(string),
		HeldBackFolders:   c.HeldBack,
		Reconciles:        lc.reconciles.Load(),
	}
	cov.Complete = c.RootCatalogued && c.Uncatalogued == 0
	switch {
	case cov.Complete:
	case lc.cfg.fill == storage.LazyFillOnOpen:
		cov.Reason = CoverageVisitedOnly
	default:
		cov.Reason = CoverageFilling
	}
	return cov
}

// sizePartial reports, for each folder, whether its catalogued size leaves
// something out: the folder itself was never listed, or a folder below it is
// still uncatalogued. At most max folders are asked about; the rest are
// reported partial when the storage's coverage is incomplete.
func (lc *lazyCatalogue) sizePartial(ctx context.Context, dirs []string, max int) map[string]bool {
	out := make(map[string]bool, len(dirs))
	if len(dirs) == 0 {
		return out
	}
	c := lc.countsNow(ctx)
	if c.RootCatalogued && c.Uncatalogued == 0 {
		// Everything discovered is catalogued; only a folder with no state
		// of its own (never listed at all) is partial.
		for _, d := range dirs {
			d = db.CatalogueFolderPath(d)
			row, err := lc.s.store.GetCatalogueFolder(ctx, lc.s.storage.ID, pathkey.Hash(lc.s.storage.ID, d))
			out[d] = err != nil || !row.Catalogued()
		}
		return out
	}
	st := lc.s.storage
	for i, d := range dirs {
		d = db.CatalogueFolderPath(d)
		if i >= max {
			out[d] = true
			continue
		}
		row, err := lc.s.store.GetCatalogueFolder(ctx, st.ID, pathkey.Hash(st.ID, d))
		if err != nil || !row.Catalogued() {
			out[d] = true
			continue
		}
		below, err := lc.s.store.HasUncataloguedUnder(ctx, st.ID, d)
		out[d] = err != nil || below
	}
	return out
}

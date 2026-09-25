package sync

// ⚠⚠ The lazy catalogue's deletion-safety invariant (docs/LAZY-CATALOGUE.md):
//
//	A row is only ever removed because the folder that directly contains it
//	was just listed, completely and successfully, and the row's entry was not
//	in that listing and is confirmed gone. A folder that was never visited or
//	reconciled is never treated as deleted, however old its rows are.
//
// Every test here pins one clause of that sentence, and each was run against
// the code with its guard reverted (the "break" line in each comment) and seen
// to go red: rows wrongly removed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// lazyLab is one lazily catalogued local storage with its engine built but
// not started: a test drives reconcile itself, synchronously.
type lazyLab struct {
	lc    *lazyCatalogue
	store db.Store
	pool  *sql.DB
	st    *model.Storage
	root  string
	ctx   context.Context
}

func newLazyLab(t *testing.T, extra map[string]any) *lazyLab {
	t.Helper()
	return newLazyLabOn(t, extra, nil)
}

// newLazyLabOn is newLazyLab whose catalogue writes through wrap(store, pool)
// (nil: the store itself); the lab's own reads and seeds use the raw store.
func newLazyLabOn(t *testing.T, extra map[string]any, wrap func(db.Store, *sql.DB) db.Store) *lazyLab {
	t.Helper()
	pool, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg := map[string]any{"root": root}
	for k, v := range extra {
		cfg[k] = v
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "tembel", Driver: "local", MountPath: "/tembel", ConfigJSON: raw,
		SyncMode: model.SyncModeLazy, Enabled: true,
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), cfg))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	through := store
	if wrap != nil {
		through = wrap(store, pool)
	}
	s := &storageSyncer{store: through, storage: st, driver: drv, rule: ruleFor(st, cfg), ctx: ctx, fallback: time.Minute}
	lc := newLazyCatalogue(s, nil, time.Minute)
	s.lazy.Store(lc)
	require.NoError(t, lc.watch.start())
	t.Cleanup(lc.watch.close)
	return &lazyLab{lc: lc, store: store, pool: pool, st: st, root: root, ctx: ctx}
}

func (l *lazyLab) write(t *testing.T, rel, body string) {
	t.Helper()
	abs := filepath.Join(l.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

func (l *lazyLab) mkdir(t *testing.T, rel string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(l.root, filepath.FromSlash(rel)), 0o755))
}

// seed writes a catalogue row the way an earlier full scan (or a write
// through filex) left it, whether or not the storage still has the object.
func (l *lazyLab) seed(t *testing.T, p string, typ model.NodeType) *model.Node {
	t.Helper()
	var parent *int64
	if dir := filepath.ToSlash(filepath.Dir(p)); dir != "/" && dir != "." {
		pr := l.row(dir)
		require.NotNil(t, pr, "seed the parent %s first", dir)
		parent = &pr.ID
	}
	n, err := l.store.CreateNode(context.Background(), &model.Node{
		StorageID: l.st.ID, ParentID: parent, Name: filepath.Base(p), Path: p,
		PathHash: pathkey.Hash(l.st.ID, p), StorageKey: p, Type: typ, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

func (l *lazyLab) row(p string) *model.Node {
	n, _ := l.store.GetNodeByPath(context.Background(), l.st.ID, pathkey.Hash(l.st.ID, p))
	return n
}

func (l *lazyLab) folder(t *testing.T, p string) *model.CatalogueFolder {
	t.Helper()
	f, err := l.store.GetCatalogueFolder(context.Background(), l.st.ID, pathkey.Hash(l.st.ID, p))
	require.NoError(t, err)
	return f
}

func (l *lazyLab) reconcile(t *testing.T, dir string) folderResult {
	t.Helper()
	res, err := l.lc.reconcile(l.ctx, dir, reasonOpen)
	require.NoError(t, err)
	return res
}

// ── the invariant, clause by clause ─────────────────────────────────────

// A folder nobody visited keeps every row, even rows whose files are long gone,
// while its parent and its siblings are reconciled around it. Visiting it is
// what removes them — so the test is not passing because nothing ever removes
// anything.
//
// Break: make deletePass take its candidates from ListNodesUnder(dir) (every
// row below the folder) instead of ListNodesByParent (its direct children) —
// reconciling /p then removes /p/arsiv/eski-1.txt and eski-2.txt.
func TestLazySafety_NeverVisitedFolderKeepsItsRows(t *testing.T) {
	l := newLazyLab(t, nil)
	l.write(t, "p/a/x.txt", "x")
	l.write(t, "p/b/y.txt", "y")
	l.mkdir(t, "p/arsiv") // on disk, and empty now
	l.seed(t, "/p", model.NodeTypeDirectory)
	arsiv := l.seed(t, "/p/arsiv", model.NodeTypeDirectory)
	l.seed(t, "/p/arsiv/eski-1.txt", model.NodeTypeFile)
	l.seed(t, "/p/arsiv/eski-2.txt", model.NodeTypeFile)

	l.reconcile(t, "/")
	l.reconcile(t, "/p")
	l.reconcile(t, "/p/a")
	l.reconcile(t, "/p/b")
	l.reconcile(t, "/p")

	require.NotNil(t, l.row("/p/a/x.txt"))
	require.NotNil(t, l.row("/p/b/y.txt"))
	assert.NotNil(t, l.row("/p/arsiv/eski-1.txt"), "a folder that was never listed is never treated as deleted")
	assert.NotNil(t, l.row("/p/arsiv/eski-2.txt"), "a folder that was never listed is never treated as deleted")
	assert.Equal(t, arsiv.ID, l.row("/p/arsiv").ID, "the folder itself is still on disk and keeps its row")
	assert.Equal(t, model.FolderUncatalogued, l.folder(t, "/p/arsiv").State)

	res := l.reconcile(t, "/p/arsiv")
	assert.Equal(t, 2, res.Removed, "listing the folder is what may remove its rows")
	assert.Nil(t, l.row("/p/arsiv/eski-1.txt"))
	assert.Nil(t, l.row("/p/arsiv/eski-2.txt"))
}

// The delete pass itself refuses a folder this listing did not reconcile: a
// caller that reaches it any other way — never listed, or listed at another
// time — removes nothing.
//
// Break: drop clause 1 of deletePass (the state-row check) — both calls below
// then remove the rows.
func TestLazySafety_DeletePassRefusesAFolderThisListingDidNotReconcile(t *testing.T) {
	l := newLazyLab(t, nil)
	l.mkdir(t, "u")
	u := l.seed(t, "/u", model.NodeTypeDirectory)
	l.seed(t, "/u/yok.txt", model.NodeTypeFile)
	ctx := l.ctx

	removed, held := l.lc.deletePass(ctx, "/u", &u.ID, map[string]bool{}, time.Now().UTC().Truncate(time.Second), nil)
	assert.Zero(t, removed, "never reconciled: no delete pass")
	assert.Zero(t, held)
	require.NotNil(t, l.row("/u/yok.txt"))

	// Catalogued once, a while ago — and this is not that listing.
	then := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, l.store.RecordCatalogueFolder(ctx, &model.CatalogueFolder{
		StorageID: l.st.ID, PathHash: pathkey.Hash(l.st.ID, "/u"), Path: "/u", Depth: 1,
		State: model.FolderCatalogued, ReconciledAt: &then,
	}))
	removed, _ = l.lc.deletePass(ctx, "/u", &u.ID, map[string]bool{}, time.Now().UTC().Truncate(time.Second), nil)
	assert.Zero(t, removed, "a listing the state row does not record is not a listing the pass may act on")
	assert.NotNil(t, l.row("/u/yok.txt"))
}

// shortList is a local driver whose listing of one folder leaves an entry out
// — a network filesystem's readdir coming back short — while Stat still sees it.
type shortList struct {
	*local.Driver
	dir, hide string
}

func (d shortList) List(ctx context.Context, p string) ([]storage.Object, error) {
	objs, err := d.Driver.List(ctx, p)
	if err != nil || db.CatalogueFolderPath(p) != d.dir {
		return objs, err
	}
	out := objs[:0]
	for _, o := range objs {
		if o.Name != d.hide {
			out = append(out, o)
		}
	}
	return out, nil
}

// A child folder is removed only when it is confirmed gone by its own Stat,
// and its subtree only with it. A folder the listing merely missed keeps
// itself and everything below it.
//
// Break: make lazyCatalogue.confirmGone answer true for folders without a Stat
// (as the full scan's confirmGone does) — the missed folder and the file below
// it are removed.
func TestLazySafety_AFolderTheListingMissedIsNotRemoved(t *testing.T) {
	l := newLazyLab(t, nil)
	l.write(t, "p/kalan/dosya.txt", "burada")
	l.mkdir(t, "p/diger")
	l.reconcile(t, "/")
	l.reconcile(t, "/p")
	l.reconcile(t, "/p/kalan")
	require.NotNil(t, l.row("/p/kalan/dosya.txt"))
	// A folder that really is gone, with a row below it.
	l.seed(t, "/p/silinen", model.NodeTypeDirectory)
	l.seed(t, "/p/silinen/icinde.txt", model.NodeTypeFile)

	l.lc.s.driver = shortList{Driver: l.lc.s.driver.(*local.Driver), dir: "/p", hide: "kalan"}
	res := l.reconcile(t, "/p")

	assert.NotNil(t, l.row("/p/kalan"), "missed by the listing, found by Stat: kept")
	assert.NotNil(t, l.row("/p/kalan/dosya.txt"), "and its subtree with it")
	assert.Nil(t, l.row("/p/silinen"), "gone from the disk: removed")
	assert.Nil(t, l.row("/p/silinen/icinde.txt"), "and its subtree with it")
	assert.Equal(t, 2, res.Removed)
}

// The storage root listing EMPTY while the catalogue holds children is an
// unmounted mount point, not a storage somebody emptied: nothing goes, the
// folder records what it held back, and it is no longer current.
//
// Break: drop the isRoot clause of folderGuardOK — four children is under the
// floor, so all four are removed.
func TestLazySafety_AnEmptyRootRemovesNothing(t *testing.T) {
	l := newLazyLab(t, nil)
	for _, rel := range []string{"a.txt", "b.txt", "c.txt", "klasor/d.txt"} {
		l.write(t, rel, "x")
	}
	l.reconcile(t, "/")
	require.NotNil(t, l.row("/klasor"))

	entries, err := os.ReadDir(l.root)
	require.NoError(t, err)
	for _, e := range entries {
		require.NoError(t, os.RemoveAll(filepath.Join(l.root, e.Name())))
	}
	res := l.reconcile(t, "/")

	assert.Zero(t, res.Removed)
	assert.Equal(t, 4, res.HeldBack)
	for _, p := range []string{"/a.txt", "/b.txt", "/c.txt", "/klasor"} {
		assert.NotNil(t, l.row(p), "%s survives an empty root listing", p)
	}
	assert.Equal(t, 4, l.folder(t, "/").HeldBack)
	assert.True(t, l.lc.isHeldBack("/"), "a folder that held deletions back is listed from the disk")
}

// Ten or more children vanishing at once while the listing saw under 70% of
// the folder removes nothing (a readdir that came back short). A few files
// deleted outside filex is ordinary life and goes through.
//
// Break: drop the guardOK clause of folderGuardOK — fifteen rows are removed.
func TestLazySafety_AShortListingOfABigFolderRemovesNothing(t *testing.T) {
	l := newLazyLab(t, nil)
	for i := 0; i < 20; i++ {
		l.write(t, "buyuk/f"+string(rune('a'+i))+".txt", "x")
	}
	l.reconcile(t, "/")
	l.reconcile(t, "/buyuk")

	for i := 5; i < 20; i++ {
		require.NoError(t, os.Remove(filepath.Join(l.root, "buyuk", "f"+string(rune('a'+i))+".txt")))
	}
	res := l.reconcile(t, "/buyuk")
	assert.Zero(t, res.Removed, "15 of 20 vanished: held back")
	assert.Equal(t, 15, res.HeldBack)
	assert.NotNil(t, l.row("/buyuk/ft.txt"))

	// Put most of them back and delete three: three go.
	for i := 5; i < 20; i++ {
		l.write(t, "buyuk/f"+string(rune('a'+i))+".txt", "x")
	}
	for i := 0; i < 3; i++ {
		require.NoError(t, os.Remove(filepath.Join(l.root, "buyuk", "f"+string(rune('a'+i))+".txt")))
	}
	res = l.reconcile(t, "/buyuk")
	assert.Equal(t, 3, res.Removed)
	assert.Zero(t, res.HeldBack)
	assert.False(t, l.lc.isHeldBack("/buyuk"), "a reconcile that removed everything it had to clears the mark")
	assert.Nil(t, l.row("/buyuk/fa.txt"))
}

// An upload whose bytes are still on their way, and a row a scan exclusion
// covers, are never candidates — whatever the listing says.
//
// Break: drop the transfer-state check in lazyCatalogue.confirmGone, or the
// rule.Skips check in deletePass's candidate loop.
func TestLazySafety_UnstoredAndExcludedRowsAreNeverCandidates(t *testing.T) {
	l := newLazyLab(t, map[string]any{"scan_exclude": "node_modules"})
	l.write(t, "kod/main.go", "package main")
	l.reconcile(t, "/")
	l.reconcile(t, "/kod")
	nm := l.seed(t, "/kod/node_modules", model.NodeTypeDirectory)
	_ = nm
	l.seed(t, "/kod/node_modules/paket.js", model.NodeTypeFile)
	up := l.seed(t, "/kod/yukleniyor.bin", model.NodeTypeFile)
	require.NoError(t, l.store.SetNodeTransferState(l.ctx, up.ID, model.TransferStateStaged))

	res := l.reconcile(t, "/kod")
	assert.Zero(t, res.Removed)
	assert.NotNil(t, l.row("/kod/node_modules"), "an excluded folder is never listed, so never 'unseen'")
	assert.NotNil(t, l.row("/kod/node_modules/paket.js"))
	assert.NotNil(t, l.row("/kod/yukleniyor.bin"), "an upload still in flight is not a deletion")
}

// A watched folder that itself goes away does not take its rows with it: the
// watch is released and the parent's reconcile decides.
func TestLazySafety_AWatchedFolderThatWentAwayKeepsItsRowsUntilItsParentIsListed(t *testing.T) {
	l := newLazyLab(t, nil)
	l.write(t, "izlenen/a.txt", "x")
	l.write(t, "komsu.txt", "the root must not list empty: that is the unmount guard's case")
	l.reconcile(t, "/")
	res, err := l.lc.reconcile(l.ctx, "/izlenen", reasonOpen)
	require.NoError(t, err)
	require.False(t, res.Gone)
	require.NoError(t, os.RemoveAll(filepath.Join(l.root, "izlenen")))

	res, err = l.lc.reconcile(l.ctx, "/izlenen", reasonWatch)
	require.NoError(t, err)
	assert.True(t, res.Gone)
	assert.NotNil(t, l.row("/izlenen/a.txt"), "a folder's own reconcile never removes it")
	assert.False(t, l.lc.watch.has("/izlenen"), "its watch is released")

	res = l.reconcile(t, "/")
	assert.Equal(t, 2, res.Removed, "the parent's listing confirms it gone: the folder and its file")
	assert.Nil(t, l.row("/izlenen"))
	assert.Nil(t, l.folder(t, "/izlenen"), "and its catalogue state with it")
}

// The lazy engine never sweeps the storage by seen_at. Rows in a folder
// nobody listed carry ancient seen_at values; a storage-wide "not seen since"
// pass (the full scan's tombstone) would read every one of them as deleted.
// No file of the engine may ask for one.
//
// Break: call ListStaleNodes (and tombstone the answer) anywhere in the lazy
// engine — the convergence step is where it would look natural — and this
// goes red before any rows do.
func TestLazySafety_NothingSweepsBySeenAt(t *testing.T) {
	for _, f := range []string{"lazy.go", "lazy_watch.go", "lazy_api.go"} {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, banned := range []string{"ListStaleNodes", "ListStaleNodesUnder", "previousSeenCount"} {
			assert.NotContains(t, string(src), banned+"(", "%s must not sweep by seen_at (%s)", f, banned)
		}
	}
}

// The whole engine — workers, watcher, the open path through the Worker — on
// a storage in behaviour B: a folder nobody opens keeps rows whose files are
// long gone, while the folder next to it is opened, catalogued and watched.
func TestLazySafety_OnOpenEngineLeavesUnvisitedFoldersAlone(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg := map[string]any{"root": root, "lazy_fill": "on_open"}
	raw, _ := json.Marshal(cfg)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "tembel", Driver: "local", MountPath: "/tembel", ConfigJSON: raw, SyncMode: model.SyncModeLazy, Enabled: true,
	})
	require.NoError(t, err)
	for _, rel := range []string{"acik/1.txt", "acik/2.txt"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("x"), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "kapali"), 0o755))
	seedAt := func(p string, parent *int64, typ model.NodeType) *model.Node {
		n, err := store.CreateNode(context.Background(), &model.Node{StorageID: st.ID, ParentID: parent, Name: filepath.Base(p), Path: p,
			PathHash: pathkey.Hash(st.ID, p), StorageKey: p, Type: typ, SyncState: model.SyncStateSynced})
		require.NoError(t, err)
		return n
	}
	kapali := seedAt("/kapali", nil, model.NodeTypeDirectory)
	seedAt("/kapali/eski.txt", &kapali.ID, model.NodeTypeFile)

	w := New(store)
	require.NoError(t, w.AddStorage(context.Background(), st))
	t.Cleanup(w.Stop)
	require.Eventually(t, func() bool { return w.lazyOf(st.ID) != nil }, 5*time.Second, 10*time.Millisecond)
	w.CatalogueOpened(st.ID, "")
	w.CatalogueOpened(st.ID, "acik")
	require.Eventually(t, func() bool {
		_, cur := w.CatalogueCurrent(st.ID, "/acik")
		return cur
	}, 10*time.Second, 20*time.Millisecond, "an opened folder is catalogued and watched")
	n, _ := store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/acik/2.txt"))
	require.NotNil(t, n)
	time.Sleep(200 * time.Millisecond)
	still, _ := store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/kapali/eski.txt"))
	assert.NotNil(t, still, "nobody opened /kapali: its rows are not the engine's to judge")
	cov := w.CatalogueCoverage(context.Background(), st)
	require.NotNil(t, cov)
	assert.Equal(t, CoverageVisitedOnly, cov.Reason)
}

// setFastLazy runs the filler and the watch debounce at test speed.
func setFastLazy() func() {
	idle, busy, quiet, maxWait, batch := LazyFillIdlePause, LazyFillBusyPause, LazyWatchQuiet, LazyWatchMaxWait, LazyFillBatch
	LazyFillIdlePause, LazyFillBusyPause, LazyWatchQuiet, LazyWatchMaxWait = 0, time.Millisecond, 100*time.Millisecond, 400*time.Millisecond
	return func() {
		LazyFillIdlePause, LazyFillBusyPause, LazyWatchQuiet, LazyWatchMaxWait, LazyFillBatch = idle, busy, quiet, maxWait, batch
	}
}

// folderGuardOK's table, so a change to the thresholds is a deliberate one.
func TestFolderGuardOK(t *testing.T) {
	for _, c := range []struct {
		root                 bool
		seen, baseline, gone int
		ok                   bool
	}{
		{true, 0, 4, 4, false},     // unmounted mount point
		{false, 0, 4, 4, true},     // a subfolder emptied by hand
		{false, 1, 2, 1, true},     // one of two files deleted
		{false, 5, 20, 15, false},  // a readdir that came back short
		{false, 17, 20, 3, true},   // three of twenty deleted
		{false, 90, 100, 10, true}, // ten of a hundred: over the floor, ratio fine
		{false, 0, 0, 0, true},     // nothing to judge
	} {
		assert.Equal(t, c.ok, folderGuardOK(c.root, c.seen, c.baseline, c.gone), "%+v", c)
	}
}

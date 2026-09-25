package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The watch budget: past lazy_max_watches the folder opened longest ago loses
// its watch. Its rows stay, the row says "reconcile on next open", it is no
// longer current (so it is listed from the disk), and opening it again
// reconciles it before it is trusted.
//
// Break: make watchSet.add skip the eviction loop — three watches on a budget
// of two, and the first folder stays current.
func TestLazyWatch_BudgetEvictsTheLeastRecentlyOpened(t *testing.T) {
	l := newLazyLab(t, map[string]any{"lazy_max_watches": 2})
	for _, d := range []string{"bir", "iki", "uc"} {
		l.write(t, d+"/dosya.txt", d)
	}
	l.reconcile(t, "/")
	l.reconcile(t, "/bir")
	l.reconcile(t, "/iki")
	require.True(t, l.lc.current("/bir"))
	require.True(t, l.lc.current("/iki"))

	l.reconcile(t, "/uc")
	assert.Equal(t, 2, l.lc.watch.count(), "the budget holds")
	assert.False(t, l.lc.current("/bir"), "the least recently opened folder lost its watch")
	assert.True(t, l.lc.current("/iki"))
	assert.True(t, l.lc.current("/uc"))
	bir := l.folder(t, "/bir")
	assert.Equal(t, model.FolderCatalogued, bir.State)
	assert.True(t, bir.ReconcileOnOpen, "an evicted watch means: reconcile before trusting it again")
	assert.Nil(t, bir.WatchedAt)
	assert.NotNil(t, l.row("/bir/dosya.txt"), "eviction never takes a row with it")

	// Changed while nobody watched it — the next open finds out.
	l.write(t, "bir/yeni.txt", "x")
	res := l.reconcile(t, "/bir")
	assert.Equal(t, 1, res.Added)
	assert.True(t, l.lc.current("/bir"))
	assert.False(t, l.folder(t, "/bir").ReconcileOnOpen)
}

// A folder nobody has opened for lazy_watch_ttl loses its watch.
func TestLazyWatch_TTLReleasesIdleWatches(t *testing.T) {
	l := newLazyLab(t, map[string]any{"lazy_watch_ttl": 5})
	l.write(t, "eski/a.txt", "a")
	l.write(t, "taze/b.txt", "b")
	l.reconcile(t, "/")
	l.reconcile(t, "/eski")
	l.reconcile(t, "/taze")
	l.lc.watch.touch("/taze")

	l.lc.watch.mu.Lock()
	l.lc.watch.byDir["/eski"].Value.(*watchEntry).lastUsed = time.Now().Add(-6 * time.Minute)
	l.lc.watch.mu.Unlock()
	l.lc.watch.sweep(time.Now())

	assert.False(t, l.lc.current("/eski"), "unopened past the TTL: released")
	assert.True(t, l.lc.current("/taze"))
	assert.True(t, l.folder(t, "/eski").ReconcileOnOpen)
}

// A change made outside filex in a watched folder is caught by fsnotify and
// reconciled — that folder alone, never a full scan — and announced as a real
// change (it reaches explorers and the desktop's tree_change watchers). The
// first catalogue of a folder somebody opened is announced as derived only.
func TestLazyWatch_AnOutsideChangeIsReconciledAndAnnounced(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	l := newLazyLab(t, nil)
	fr := &frames{}
	l.lc.emit = func() ChangeEmitter { return fr }
	l.write(t, "izle/a.txt", "a")
	l.write(t, "komsu/b.txt", "b")
	ctx, cancel := context.WithCancel(l.ctx)
	defer cancel()
	go l.lc.watch.loop(ctx)
	go l.lc.serve(ctx)

	l.reconcile(t, "/")
	l.reconcile(t, "/izle")
	l.reconcile(t, "/komsu")
	assert.Contains(t, fr.list(), "derived:izle", "a first catalogue changes nothing on disk")

	require.NoError(t, os.WriteFile(filepath.Join(l.root, "izle", "yeni.txt"), []byte("yeni"), 0o644))
	require.Eventually(t, func() bool { return l.row("/izle/yeni.txt") != nil }, 10*time.Second, 20*time.Millisecond,
		"the watch caught the new file")
	require.Eventually(t, func() bool {
		for _, f := range fr.list() {
			if f == "real:izle" {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "and announced it as a change")

	require.NoError(t, os.Remove(filepath.Join(l.root, "izle", "a.txt")))
	require.Eventually(t, func() bool { return l.row("/izle/a.txt") == nil }, 10*time.Second, 20*time.Millisecond,
		"a file deleted outside filex leaves the catalogue")
	assert.NotNil(t, l.row("/komsu/b.txt"))
	run, _ := l.store.GetLastSyncRun(context.Background(), l.st.ID)
	assert.Nil(t, run, "no full scan ever ran")
}

// Watches die with the process that placed them. A new engine over the same
// database demotes every "watched" row to "catalogued, reconcile on next
// open": nothing is current until it has been listed again, and the rows stay.
//
// Break: drop the ResetCatalogueWatches call at the top of lazyCatalogue.run
// — the folder still reads as watched by a process that is gone.
func TestLazyWatch_ARestartForgetsEveryWatch(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	l := newLazyLab(t, map[string]any{"lazy_fill": "on_open"})
	l.write(t, "izli/a.txt", "a")
	l.reconcile(t, "/")
	l.reconcile(t, "/izli")
	require.Equal(t, model.FolderWatched, l.folder(t, "/izli").State)

	s2 := &storageSyncer{store: l.store, storage: l.st, driver: l.lc.s.driver, rule: l.lc.s.rule, ctx: l.ctx, fallback: time.Minute}
	lc2 := newLazyCatalogue(s2, nil, time.Minute)
	s2.lazy.Store(lc2)
	ctx, cancel := context.WithCancel(l.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lc2.run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	require.Eventually(t, func() bool {
		f := l.folder(t, "/izli")
		return f != nil && f.State == model.FolderCatalogued && f.ReconcileOnOpen && f.WatchedAt == nil
	}, 5*time.Second, 10*time.Millisecond, "the old process's watch is forgotten")
	assert.False(t, lc2.current("/izli"), "and the new engine does not vouch for the folder")
	assert.NotNil(t, l.row("/izli/a.txt"), "the rows stay")

	lc2.opened(context.Background(), "/izli")
	require.Eventually(t, func() bool { return lc2.current("/izli") }, 10*time.Second, 10*time.Millisecond,
		"the next open reconciles it and watches it again")
	assert.False(t, l.folder(t, "/izli").ReconcileOnOpen)
}

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

// runFillFor runs the filler loop until it converges or the deadline passes.
func runFillFor(t *testing.T, l *lazyLab, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(l.ctx, d)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.lc.fillLoop(ctx)
	}()
	for ctx.Err() == nil && !l.lc.converged.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func makeTree(t *testing.T, l *lazyLab, dirs, files int) int {
	t.Helper()
	n := 0
	for d := 0; d < dirs; d++ {
		for f := 0; f < files; f++ {
			l.write(t, filepath.Join("d"+itoa(d), "alt", "f"+itoa(f)+".txt"), "x")
			n++
		}
	}
	return n
}

func itoa(i int) string { return string(rune('a'+i/26)) + string(rune('a'+i%26)) }

// Behaviour A: the filler catalogues the whole tree, stamps the storage's
// last-synced time when it is done, computes folder sizes, and coverage says
// complete. Scan exclusions stay out.
func TestLazyFill_ConvergesOnTheWholeTree(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	l := newLazyLab(t, map[string]any{"scan_exclude": "gizli"})
	makeTree(t, l, 8, 6)
	l.write(t, "gizli/sir.txt", "x")

	runFillFor(t, l, 20*time.Second)
	require.True(t, l.lc.converged.Load(), "the filler converges")
	for d := 0; d < 8; d++ {
		for f := 0; f < 6; f++ {
			require.NotNil(t, l.row("/d"+itoa(d)+"/alt/f"+itoa(f)+".txt"))
		}
	}
	assert.Nil(t, l.row("/gizli"), "an excluded folder is never catalogued")
	assert.Nil(t, l.folder(t, "/gizli"), "nor enters the work list")
	st, err := l.store.GetStorage(context.Background(), l.st.ID)
	require.NoError(t, err)
	assert.NotNil(t, st.LastSyncAt, "a converged lazy storage has been synced")
	l.lc.countsAt = time.Time{}
	cov := l.lc.coverage(context.Background())
	assert.True(t, cov.Complete)
	assert.EqualValues(t, 1+8+8, cov.CataloguedFolders, "root, d.., d../alt")
	dir := l.row("/daa")
	require.NotNil(t, dir)
	assert.EqualValues(t, 6, dir.Size, "folder sizes are computed: 6 one-byte files")
}

// Resumable: the work list is the table. A filler stopped halfway picks up
// where it was on the next start — it does not start over, and it does not
// lose the folders it had discovered.
func TestLazyFill_ResumesAfterARestart(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	l := newLazyLab(t, nil)
	makeTree(t, l, 10, 2)

	// Root plus three folders, then "the process stops".
	ctx := l.ctx
	for i := 0; i < 4; i++ {
		batch := l.lc.nextFillBatch(ctx)
		require.NotEmpty(t, batch)
		_, err := l.lc.reconcile(ctx, batch[0].dir, batch[0].why)
		require.NoError(t, err)
	}
	counts, err := l.store.CountCatalogueFolders(ctx, l.st.ID)
	require.NoError(t, err)
	require.True(t, counts.RootCatalogued)
	require.Greater(t, counts.Uncatalogued, int64(0))
	before := l.lc.reconciles.Load()

	// A new engine over the same database.
	l2 := &lazyLab{store: l.store, st: l.st, root: l.root, ctx: l.ctx}
	s2 := &storageSyncer{store: l.store, storage: l.st, driver: l.lc.s.driver, rule: l.lc.s.rule, ctx: l.ctx, fallback: time.Minute}
	l2.lc = newLazyCatalogue(s2, nil, time.Minute)
	s2.lazy.Store(l2.lc)
	runFillFor(t, l2, 20*time.Second)
	require.True(t, l2.lc.converged.Load())
	assert.EqualValues(t, 1+10+10-4, l2.lc.reconciles.Load(), "only what was left: nothing listed twice")
	assert.EqualValues(t, 4, before)
	for d := 0; d < 10; d++ {
		assert.NotNil(t, l.row("/d"+itoa(d)+"/alt/fab.txt"))
	}
}

// While somebody uses the storage the filler slows to LazyFillBusyPause, and
// a queued open always runs before its next folder.
func TestLazyFill_YieldsToPeople(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	LazyFillBusyPause = 200 * time.Millisecond
	l := newLazyLab(t, nil)
	makeTree(t, l, 6, 1)

	l.lc.noteActivity()
	ctx, cancel := context.WithTimeout(l.ctx, 700*time.Millisecond)
	defer cancel()
	l.lc.fillLoop(ctx)
	busy := l.lc.reconciles.Load()
	assert.LessOrEqual(t, busy, int64(4), "about one folder per busy pause while somebody is active")

	// An open queued ahead of the filler goes first: the filler does not take
	// its next folder while a request is waiting, and carries on once it has
	// been served.
	//
	// Break: drop the `for lc.pending()` loop in pace — the filler goes ahead.
	// (Nobody is active any more, so no busy pause hides the difference.)
	l.lc.lastActivity.Store(0)
	l.lc.request("/dae", reasonOpen)
	done := make(chan bool, 1)
	go func() { done <- l.lc.pace(l.ctx) }()
	select {
	case <-done:
		t.Fatal("the filler went ahead of a queued open")
	case <-time.After(150 * time.Millisecond):
	}
	it, queued := l.lc.next()
	require.True(t, queued, "the open is still there to serve")
	assert.Equal(t, "/dae", it.dir)
	select {
	case ok := <-done:
		assert.True(t, ok)
	case <-time.After(3 * time.Second):
		t.Fatal("the filler did not carry on once the queue was served")
	}
}

// A folder that vanished, or that an exclusion now covers, leaves the work
// list without touching any catalogue row; a folder whose listing fails is
// retried later instead of pinning the filler.
func TestLazyFill_WorkListCleansItself(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	l := newLazyLab(t, nil)
	l.write(t, "kalacak/a.txt", "a")
	l.write(t, "gidecek/b.txt", "b")
	l.reconcile(t, "/")
	require.Equal(t, model.FolderUncatalogued, l.folder(t, "/gidecek").State)
	gidecek := l.row("/gidecek")
	require.NotNil(t, gidecek)
	require.NoError(t, os.RemoveAll(filepath.Join(l.root, "gidecek")))

	runFillFor(t, l, 10*time.Second)
	require.True(t, l.lc.converged.Load())
	assert.Nil(t, l.folder(t, "/gidecek"), "gone from the disk: gone from the work list")
	assert.NotNil(t, l.row("/gidecek"), "…and nothing else: its row is its parent's reconcile's to remove")
}

// Once converged, a filler with nothing due says so: the storage page reads
// "Done", not "Waiting".
//
// Break: store "idle" unconditionally in fillLoop's empty-batch branch.
func TestLazyFill_ConvergedAndNothingDueReadsConverged(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	l := newLazyLab(t, nil)
	makeTree(t, l, 3, 1)
	ctx, cancel := context.WithCancel(l.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.lc.fillLoop(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	require.Eventually(t, func() bool { return l.lc.converged.Load() }, 10*time.Second, 5*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, "converged", l.lc.filler.Load())
}

// The filler reads its frontier a batch at a time, and every open runs ahead
// of it — so the folder a person just opened is usually still in the batch.
// It is not listed a second time.
//
// Measured on the 100k-file lab tree: an opened folder was listed by the open
// and again by the filler 2 s later, for every folder anybody opened.
//
// Break: drop the handledSince check in fillLoop — 22 listings, not 21.
func TestLazyFill_DoesNotListAFolderAnOpenAlreadyCatalogued(t *testing.T) {
	restore := setFastLazy()
	defer restore()
	LazyFillBusyPause = 40 * time.Millisecond
	l := newLazyLab(t, nil)
	makeTree(t, l, 10, 1) // root + 10 folders + 10 subfolders = 21

	l.lc.noteActivity()
	ctx, cancel := context.WithTimeout(l.ctx, 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.lc.fillLoop(ctx)
	}()
	// The root and the first top-level folder are in: the batch holding the
	// other nine was read already.
	require.Eventually(t, func() bool { return l.lc.reconciles.Load() >= 2 }, 10*time.Second, 5*time.Millisecond)
	_, err := l.lc.reconcile(l.ctx, "/daj", reasonOpen)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return l.lc.converged.Load() }, 15*time.Second, 10*time.Millisecond)
	cancel()
	<-done
	assert.EqualValues(t, 21, l.lc.reconciles.Load(), "every folder listed once")
}

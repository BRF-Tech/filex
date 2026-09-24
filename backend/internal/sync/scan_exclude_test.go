package sync_test

// Issue #44: a storage pointed at an existing tree catalogued everything under
// its root — `.git`, `.snapshots`, a download client's `incomplete/` — and
// fed all of it to the search index, the thumbnailer and the virus scanner.
// A storage now carries scan exclusions (config `scan_exclude`, glob patterns),
// and every walk that catalogues asks the one rule (internal/scanrule) before
// it goes anywhere.

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// listRecDriver is the local driver, recording every directory the walk asks
// it to list — so a test can prove the walk never went INTO an excluded
// folder, rather than merely that it did not keep what it found there.
type listRecDriver struct {
	*local.Driver
	root string
}

var listRec struct {
	gosync.Mutex
	byRoot map[string][]string
}

func init() {
	storage.Register("listrec-test", func() storage.Driver { return &listRecDriver{} })
}

func (d *listRecDriver) Init(ctx context.Context, cfg map[string]any) error {
	d.Driver = &local.Driver{}
	d.root, _ = cfg["root"].(string)
	return d.Driver.Init(ctx, cfg)
}

func (d *listRecDriver) Name() string { return "listrec-test" }

func (d *listRecDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	listRec.Lock()
	if listRec.byRoot == nil {
		listRec.byRoot = map[string][]string{}
	}
	listRec.byRoot[d.root] = append(listRec.byRoot[d.root], path.Clean("/"+p))
	listRec.Unlock()
	return d.Driver.List(ctx, p)
}

func listedUnder(root string) []string {
	listRec.Lock()
	defer listRec.Unlock()
	return append([]string(nil), listRec.byRoot[root]...)
}

// excludingStorage is a storage over a fresh directory, with the given scan
// exclusions, on driver ("local" or "listrec-test").
func excludingStorage(t *testing.T, store db.Store, driver, exclude string) (*model.Storage, string) {
	t.Helper()
	root := t.TempDir()
	cfg := map[string]any{"root": root}
	if exclude != "" {
		cfg["scan_exclude"] = exclude
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "haric-" + driver, Driver: driver, MountPath: "/haric",
		ConfigJSON: raw, SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	return st, root
}

// setExclusions rewrites a storage row's scan exclusions, as the admin form's
// Save does. The caller starts a fresh worker afterwards (the server restarts
// the storage's syncer on every edit).
func setExclusions(t *testing.T, store db.Store, st *model.Storage, exclude string) {
	t.Helper()
	cfg := map[string]any{}
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	cfg["scan_exclude"] = exclude
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st.ConfigJSON = raw
	require.NoError(t, store.UpdateStorage(context.Background(), st))
}

// The tree and the patterns of issue #44, plus a `*.tmp` and the desktop's
// "open with" working area: the excluded folders are never listed, nothing
// under them is catalogued, and filex's own `.filex-open` — which `.*` would
// otherwise take — is catalogued as before.
func TestScanExclusions_TheWalkNeverGoesIntoAnExcludedFolder(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, root := excludingStorage(t, store, "listrec-test", "**/.*\ndownloads/incomplete/**\n*.tmp")
	for _, rel := range []string{
		"Movies/film.mkv", "Music/song.mp3",
		".snapshots/daily/0001/film.mkv",
		"downloads/complete/a.mkv", "downloads/incomplete/b.part", "downloads/incomplete/deep/c.part",
		"Music/.DS_Store", "notes.tmp", "keep.txt",
		".filex-open/a1b2c3d4e5f6-rapor.docx",
	} {
		writeUnder(t, root, rel, "x")
	}

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	want := []string{
		"/.filex-open", "/.filex-open/a1b2c3d4e5f6-rapor.docx",
		"/Movies", "/Movies/film.mkv", "/Music", "/Music/song.mp3",
		"/downloads", "/downloads/complete", "/downloads/complete/a.mkv",
		"/keep.txt",
	}
	assert.Equal(t, want, livePaths(t, store, st.ID))
	assert.Equal(t, len(want), run.SeenCount, "seen counts what was walked")

	listed := listedUnder(root)
	require.NotEmpty(t, listed)
	for _, p := range listed {
		assert.False(t, strings.HasPrefix(p, "/.snapshots") || strings.HasPrefix(p, "/downloads/incomplete"),
			"the walk listed %s, inside an excluded folder", p)
	}
	assert.Contains(t, listed, "/downloads", "the folder ABOVE an excluded one is walked as usual")
}

// A backend that hands the walk its whole tree in one pass (an object store)
// cannot be told to leave a prefix out — but what it returns under one is not
// kept: a `.git` and a `node_modules` must not count against the prefetch cap
// (or the memory it bounds), nor be catalogued.
func TestScanExclusions_OnePassListingDoesNotKeepExcludedKeys(t *testing.T) {
	prev := filexsync.TreePrefetchMax
	filexsync.TreePrefetchMax = 3 // /readme.md, /src, /src/a.go — and nothing else
	t.Cleanup(func() { filexsync.TreePrefetchMax = prev })

	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t,
		"readme.md", "src/a.go",
		"src/.git/HEAD", "src/.git/objects/aa/bb", "src/.git/objects/cc/dd",
		"node_modules/x/index.js", "node_modules/y/index.js",
	)
	st := treeStorage(t, store, "objtree-test", map[string]any{
		"bucket": bucket, "scan_exclude": ".git\nnode_modules",
	})

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.Equal(t, []string{"/readme.md", "/src", "/src/a.go"}, livePaths(t, store, st.ID))

	b := treeBucketOf(bucket)
	assert.EqualValues(t, 1, b.walks.Load())
	assert.EqualValues(t, 0, b.lists.Load(),
		"the excluded keys were held and pushed the tree over the prefetch cap")
}

// ⚠⚠ A row catalogued BEFORE its pattern was added is never seen again, and
// "unseen" must not read as "deleted": a folder row in the trash is purged by
// deleting its prefix on the backend, and the folder is still there. Such rows
// stay as they are, while the rest of the storage is reconciled as usual — a
// file really deleted elsewhere still goes to the trash.
func TestScanExclusions_RowsCataloguedBeforeThePatternStayAndAreNeverTrashed(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, root := excludingStorage(t, store, "local", "")
	for _, rel := range []string{
		"proj/.git/HEAD", "proj/.git/objects/aa/bb",
		"proj/doc0.txt", "proj/doc1.txt", "proj/doc2.txt", "proj/doc3.txt", "proj/doc4.txt",
		"proj/doc5.txt", "proj/doc6.txt", "proj/doc7.txt", "proj/doc8.txt", "proj/doc9.txt",
	} {
		writeUnder(t, root, rel, "x")
	}
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	require.NotNil(t, node(t, store, st.ID, "/proj/.git/objects/aa"))

	setExclusions(t, store, st, ".git")
	writeUnder(t, root, "proj/.git/NEW", "x") // appears after the pattern: never catalogued
	waitPastSecondBoundary()
	_, second := runSync(t, store, st) // seen drops by the .git subtree: the guard trips once
	require.Equal(t, "ok", second.Status, second.Error)

	require.NoError(t, os.Remove(filepath.Join(root, "proj", "doc9.txt")))
	waitPastSecondBoundary()
	_, third := runSync(t, store, st) // like with like: the delete pass runs
	require.Equal(t, "ok", third.Status, third.Error)

	for _, p := range []string{"/proj/.git", "/proj/.git/HEAD", "/proj/.git/objects", "/proj/.git/objects/aa", "/proj/.git/objects/aa/bb"} {
		n := rowAnywhere(t, store, st.ID, p)
		require.NotNil(t, n, "%s lost its row", p)
		assert.Nil(t, n.DeletedAt, "%s went to the trash although it is still on the storage", p)
	}
	assert.Nil(t, node(t, store, st.ID, "/proj/.git/NEW"), "a file under an excluded folder was catalogued")
	gone := rowAnywhere(t, store, st.ID, "/proj/doc9.txt")
	require.NotNil(t, gone)
	assert.NotNil(t, gone.DeletedAt, "a file really deleted outside the excluded folder must still be tombstoned")
}

// A folder rescan compares what it saw with the folder's catalogued size. A
// subtree the walk no longer enters is never seen, so without discounting it a
// folder holding a big `.git` beside a few documents would trip the 70% guard
// on EVERY rescan, for good, and a deleted document would never leave the
// catalogue. And the excluded folder itself cannot be rescanned.
func TestScanExclusions_FolderRescanDiscountsWhatItDoesNotWalk(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, root := excludingStorage(t, store, "local", "")
	for i := 0; i < 10; i++ {
		writeUnder(t, root, "proj/doc"+itoa(int64(i))+".txt", "x")
	}
	writeUnder(t, root, "proj/gone.txt", "x")
	for i := 0; i < 20; i++ {
		writeUnder(t, root, "proj/.git/objects/"+itoa(int64(i)), "x")
	}
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)

	setExclusions(t, store, st, ".git")
	w := newWorkerFor(t, store, st)
	require.NoError(t, os.Remove(filepath.Join(root, "proj", "gone.txt")))
	waitPastSecondBoundary()

	res, err := w.RescanFolder(ctx, st.ID, "/proj")
	require.NoError(t, err)
	assert.Empty(t, res.RemovalSkipped)
	assert.Equal(t, 1, res.Removed, "the deleted document")
	assert.Nil(t, node(t, store, st.ID, "/proj/gone.txt"))
	assert.NotNil(t, node(t, store, st.ID, "/proj/.git/objects"), "the excluded subtree is left as it is")

	_, err = w.RescanFolder(ctx, st.ID, "/proj/.git")
	assert.ErrorIs(t, err, filexsync.ErrScopeInvalid, "an excluded folder is not the scan's to look at")
}

// A same-storage folder copy has its subtree catalogued by the walk
// (CatalogueTree). It must stop where the scan stops, or a copied `.git`
// comes back into the catalogue the scan keeps it out of.
func TestScanExclusions_CatalogueTreeStopsWhereTheScanStops(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, root := excludingStorage(t, store, "local", ".git")
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	writeUnder(t, root, "kopya/a.txt", "x")
	writeUnder(t, root, "kopya/.git/HEAD", "x")
	dir := seedRow(t, store, st, nil, "/kopya", model.NodeTypeDirectory, 0)

	require.NoError(t, filexsync.CatalogueTree(ctx, store, nil, nil, st, drv, "/kopya", &dir.ID))
	assert.NotNil(t, node(t, store, st.ID, "/kopya/a.txt"))
	assert.Nil(t, node(t, store, st.ID, "/kopya/.git"))
	assert.Nil(t, node(t, store, st.ID, "/kopya/.git/HEAD"))
}

// A setting that got past the API's check some other way (a hand edit) must
// not stop the storage from being scanned: it scans everything, and says so.
func TestScanExclusions_ABrokenSettingScansEverything(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, root := excludingStorage(t, store, "local", "[unclosed")
	writeUnder(t, root, "a/.git/HEAD", "x")
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.NotNil(t, node(t, store, st.ID, "/a/.git/HEAD"))
}

// In fsnotify mode every batch of events starts a full scan. A download client
// writing `*.part` files, or git rewriting `.git/index`, would keep one running
// every two seconds for as long as it works. An excluded folder is not watched
// at all; an excluded name in a watched folder (`docs/film.part`) raises
// events there, and each is judged by the same rule as the walk and starts
// nothing. A change to anything the scan walks still starts one.
func TestScanExclusions_AChangeInAnExcludedFolderStartsNoScan(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st, root := excludingStorage(t, store, "local", "downloads/incomplete/**\n*.part")
	writeUnder(t, root, "downloads/incomplete/.keep", "x")
	writeUnder(t, root, "docs/a.txt", "x")
	st.SyncMode = model.SyncModeFSNotify
	require.NoError(t, store.UpdateStorage(context.Background(), st))
	newWorkerFor(t, store, st)

	runs := func() int { return syncRunCount(t, store, st.ID) }
	waitFor := func(n int, within time.Duration) bool {
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			if runs() >= n {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return runs() >= n
	}
	require.True(t, waitFor(1, 10*time.Second), "the initial scan never ran")
	time.Sleep(500 * time.Millisecond) // let the initial run's own events settle
	base := runs()

	for i := 0; i < 5; i++ {
		writeUnder(t, root, "downloads/incomplete/film.mkv", strings.Repeat("x", i+1))
		writeUnder(t, root, "docs/film.part", strings.Repeat("x", i+1))
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(3500 * time.Millisecond) // past the 2 s debounce
	assert.Equal(t, base, runs(), "writing an excluded path started a scan")

	writeUnder(t, root, "docs/b.txt", "x")
	assert.True(t, waitFor(base+1, 10*time.Second), "a change outside the excluded folder must still start a scan")
}

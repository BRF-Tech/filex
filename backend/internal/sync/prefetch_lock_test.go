package sync_test

// Two things issue #33 asked for, measured rather than assumed.
//
// A storage with 150,000 objects took longer to scan than its own interval,
// and pressing "Scan now" while a poll was still walking started a second
// full walk over the same rows. The scan was slow for a structural reason:
// the walk asked the object store for every directory separately — one
// ListObjectsV2 round trip per prefix — when an object store can hand over
// the whole tree in one un-delimited listing. And nothing stopped two runs
// from overlapping.
//
// So: a driver that offers storage.TreeWalker is listed ONCE per run and the
// walk never calls List; when the tree is too large to hold, the run falls
// back to the per-directory walk and catalogues exactly the same rows; and a
// second RunOnce on a storage already being walked is refused with
// ErrRunInProgress instead of being started.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// ---------------------------------------------------------------------------
// fixtures: an object store that can be listed in one pass, and one that
// blocks so a run can be caught in flight
// ---------------------------------------------------------------------------

var treeBuckets struct {
	sync.Mutex
	m map[string]*treeBucket
}

type treeBucket struct {
	objs  map[string]int64 // key → size
	lists atomic.Int32     // List calls the walk made
	walks atomic.Int32     // WalkTree calls
}

func newTreeBucket(t *testing.T, keys ...string) string {
	t.Helper()
	treeBuckets.Lock()
	defer treeBuckets.Unlock()
	if treeBuckets.m == nil {
		treeBuckets.m = map[string]*treeBucket{}
	}
	name := fmt.Sprintf("tree-%s-%d", t.Name(), time.Now().UnixNano())
	b := &treeBucket{objs: map[string]int64{}}
	for i, k := range keys {
		b.objs[k] = int64(10 + i)
	}
	treeBuckets.m[name] = b
	return name
}

func treeBucketOf(name string) *treeBucket {
	treeBuckets.Lock()
	defer treeBuckets.Unlock()
	return treeBuckets.m[name]
}

// treeDriver is an object store: a directory is a key prefix and nothing
// else. It implements storage.TreeWalker the way the S3 driver does —
// every key under the root in one pass, ancestors synthesised once each.
type treeDriver struct{ b *treeBucket }

func init() {
	storage.Register("objtree-test", func() storage.Driver { return &treeDriver{} })
	storage.Register("gate-test", func() storage.Driver { return &gateDriver{} })
}

func (d *treeDriver) Init(_ context.Context, cfg map[string]any) error {
	name, _ := cfg["bucket"].(string)
	d.b = treeBucketOf(name)
	if d.b == nil {
		return fmt.Errorf("objtree-test: unknown bucket %q", name)
	}
	return nil
}
func (d *treeDriver) Name() string { return "objtree-test" }
func (d *treeDriver) Capabilities() storage.Capabilities {
	return storage.Capabilities{Read: true}
}

func treeKey(p string) string { return strings.Trim(path.Clean("/"+p), "/") }

func (d *treeDriver) List(_ context.Context, p string) ([]storage.Object, error) {
	d.b.lists.Add(1)
	prefix := treeKey(p)
	if prefix != "" {
		prefix += "/"
	}
	files := map[string]int64{}
	dirs := map[string]bool{}
	for k, size := range d.b.objs {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		if i := strings.Index(rest, "/"); i >= 0 {
			dirs[rest[:i]] = true
			continue
		}
		files[rest] = size
	}
	base := path.Clean("/" + p)
	out := []storage.Object{}
	for name := range dirs {
		out = append(out, storage.Object{Path: path.Join(base, name), Name: name, Kind: storage.KindDirectory})
	}
	for name, size := range files {
		out = append(out, storage.Object{Path: path.Join(base, name), Name: name, Kind: storage.KindFile,
			Size: size, Etag: fmt.Sprint(size), Mtime: time.Now()})
	}
	return out, nil
}

func (d *treeDriver) WalkTree(_ context.Context, p string, fn func(storage.Object) error) error {
	d.b.walks.Add(1)
	prefix := treeKey(p)
	if prefix != "" {
		prefix += "/"
	}
	base := path.Clean("/" + p)
	keys := make([]string, 0, len(d.b.objs))
	for k := range d.b.objs {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		rel := strings.TrimPrefix(k, prefix)
		parts := strings.Split(rel, "/")
		cur := base
		for _, part := range parts[:len(parts)-1] {
			cur = path.Join(cur, part)
			if seen[cur] {
				continue
			}
			seen[cur] = true
			if err := fn(storage.Object{Path: cur, Name: part, Kind: storage.KindDirectory}); err != nil {
				return err
			}
		}
		size := d.b.objs[k]
		if err := fn(storage.Object{Path: path.Join(base, rel), Name: path.Base(rel), Kind: storage.KindFile,
			Size: size, Etag: fmt.Sprint(size), Mtime: time.Now()}); err != nil {
			return err
		}
	}
	return nil
}

func (d *treeDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	k := treeKey(p)
	if size, ok := d.b.objs[k]; ok {
		return storage.Object{Path: "/" + k, Name: path.Base(k), Kind: storage.KindFile, Size: size}, nil
	}
	for key := range d.b.objs {
		if strings.HasPrefix(key, k+"/") {
			return storage.Object{Path: "/" + k, Name: path.Base(k), Kind: storage.KindDirectory}, nil
		}
	}
	return storage.Object{}, storage.ErrNotFound
}

func (d *treeDriver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	if _, ok := d.b.objs[treeKey(p)]; !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader("bytes")), nil
}

// gateDriver blocks its first List until released — a walk caught mid-flight.
type gateDriver struct {
	gate    chan struct{}
	release chan struct{}
	once    sync.Once
}

var gates struct {
	sync.Mutex
	m map[string]*gateDriver
}

func (d *gateDriver) Init(_ context.Context, cfg map[string]any) error {
	name, _ := cfg["gate"].(string)
	gates.Lock()
	defer gates.Unlock()
	g := gates.m[name]
	if g == nil {
		return fmt.Errorf("gate-test: unknown gate %q", name)
	}
	d.gate, d.release = g.gate, g.release
	return nil
}
func (d *gateDriver) Name() string                       { return "gate-test" }
func (d *gateDriver) Capabilities() storage.Capabilities { return storage.Capabilities{Read: true} }
func (d *gateDriver) List(ctx context.Context, _ string) ([]storage.Object, error) {
	d.once.Do(func() { close(d.gate) }) // "I am inside the walk now"
	select {
	case <-d.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, nil
}
func (d *gateDriver) Stat(_ context.Context, _ string) (storage.Object, error) {
	return storage.Object{}, storage.ErrNotFound
}
func (d *gateDriver) Read(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}

func newGate(t *testing.T) (name string, entered <-chan struct{}, release chan struct{}) {
	t.Helper()
	gates.Lock()
	defer gates.Unlock()
	if gates.m == nil {
		gates.m = map[string]*gateDriver{}
	}
	name = fmt.Sprintf("gate-%s-%d", t.Name(), time.Now().UnixNano())
	g := &gateDriver{gate: make(chan struct{}), release: make(chan struct{})}
	gates.m[name] = g
	return name, g.gate, g.release
}

func treeStorage(t *testing.T, store db.Store, driver string, cfg map[string]any) *model.Storage {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "kova-" + driver,
		Driver:     driver,
		MountPath:  "/kova",
		ConfigJSON: raw,
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st
}

// livePaths returns every live catalogue row of the storage, sorted.
func livePaths(t *testing.T, store db.Store, storageID int64) []string {
	t.Helper()
	ctx := context.Background()
	out := []string{}
	var visit func(parent *int64)
	visit = func(parent *int64) {
		rows, err := store.ListNodesByParent(ctx, storageID, parent)
		require.NoError(t, err)
		for _, n := range rows {
			if n.DeletedAt != nil {
				continue
			}
			out = append(out, n.Path)
			if n.Type == model.NodeTypeDirectory {
				id := n.ID
				visit(&id)
			}
		}
	}
	visit(nil)
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// 1. one pass, not one call per directory
// ---------------------------------------------------------------------------

func TestRunOnce_ListsATreeWalkerBackendInOnePass(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t,
		"a/1.txt", "a/b/2.txt", "a/b/c/3.txt", "d/4.txt", "root.txt",
		".filex-trash/1700000000-x__old.txt", // never catalogued, never held
	)
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	b := treeBucketOf(bucket)
	assert.EqualValues(t, 1, b.walks.Load(), "the tree is asked for once")
	assert.EqualValues(t, 0, b.lists.Load(),
		"the walk asked the backend for a directory although it already held the whole tree")

	want := []string{"/a", "/a/1.txt", "/a/b", "/a/b/2.txt", "/a/b/c", "/a/b/c/3.txt", "/d", "/d/4.txt", "/root.txt"}
	assert.Equal(t, want, livePaths(t, store, st.ID))
	assert.Equal(t, len(want), run.SeenCount, "seen must count what the per-directory walk counts")
}

// The shortcut is bounded. Over the cap the run must still complete, must
// still catalogue the same rows, and must do so the ordinary way.
func TestRunOnce_TooLargeATreeFallsBackToThePerDirectoryWalk(t *testing.T) {
	prev := filexsync.TreePrefetchMax
	filexsync.TreePrefetchMax = 3
	t.Cleanup(func() { filexsync.TreePrefetchMax = prev })

	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t, "a/1.txt", "a/b/2.txt", "a/b/c/3.txt", "d/4.txt", "root.txt")
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)

	b := treeBucketOf(bucket)
	assert.EqualValues(t, 1, b.walks.Load(), "the one-pass listing was tried")
	assert.Greater(t, b.lists.Load(), int32(0), "…and given up on: the walk must have asked per directory")

	want := []string{"/a", "/a/1.txt", "/a/b", "/a/b/2.txt", "/a/b/c", "/a/b/c/3.txt", "/d", "/d/4.txt", "/root.txt"}
	assert.Equal(t, want, livePaths(t, store, st.ID))
	assert.Equal(t, len(want), run.SeenCount)
}

// A second run on the same storage sees exactly what the first saw: nothing
// is added, nothing is tombstoned. (The bug this guards: a prefetch that
// spelled a path differently from List would tombstone the whole tree on the
// second pass and re-create it on the third.)
func TestRunOnce_SecondPassOverAPrefetchedTreeIsStable(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	bucket := newTreeBucket(t, "a/1.txt", "a/b/2.txt", "root.txt")
	st := treeStorage(t, store, "objtree-test", map[string]any{"bucket": bucket})

	w, first := runSync(t, store, st)
	require.Equal(t, 5, first.Added)
	waitPastSecondBoundary()
	require.NoError(t, w.Trigger(context.Background(), st.ID))
	second, err := store.GetLastSyncRun(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Equal(t, "ok", second.Status)
	assert.Equal(t, 0, second.Added, "nothing new on the second pass")
	assert.Equal(t, 0, second.Deleted, "nothing tombstoned on the second pass")
	assert.Equal(t, 5, second.SeenCount)
}

// ---------------------------------------------------------------------------
// 2. one run at a time
// ---------------------------------------------------------------------------

func TestRunOnce_ASecondRunWhileOneIsWalkingIsRefused(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	gate, entered, release := newGate(t)
	st := treeStorage(t, store, "gate-test", map[string]any{"gate": gate})

	ctx := context.Background()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		w.Stop()
	})
	assert.False(t, w.Running(st.ID), "nothing has started yet")

	first := make(chan error, 1)
	go func() { first <- w.Trigger(ctx, st.ID) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first run never reached the backend")
	}
	assert.True(t, w.Running(st.ID), "a run is in flight")

	// The second press: answered at once, not queued behind the first.
	done := make(chan error, 1)
	go func() { done <- w.Trigger(ctx, st.ID) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, filexsync.ErrRunInProgress)
	case <-time.After(2 * time.Second):
		t.Fatal("the second run blocked behind the first instead of being refused")
	}

	close(release)
	select {
	case err := <-first:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the first run did not finish after the gate opened")
	}
	assert.False(t, w.Running(st.ID))
	runs, _, err := store.ListSyncRunsAcrossAll(ctx, st.ID, "", 10, 0)
	require.NoError(t, err)
	assert.Len(t, runs, 1, "the refused press must not have opened a sync_runs row")
}

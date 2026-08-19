package sync_test

// The sync worker over a PLUGIN-backed storage.
//
// Why this file exists: sync is the surface that decides what filex BELIEVES
// is in a storage. Everything downstream — search, thumbnails, share links,
// folder sizes, the explorer itself — reads the node rows this worker writes,
// so a plugin that lists correctly through the file manager but indexes wrong
// here is a plugin whose files are invisible in half the product. The worker
// also reaches storage differently from every HTTP surface: it looks the
// driver up in the global registry by the storage row's `driver` column
// (storage.Get), Init()s it from ConfigJSON, and then only ever calls List —
// recursively, from "/" down. None of that runs in the plugin package's own
// tests.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/plugin/testplugin"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// pluginStorage creates a storage row whose driver column names the running
// plugin. SyncModeOnDemand is load-bearing: the poll and fsnotify modes make
// Loop() fire its own pass the moment the storage is added, which would race
// the Trigger below and make every count in these tests non-deterministic.
func pluginStorage(t *testing.T, store db.Store, p *testplugin.Plugin) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:       "eklenti",
		Driver:     p.Register(t),
		MountPath:  "/eklenti",
		ConfigJSON: []byte(`{"root":"/data"}`),
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st
}

// runSync performs exactly one pass and returns its recorded outcome.
func runSync(t *testing.T, store db.Store, st *model.Storage) (*filexsync.Worker, *model.SyncRun) {
	t.Helper()
	ctx := context.Background()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	return w, run
}

// waitPastSecondBoundary sleeps until the wall clock has certainly moved into
// a later second than the one the previous sync stamped its rows with.
func waitPastSecondBoundary() {
	now := time.Now()
	time.Sleep(now.Truncate(time.Second).Add(1100 * time.Millisecond).Sub(now))
}

func node(t *testing.T, store db.Store, storageID int64, path string) *model.Node {
	t.Helper()
	n, _ := store.GetNodeByPath(context.Background(), storageID, pathkey.Hash(storageID, path))
	return n
}

// One poll pass over a plugin has to produce the same node rows a local
// storage would: a row per file AND per directory, with the size and the kind
// carried across the protocol hop. A listing that comes back with everything
// sized 0, or with directories typed as files, renders in the explorer and is
// wrong everywhere it matters (folder sizes, thumbnails, search results).
func TestSyncIndexesAPluginStorage(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	p := testplugin.Start(t)
	p.Seed("rapor.txt", "kök dosya")
	p.Seed("belgeler/notlar.txt", "on bytes")
	p.Seed("belgeler/alt/derin.txt", "derinlerde")
	st := pluginStorage(t, store, p)

	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, "sync run error: %s", run.Error)

	for path, want := range map[string]struct {
		kind model.NodeType
		size int64
	}{
		"/rapor.txt":              {model.NodeTypeFile, int64(len("kök dosya"))},
		"/belgeler":               {model.NodeTypeDirectory, 0},
		"/belgeler/notlar.txt":    {model.NodeTypeFile, 8},
		"/belgeler/alt":           {model.NodeTypeDirectory, 0},
		"/belgeler/alt/derin.txt": {model.NodeTypeFile, int64(len("derinlerde"))},
	} {
		n := node(t, store, st.ID, path)
		require.NotNil(t, n, "no node row for %s", path)
		require.Equal(t, want.kind, n.Type, "kind for %s", path)
		if want.kind == model.NodeTypeFile {
			require.Equal(t, want.size, n.Size, "size for %s", path)
		}
		// The mtime the plugin reported must survive: a sync that stamps every
		// file with "now" makes the next differential pass copy the lot again.
		if want.kind == model.NodeTypeFile {
			require.NotNil(t, n.BackendMtime, "no backend mtime for %s", path)
			require.True(t, n.BackendMtime.Equal(testplugin.SeedTime),
				"backend mtime for %s = %v, want the plugin's %v", path, n.BackendMtime, testplugin.SeedTime)
		}
	}

	// The recursive walk has to reach the bottom: seen counts every object.
	require.Equal(t, 5, run.SeenCount, "walk did not reach every object")
	require.Equal(t, 5, run.Added)
}

// A second pass has to see what changed out of band — which is the entire
// point of running one over a plugin, since a plugin's backend belongs to
// somebody else and filex is never the only writer.
func TestSyncPicksUpPluginSideChanges(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	p := testplugin.Start(t)
	// Ten files, because the tombstone guard refuses to delete anything when a
	// pass sees fewer than 70% of what the previous one saw. With three files
	// a single deletion would trip it and the test would prove the guard, not
	// the deletion.
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		p.Seed("kutu/"+n+".txt", n)
	}
	st := pluginStorage(t, store, p)

	ctx := context.Background()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	require.NotNil(t, node(t, store, st.ID, "/kutu/a.txt"))

	// Somebody else writes and deletes directly in the plugin's backend.
	p.Seed("kutu/yeni.txt", "sonradan")
	p.Delete("kutu/a.txt")

	// ⚠ The wait is load-bearing, not flake-padding. RunOnce truncates its
	// runStart to the second to match SQLite's CURRENT_TIMESTAMP, and the
	// tombstone pass only considers a node stale when seen_at < runStart — so
	// two passes inside the same wall-clock second can never delete anything.
	// Real polls are minutes apart; a test that fires both immediately would
	// measure the clock, not the sync.
	waitPastSecondBoundary()

	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status, "sync run error: %s", run.Error)

	require.NotNil(t, node(t, store, st.ID, "/kutu/yeni.txt"), "new object was not indexed")
	require.Nil(t, node(t, store, st.ID, "/kutu/a.txt"), "removed object still has a live node row")
	require.Equal(t, 1, run.Deleted)
}

// ⚠ The one that matters most: a plugin that is unavailable must not empty the
// index. The tombstone pass soft-deletes every node the walk did not see, so
// a walk that fails and is reported as an empty-but-successful listing would
// mark the whole storage deleted — the user opens filex during a plugin
// outage and finds their files gone, with nothing on the backend to blame.
func TestPluginOutageDoesNotWipeTheIndex(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	p := testplugin.Start(t)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		p.Seed("kutu/"+n+".txt", n)
	}
	st := pluginStorage(t, store, p)

	ctx := context.Background()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))
	before, err := store.CountNodesByStorage(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, int64(6), before, "expected 5 files plus their folder")

	p.Down()
	err = w.Trigger(ctx, st.ID)
	require.Error(t, err, "a sync against an unavailable plugin reported success")

	run, rerr := store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, rerr)
	require.Equal(t, "failed", run.Status, "the failed pass was not recorded as failed")

	after, err := store.CountNodesByStorage(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, before, after, "an unreachable plugin cost the index %d nodes", before-after)
	require.NotNil(t, node(t, store, st.ID, "/kutu/a.txt"), "node was tombstoned during an outage")

	// And the next healthy pass is a no-op, not a re-import.
	p.Up()
	require.NoError(t, w.Trigger(ctx, st.ID))
	run, err = store.GetLastSyncRun(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", run.Status)
	require.Equal(t, 0, run.Added, "a recovered plugin re-added rows that were already there")
}

// A plugin whose process was restarted between two passes must keep syncing.
// The worker resolves the driver ONCE, when the storage is added, and holds it
// for the lifetime of the process — so a driver that cannot recover from a
// lost instance leaves this storage permanently stale, and nothing about the
// storage's own state would show why.
func TestSyncSurvivesAPluginRestart(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	p := testplugin.Start(t)
	p.Seed("a.txt", "önce")
	st := pluginStorage(t, store, p)

	ctx := context.Background()
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(ctx, st))
	t.Cleanup(w.Stop)
	require.NoError(t, w.Trigger(ctx, st.ID))

	p.Restart()
	p.Seed("b.txt", "sonra")
	require.NoError(t, w.Trigger(ctx, st.ID))

	require.NotNil(t, node(t, store, st.ID, "/b.txt"), "nothing was indexed after the plugin restarted")
	require.Positive(t, p.NoInstanceCount(), "the plugin never reported a lost instance, so no retry was exercised")
}

package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netfs"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// lazyOnFS is a lazy catalogue over a fresh directory that the filesystem
// probe reports as info (ok=false: the probe could not tell). The watcher is
// started the way loopLazy starts it.
func lazyOnFS(t *testing.T, info netfs.Info, ok bool) (*lazyCatalogue, string, error) {
	t.Helper()
	was := probeWatchFS
	probeWatchFS = func(string) (netfs.Info, bool) { return info, ok }
	t.Cleanup(func() { probeWatchFS = was })
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg := map[string]any{"root": root}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "nas", Driver: "local", MountPath: "/nas", ConfigJSON: raw,
		SyncMode: model.SyncModeLazy, Enabled: true,
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), cfg))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &storageSyncer{store: store, storage: st, driver: drv, rule: ruleFor(st, cfg), ctx: ctx, fallback: time.Minute}
	lc := newLazyCatalogue(s, nil, time.Minute)
	s.lazy.Store(lc)
	err = lc.watch.start()
	t.Cleanup(lc.watch.close)
	return lc, root, err
}

func writeNetfsFile(t *testing.T, root, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644))
}

// Task #351: on a network filesystem (here NFS) a watch never hears of a file
// another machine writes, so the lazy catalogue places none. No folder is ever
// current - every folder somebody opens is read from the disk and reconciled
// again - and the storage page is told why.
//
// Break: skip the probe in watchSet.start - the watcher opens, the reconciled
// root becomes current and is listed from the catalogue alone, and a file
// written onto the share afterwards is not shown.
func TestLazyWatch_ANetworkFilesystemIsNeverWatched(t *testing.T) {
	lc, root, err := lazyOnFS(t, netfs.Info{Type: "NFS", Network: true}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NFS")

	writeNetfsFile(t, root, "rapor.txt")
	res, err := lc.reconcile(context.Background(), "/", reasonOpen)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Added)
	assert.False(t, lc.current("/"), "an opened folder on NFS is never vouched for by the catalogue")
	assert.Zero(t, lc.watch.count())

	cov := lc.coverage(context.Background())
	assert.Equal(t, WatchOffNetworkFS, cov.WatchOff)
	assert.Equal(t, "NFS", cov.FSType)
}

// A root whose filesystem could not be told (a hung mount, a probe that
// failed) is not trusted either.
func TestLazyWatch_AnUnknownFilesystemIsNotWatched(t *testing.T) {
	lc, root, err := lazyOnFS(t, netfs.Info{}, false)
	require.Error(t, err)
	writeNetfsFile(t, root, "a.txt")
	_, err = lc.reconcile(context.Background(), "/", reasonOpen)
	require.NoError(t, err)
	assert.False(t, lc.current("/"))
	assert.Equal(t, WatchOffUnknownFS, lc.coverage(context.Background()).WatchOff)
}

// A local disk is watched as before, and the storage page says nothing about
// watches being off.
func TestLazyWatch_ALocalDiskIsWatched(t *testing.T) {
	lc, root, err := lazyOnFS(t, netfs.Info{}, true)
	require.NoError(t, err)
	writeNetfsFile(t, root, "a.txt")
	_, err = lc.reconcile(context.Background(), "/", reasonOpen)
	require.NoError(t, err)
	assert.True(t, lc.current("/"))
	assert.Empty(t, lc.coverage(context.Background()).WatchOff)
}

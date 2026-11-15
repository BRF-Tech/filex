package sync

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/netfs"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// runFSNotifyOn runs the Loop of an `fsnotify` storage over a fresh directory
// that the filesystem probe reports as info (ok: whether it could tell), with
// a poll cadence of 50 ms should it fall back (no interval of its own, the
// fallback set short). The loop is stopped when the test ends.
func runFSNotifyOn(t *testing.T, info netfs.Info, ok bool) (db.Store, *model.Storage) {
	t.Helper()
	was := probeWatchFS
	probeWatchFS = func(string) (netfs.Info, bool) { return info, ok }
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg := map[string]any{"root": root}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "paylasim", Driver: "local", MountPath: "/paylasim", ConfigJSON: raw,
		SyncMode: model.SyncModeFSNotify, Enabled: true,
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), cfg))
	ctx, cancel := context.WithCancel(context.Background())
	s := &storageSyncer{store: store, storage: st, driver: drv, rule: ruleFor(st, cfg), ctx: ctx, fallback: 50 * time.Millisecond}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Loop()
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		probeWatchFS = was
	})
	return store, st
}

// runCount is how many sync runs the storage has; -1 when they could not be
// read. Safe inside Eventually, which calls it off the test's goroutine.
func runCount(store db.Store, storageID int64) int {
	runs, err := store.ListSyncRuns(context.Background(), storageID, 50)
	if err != nil {
		return -1
	}
	return len(runs)
}

// Task #351: `fsnotify` on a network filesystem (or one whose kind could not
// be told) would hear what this machine writes and never what another machine
// writes to the share, so the storage falls back to the poll loop at its own
// interval - as it already did for a driver that is not local. Here the
// interval is 50 ms, so the poll loop's runs pile up without any file being
// written; the watch loop would run once (its first scan) and then wait for an
// event.
//
// Break: drop the watchUntrusted check from loopFSNotify - one run, then
// nothing, and the Eventually times out.
func TestFSNotify_ANetworkFilesystemFallsBackToPoll(t *testing.T) {
	for name, probe := range map[string]struct {
		info netfs.Info
		ok   bool
	}{
		"NFS":     {netfs.Info{Type: "NFS", Network: true}, true},
		"unknown": {netfs.Info{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			store, st := runFSNotifyOn(t, probe.info, probe.ok)
			require.Eventually(t, func() bool { return runCount(store, st.ID) >= 3 },
				10*time.Second, 20*time.Millisecond, "the storage is scanned at its interval, not watched")
		})
	}
}

// On a local disk the mode is what it says: one first scan, then a scan per
// burst of events, and nothing on a timer.
func TestFSNotify_ALocalDiskIsStillWatched(t *testing.T) {
	store, st := runFSNotifyOn(t, netfs.Info{}, true)
	require.Eventually(t, func() bool { return runCount(store, st.ID) >= 1 }, 10*time.Second, 20*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	// The 50 ms poll fallback would have run about ten times by now; a stray
	// event of the first scan's own may add one run, never a timer's worth.
	assert.Less(t, runCount(store, st.ID), 3, "no timer-driven scans on a watched local disk")
}

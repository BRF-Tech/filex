package sync_test

// A sync_runs row is opened when a run starts and closed when it ends, and the
// close could fail silently: FinishSyncRun ran on the run's own context, so a
// run that was cancelled — a shutdown, a storage edit that restarts its syncer,
// the ceiling on a manual scan — tried to record its end on a dead context,
// failed, and left the row `running` for ever. So did every run the process
// died in the middle of. Panels, the thumbnail backfill's "is a sync running?"
// check and the tombstone guard all read those rows.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestACancelledRunIsClosedAsAborted(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	gate, entered, release := newGate(t)
	st := treeStorage(t, store, "gate-test", map[string]any{"gate": gate})
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(context.Background(), st))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		w.Stop()
	})

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- w.Trigger(ctx, st.ID) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached the backend")
	}
	cancel()
	require.Error(t, <-errc, "a cancelled run is not a successful one")

	run, err := store.GetLastSyncRun(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Equal(t, "aborted", run.Status, "a run cut short must be closed, not left running")
	assert.NotNil(t, run.FinishedAt)
	assert.Contains(t, run.Error, "interrupted")
}

// listFailDriver answers every List with an error of its own — a backend that
// is down, which is a FAILED run, not an aborted one.
type listFailDriver struct{}

func init() {
	storage.Register("listfail-test", func() storage.Driver { return listFailDriver{} })
}

func (listFailDriver) Init(context.Context, map[string]any) error { return nil }
func (listFailDriver) Name() string                               { return "listfail-test" }
func (listFailDriver) Capabilities() storage.Capabilities         { return storage.Capabilities{Read: true} }
func (listFailDriver) List(context.Context, string) ([]storage.Object, error) {
	return nil, errors.New("503 Slow Down")
}
func (listFailDriver) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, storage.ErrNotFound
}
func (listFailDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, storage.ErrNotFound
}

func TestARunThatFailsOnItsOwnIsStillFailed(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	st := treeStorage(t, store, "listfail-test", map[string]any{})
	w := filexsync.New(store)
	require.NoError(t, w.AddStorage(context.Background(), st))
	t.Cleanup(w.Stop)
	require.Error(t, w.Trigger(context.Background(), st.ID))

	run, err := store.GetLastSyncRun(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status)
	assert.NotNil(t, run.FinishedAt)
	assert.Contains(t, run.Error, "503 Slow Down")
}

// A row the previous process never closed is closed when the worker starts,
// before any new run can open one of its own.
func TestWorkerStartClosesRunsAPreviousProcessLeftOpen(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, _ := localStorage(t, store)

	done, err := store.CreateSyncRun(ctx, st.ID, "")
	require.NoError(t, err)
	require.NoError(t, store.FinishSyncRun(ctx, done.ID, "", 5, 5, 0, 0, "ok", ""))
	orphan, err := store.CreateSyncRun(ctx, st.ID, "") // the process died here
	require.NoError(t, err)

	w := filexsync.New(store)
	require.NoError(t, w.Start(ctx))
	t.Cleanup(w.Stop)

	got, err := store.GetSyncRun(ctx, orphan.ID)
	require.NoError(t, err)
	assert.Equal(t, "aborted", got.Status)
	assert.NotNil(t, got.FinishedAt)
	assert.Equal(t, filexsync.AbortedAtStartup, got.Error)
	assert.Equal(t, "interrupted: the server stopped during the scan", got.Error)

	kept, err := store.GetSyncRun(ctx, done.ID)
	require.NoError(t, err)
	assert.Equal(t, "ok", kept.Status, "a finished run is history and is left alone")
	assert.Empty(t, kept.Error)
}

// The tombstone guard compares a run with the last one that FINISHED ok. An
// aborted or failed run records whatever it had counted when it stopped —
// usually 0 — and as the baseline that switched the guard off for the next
// run: half a bucket missing from one listing went straight to the trash.
func TestTombstoneGuardComparesWithTheLastRunThatFinishedOK(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	st, _, root := localStorage(t, store)
	for i := 0; i < 10; i++ {
		writeUnder(t, root, fmt.Sprintf("f%02d.txt", i), "x")
	}
	_, first := runSync(t, store, st)
	require.Equal(t, "ok", first.Status, first.Error)
	require.Equal(t, 10, first.SeenCount)

	waitPastSecondBoundary()
	broken, err := store.CreateSyncRun(ctx, st.ID, "")
	require.NoError(t, err)
	require.NoError(t, store.FinishSyncRun(ctx, broken.ID, "", 0, 0, 0, 0, "aborted", filexsync.AbortedAtStartup))

	// A listing that comes back half empty: the shape of a backend glitch.
	for i := 0; i < 5; i++ {
		require.NoError(t, os.Remove(filepath.Join(root, fmt.Sprintf("f%02d.txt", i))))
	}
	waitPastSecondBoundary()
	_, run := runSync(t, store, st)
	require.Equal(t, "ok", run.Status, run.Error)
	assert.Equal(t, 0, run.Deleted, "the guard must compare with the last ok run (10 seen), not the aborted one (0)")
	assert.Empty(t, trashedPaths(t, store, st.ID))
}

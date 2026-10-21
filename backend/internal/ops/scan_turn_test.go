package ops_test

// Issue #127 (chengjie): on a local storage of tens of thousands of entries,
// a folder delete queued while the storage scan ran sat in pending_ops as
// `pending` with no started_at for the scan's whole four or five minutes, the
// tray saying only "Delete · Queued", and ran two seconds after the scan
// ended. The scan holds the storage's row gate for one directory, lets go of
// it and takes it again microseconds later for the next one; the queue put the
// job back whenever a judgement held the gate and asked again only at its next
// round (a 5 s ticker), so every try met a judgement.
//
// These tests ask only what the queue promised before the fix as well: they
// fail on the queue of 0.57.4, where the job waits for a tick that always
// finds the scan holding the gate.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/rowgate"
)

// judgingLoop is a storage scan as it judges the storage: it holds the gate
// for one directory (hold), lets go of it and takes it for the next one at
// once, until the test ends. It answers how many directories it has judged.
func judgingLoop(t *testing.T, storageID int64, hold time.Duration) *atomic.Int64 {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			release, err := rowgate.JudgeWithin(ctx, storageID, 30*time.Second)
			if err != nil {
				continue
			}
			n.Add(1)
			select {
			case <-ctx.Done():
			case <-time.After(hold):
			}
			release()
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(5 * time.Second)
	for n.Load() == 0 {
		require.False(t, time.Now().After(deadline), "the scan never started")
		time.Sleep(time.Millisecond)
	}
	return &n
}

// scanWentOn waits until the scan has judged past was.
func scanWentOn(t *testing.T, n *atomic.Int64, was int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n.Load() <= was {
		require.False(t, time.Now().After(deadline), "the scan did not go on after the job")
		time.Sleep(time.Millisecond)
	}
}

// Break: in Service.drain, put the job back without announcing it
// (s.putBack instead of s.holdBack) - it waits for the 5 s tick, and every
// tick finds the scan holding the gate.
func TestOpsWorker_ADeleteQueuedWhileTheStorageIsScannedStartsAtOnce(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	f.seedIn(t, "Klasor", "a.txt", "a")
	ctx := context.Background()
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)

	n := judgingLoop(t, f.st.ID, 200*time.Millisecond)
	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	require.Equal(t, ops.StatusOK, done.Status, "the delete: %s", done.Error)
	require.True(t, f.trashed(t, "Klasor"), "the folder is still live after the delete")
	_, err = f.drv.Stat(ctx, "Klasor")
	require.Error(t, err, "the folder's bytes are still at its path")
	scanWentOn(t, n, n.Load())
	f.svc.Stop()
}

// The finishing lane (renames, restores, purges) is woken the same way.
//
// Break: in runOneFinishing, put the job back without announcing it.
func TestOpsWorker_ARenameQueuedWhileTheStorageIsScannedStartsAtOnce(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	ctx := context.Background()
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)

	n := judgingLoop(t, f.st.ID, 200*time.Millisecond)
	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
	require.NoError(t, err)
	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	require.Equal(t, ops.StatusOK, done.Status, "the rename: %s", done.Error)
	_, err = f.drv.Stat(ctx, "Leo/a.txt")
	require.NoError(t, err, "the rename did not land")
	scanWentOn(t, n, n.Load())
	f.svc.Stop()
}

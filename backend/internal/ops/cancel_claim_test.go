package ops_test

// #282: a job its lane has just claimed - `running`, with no cancel handle
// yet - can be cancelled, and the lane ends it instead of starting it or
// putting it back; a job waiting for a scan of its storage is not claimed
// again at every round of its lane.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// holdClaim stops the lane the first time it claims row id, until release:
// the instant between the claim and the lane starting the job or putting it
// back. claimed closes once the lane is there.
func holdClaim(t *testing.T, id int64) (claimed <-chan struct{}, release func()) {
	t.Helper()
	there := make(chan struct{})
	gate := make(chan struct{})
	var first, let sync.Once
	restore := ops.SetClaimHook(func(got int64) {
		if got == id {
			first.Do(func() {
				close(there)
				<-gate
			})
		}
	})
	release = func() { let.Do(func() { close(gate) }) }
	t.Cleanup(func() {
		release()
		restore()
	})
	return there, release
}

func waitClaimed(t *testing.T, claimed <-chan struct{}) {
	t.Helper()
	select {
	case <-claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("the lane never claimed the job")
	}
}

// Break: drop the claimed look from Cancel - it answers false while the lane
// holds the row claimed, as it did up to 0.57.5.
func TestOpsWorker_AJobCancelledAsItIsClaimedEndsWithoutRunning(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	claimed, release := holdClaim(t, op.ID)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	waitClaimed(t, claimed)

	ok, err := f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	require.True(t, ok, "a job its lane had just claimed could not be cancelled")
	release()

	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	assert.Equal(t, ops.StatusCancelled, done.Status)
	_, err = f.drv.Stat(ctx, "Klasor")
	assert.NoError(t, err, "the cancelled delete ran")
	f.svc.Stop()
}

// Break: drop the claimed look from putBack - the cancelled job goes back to
// the queue, announced to the gate, and runs once the scan lets go.
func TestOpsWorker_AJobCancelledAsItIsClaimedIsNotPutBackForAScan(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	ctx := context.Background()
	directory := rowgate.Judge(f.st.ID)
	let := false
	letGo := func() {
		if !let {
			let = true
			directory()
		}
	}
	t.Cleanup(letGo)

	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	claimed, release := holdClaim(t, op.ID)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	waitClaimed(t, claimed)

	ok, err := f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	require.True(t, ok, "a job its lane had just claimed could not be cancelled")
	release()

	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	assert.Equal(t, ops.StatusCancelled, done.Status)
	assert.Empty(t, done.WaitReason, "a cancelled job says it waits")
	assert.Zero(t, waitingOn(f.st.ID), "the cancelled job was announced to its storage's gate")

	letGo()
	f.svc.Poke()
	time.Sleep(50 * time.Millisecond)
	_, err = f.drv.Stat(ctx, "Klasor")
	assert.NoError(t, err, "the cancelled delete ran once the scan let go")
	f.svc.Stop()
}

// A rename cannot be cancelled once it runs, but it can before it starts.
//
// Break: register the claimed rename as started in begin whatever Cancel
// noted - the rename runs.
func TestOpsWorker_ARenameCancelledAsItIsClaimedIsNotDone(t *testing.T) {
	f := newOpsFixture(t)
	f.seedFile(t, "a.txt", "x")
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"a.txt"}, "b.txt")
	require.NoError(t, err)
	claimed, release := holdClaim(t, op.ID)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	waitClaimed(t, claimed)

	ok, err := f.svc.Cancel(ctx, op.ID)
	require.NoError(t, err)
	require.True(t, ok, "a rename its lane had just claimed could not be cancelled")
	release()

	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	assert.Equal(t, ops.StatusCancelled, done.Status)
	_, err = f.drv.Stat(ctx, "a.txt")
	assert.NoError(t, err, "the cancelled rename ran")
	_, err = f.drv.Stat(ctx, "b.txt")
	assert.Error(t, err, "the cancelled rename ran")
	f.svc.Stop()
}

// Break: start drain's round with no storage held (drop stillJudged) - every
// round claims the job again, only to put it back.
func TestOpsWorker_AJobWaitingForAScanIsNotClaimedAgainEveryRound(t *testing.T) {
	f := newOpsFixture(t)
	f.seedDir(t, "Klasor")
	ctx := context.Background()
	directory := rowgate.Judge(f.st.ID)
	let := false
	letGo := func() {
		if !let {
			let = true
			directory()
		}
	}
	t.Cleanup(letGo)

	op, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"Klasor"}, "")
	require.NoError(t, err)
	var claims atomic.Int32
	defer ops.SetClaimHook(func(id int64) {
		if id == op.ID {
			claims.Add(1)
		}
	})()
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	heldBack(t, f.svc, op.ID, "en")

	reader := srvtext.WithReader(ctx, "en")
	for round := 0; round < 5; round++ {
		f.svc.Poke()
		time.Sleep(20 * time.Millisecond)
		cur, err := f.svc.Get(reader, op.ID)
		require.NoError(t, err)
		require.Equal(t, ops.StatusPending, cur.Status, "round %d: the job waiting for the scan was claimed again", round)
		require.Equal(t, "Waiting for its turn: the storage is being scanned", cur.WaitText, "round %d", round)
	}
	assert.EqualValues(t, 1, claims.Load(), "the job waiting for the scan was claimed again")

	letGo()
	done := waitForOpWithin(t, f.svc, op.ID, 3*time.Second)
	require.Equal(t, ops.StatusOK, done.Status, "the delete: %s", done.Error)
	f.svc.Stop()
}

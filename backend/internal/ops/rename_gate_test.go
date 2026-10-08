package ops_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/rowgate"
)

// Issue #192: a queued rename holds the storage's row gate from the first byte
// it moves to the last row that follows (internal/rowgate), so the storage
// scan never judges the catalogue half way through it. Judged half way - the
// bytes at the new name, the rows at the old one - a folder's contents looked
// gone and were dropped, and the renamed folder opened empty (e2e 159 and 172).
//
// Break: take rowgate.Move out of runRename - the scan's judgement gets the
// gate while the rows still sit at the old name.
func TestOpsWorker_Rename_HoldsTheRowGateUntilTheRowsFollow(t *testing.T) {
	moved := make(chan struct{})
	letGo := make(chan struct{})
	var movedOnce, letGoOnce sync.Once
	finish := func() { letGoOnce.Do(func() { close(letGo) }) }
	// The gate is process-wide: whatever happens below, the rename finishes.
	t.Cleanup(finish)
	drv := &movingDriver{afterMove: func() {
		movedOnce.Do(func() { close(moved) })
		<-letGo
	}}
	f := newPoolFixture(t, 1, drv)
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	ctx := context.Background()

	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go f.svc.Run(run)
	select {
	case <-moved:
	case <-time.After(10 * time.Second):
		t.Fatal("the rename never moved its bytes")
	}

	// Half way: the bytes at the new name, the rows at the old one. A
	// judgement asks for the gate and says what the catalogue held when it got
	// it.
	judged := make(chan bool, 1)
	go func() {
		release := rowgate.Judge(f.st.ID)
		n, _ := f.store.GetNodeByPath(context.Background(), f.st.ID, opsPathHash(f.st.ID, "Leo/a.txt"))
		release()
		judged <- n != nil
	}()
	select {
	case <-judged:
		t.Fatal("the scan's judgement got the gate while the rename was half way")
	case <-time.After(200 * time.Millisecond):
	}

	finish()
	select {
	case rowsFollowed := <-judged:
		assert.True(t, rowsFollowed, "the gate opened before the rows followed the bytes")
	case <-time.After(10 * time.Second):
		t.Fatal("the judgement never got the gate after the rename finished")
	}
	done := waitForOp(t, f.svc, op.ID)
	f.svc.Stop()
	assert.Equal(t, ops.StatusOK, done.Status, "rename op error: %s", done.Error)
	assert.Equal(t, "a", readAll(t, f, "Leo/a.txt"))
}

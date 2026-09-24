package ops_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/ops"
)

func waitForOp(t *testing.T, svc *ops.Service, id int64) *ops.Op {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, err := svc.Get(context.Background(), id)
		require.NoError(t, err)
		if op.Status == ops.StatusOK || op.Status == ops.StatusFailed || op.Status == ops.StatusPartial || op.Status == ops.StatusCancelled {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("op %d did not finish (status %s)", id, op.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOps_CancelPendingArchiveJobRunsCleanupWithoutStarting(t *testing.T) {
	f := newOpsFixture(t)
	ran := false
	cleaned := false
	op, err := f.svc.SubmitJobWithCleanup(context.Background(), ops.OpArchiveExtract, f.st.ID,
		[]string{"main://source/bundle.7z"}, "output", 2,
		func(context.Context, func(int)) error {
			ran = true
			return nil
		}, func() { cleaned = true })
	require.NoError(t, err)

	cancelled, err := f.svc.Cancel(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, cancelled)
	stored, err := f.svc.Get(context.Background(), op.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusCancelled, stored.Status)
	assert.False(t, ran)
	assert.True(t, cleaned)
}

func TestOps_CancelRunningArchiveJobKeepsCompletedProgress(t *testing.T) {
	f := newOpsFixture(t)
	started := make(chan struct{})
	op, err := f.svc.SubmitJob(context.Background(), ops.OpArchiveExtract, f.st.ID,
		[]string{"main://source/bundle.7z"}, "output", 3,
		func(ctx context.Context, progress func(int)) error {
			progress(1)
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	require.NoError(t, err)

	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("job did not start")
	}

	requested, err := f.svc.Cancel(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, requested)
	stored, err := f.svc.Get(context.Background(), op.ID)
	require.NoError(t, err)
	assert.Contains(t, []string{ops.StatusRunning, ops.StatusCancelled}, stored.Status)
	finished := waitForOp(t, f.svc, op.ID)
	assert.Equal(t, ops.StatusCancelled, finished.Status)
	assert.Equal(t, 1, finished.Done)
	assert.Empty(t, finished.Error)
}

func TestOps_ArchiveJobReportsProgressAndKeepsSafeMetadata(t *testing.T) {
	f := newOpsFixture(t)
	op, err := f.svc.SubmitJob(context.Background(), ops.OpArchiveCreate, f.st.ID,
		[]string{"main://source/a.txt", "main://source/b.txt"}, "archives/bundle.7z", 3,
		func(_ context.Context, progress func(int)) error {
			progress(1)
			progress(2)
			return nil
		})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()

	finished := waitForOp(t, f.svc, op.ID)
	assert.Equal(t, ops.StatusOK, finished.Status)
	assert.Equal(t, 3, finished.Done)
	assert.Equal(t, 3, finished.Total)
	assert.Equal(t, "archives/bundle.7z", finished.Dest)
	assert.Equal(t, []string{"main://source/a.txt", "main://source/b.txt"}, finished.Sources)
}

func TestOps_ArchiveJobFailureIsVisible(t *testing.T) {
	f := newOpsFixture(t)
	op, err := f.svc.SubmitJob(context.Background(), ops.OpArchiveCreate, f.st.ID,
		[]string{"main://source/a.txt"}, "archives/bundle.zip", 2,
		func(_ context.Context, progress func(int)) error {
			progress(1)
			return errors.New("compressor stopped")
		})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()

	finished := waitForOp(t, f.svc, op.ID)
	assert.Equal(t, ops.StatusFailed, finished.Status)
	assert.Equal(t, 1, finished.Done)
	assert.Equal(t, 1, finished.Failed)
	assert.Contains(t, finished.Error, "compressor stopped")
}

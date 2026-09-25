package ops_test

import (
	"context"
	"errors"
	"sync"
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

// An archive job runs as long as 7-Zip takes — minutes, or the whole
// configured timeout. It must not hold the queue's single worker while it
// does: every copy, move, delete and staged-upload commit on the instance
// would wait behind it (the same trap trash-empty fell into, lesson #444).
func TestOps_ArchiveJobDoesNotHoldTheWorker(t *testing.T) {
	f := newOpsFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	job, err := f.svc.SubmitJob(context.Background(), ops.OpArchiveCreate, f.st.ID,
		[]string{"main://source/huge"}, "archives/huge.7z", 1,
		func(ctx context.Context, _ func(int)) error {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	var releaseOnce sync.Once
	let := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		let()
		cancel()
		f.svc.Stop()
	}()
	go f.svc.Run(runCtx)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("archive job did not start")
	}

	del, err := f.svc.Submit(context.Background(), ops.OpDelete, f.st.ID, []string{"nothing-here.txt"}, "")
	require.NoError(t, err)
	got := waitForOp(t, f.svc, del.ID)
	assert.NotEqual(t, ops.StatusPending, got.Status, "the worker was free")
	cur, err := f.svc.Get(context.Background(), job.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusRunning, cur.Status, "while the archive job is still going")

	let()
	assert.Equal(t, ops.StatusOK, waitForOp(t, f.svc, job.ID).Status)
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

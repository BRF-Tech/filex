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

// fakeRunner is a PluginRunner that either finishes at once, fails, or
// blocks until the op's context is cancelled — which is what a wasm instance
// does when the worker's context ends.
type fakeRunner struct {
	mu      sync.Mutex
	seen    []string
	block   bool
	started chan struct{}
	err     error
}

func (f *fakeRunner) RunPluginAction(ctx context.Context, op *ops.Op, live func(done, total int64)) error {
	f.mu.Lock()
	f.seen = append(f.seen, op.Dest)
	f.mu.Unlock()
	if f.started != nil {
		close(f.started)
	}
	live(1, 4)
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}

func TestOps_PluginAction_DispatchesToRunnerAsOneUnit(t *testing.T) {
	f := newOpsFixture(t)
	r := &fakeRunner{}
	f.svc.SetPluginRunner(r)
	f.svc.SetDecorator(func(_ context.Context, rows []*ops.Op) {
		for _, op := range rows {
			op.Plugin, op.Label = "echo", "Upper-case"
		}
	})

	op := f.runOp(t, ops.OpPluginAction, []string{"a.txt", "b.txt"}, "job-1")
	assert.Equal(t, ops.StatusOK, op.Status)
	assert.Equal(t, 2, op.Done, "done=total: one job, however many inputs")
	assert.Equal(t, 0, op.Failed)
	assert.Equal(t, []string{"job-1"}, r.seen, "the job id travels in dest")
	assert.Equal(t, "echo", op.Plugin, "Get runs the decorator")
	assert.Equal(t, "Upper-case", op.Label)
}

func TestOps_PluginAction_FailureIsFailedNotPartial(t *testing.T) {
	f := newOpsFixture(t)
	f.svc.SetPluginRunner(&fakeRunner{err: errors.New("the plugin said no")})
	op := f.runOp(t, ops.OpPluginAction, []string{"a.txt"}, "job-2")
	assert.Equal(t, ops.StatusFailed, op.Status)
	assert.Equal(t, "the plugin said no", op.Error)
	assert.Equal(t, 1, op.Failed)
}

func TestOps_PluginAction_NoRunnerFailsLoudly(t *testing.T) {
	f := newOpsFixture(t)
	op := f.runOp(t, ops.OpPluginAction, []string{"a.txt"}, "job-3")
	assert.Equal(t, ops.StatusFailed, op.Status)
	assert.Contains(t, op.Error, "no plugin runner")
}

func TestOps_PluginAction_RequiresDest(t *testing.T) {
	f := newOpsFixture(t)
	_, err := f.svc.Submit(context.Background(), ops.OpPluginAction, f.st.ID, []string{"a.txt"}, "")
	require.Error(t, err)
}

func TestOps_Cancel_PendingRowNeverRuns(t *testing.T) {
	f := newOpsFixture(t)
	r := &fakeRunner{}
	f.svc.SetPluginRunner(r)
	op, err := f.svc.Submit(context.Background(), ops.OpPluginAction, f.st.ID, []string{"a.txt"}, "job-4")
	require.NoError(t, err)
	ok, err := f.svc.Cancel(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	cur, _ := f.svc.Get(context.Background(), op.ID)
	assert.Equal(t, ops.StatusCancelled, cur.Status)

	// The worker skips it: only pending rows are claimed.
	runCtx, cancel := context.WithCancel(context.Background())
	go f.svc.Run(runCtx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	f.svc.Stop()
	assert.Empty(t, r.seen)

	again, _ := f.svc.Cancel(context.Background(), op.ID)
	assert.False(t, again, "a finished row is not cancellable")
}

// opsWait is the ceiling these tests give a state they expect in
// milliseconds. It is deliberately far above the real wait: a shared CI
// runner under -race schedules goroutines late, and a tight ceiling turns a
// correct service into a red build (0.56.1 gate, 8.24s). A broken service
// still fails here, it just takes longer to say so.
const opsWait = 30 * time.Second

func TestOps_Cancel_RunningRowEndsTheContext(t *testing.T) {
	f := newOpsFixture(t)
	r := &fakeRunner{block: true, started: make(chan struct{})}
	f.svc.SetPluginRunner(r)
	op, err := f.svc.Submit(context.Background(), ops.OpPluginAction, f.st.ID, []string{"a.txt"}, "job-5")
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.svc.Run(runCtx)
	defer f.svc.Stop()
	select {
	case <-r.started:
	case <-time.After(opsWait):
		t.Fatal("runner never started")
	}
	// The runner closes `started` BEFORE its first live() call, so the row's
	// progress lands a moment later: reading it straight away is a race the
	// -race build loses on a loaded runner (CI run 38174576369). Wait for the
	// state, never for a duration.
	var cur *ops.Op
	require.Eventually(t, func() bool {
		cur, _ = f.svc.Get(context.Background(), op.ID)
		return cur != nil && cur.Status == ops.StatusRunning && cur.BytesDone == 1 && cur.BytesTotal == 4
	}, opsWait, 15*time.Millisecond, "the running row carries the runner's live progress")

	ok, err := f.svc.Cancel(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Eventually(t, func() bool {
		cur, _ = f.svc.Get(context.Background(), op.ID)
		return cur != nil && cur.Status == ops.StatusCancelled
	}, opsWait, 15*time.Millisecond, "a cancelled run ends as cancelled")
	assert.Empty(t, cur.Error, "cancelled is not an error")
}

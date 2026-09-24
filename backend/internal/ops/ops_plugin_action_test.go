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
	case <-time.After(3 * time.Second):
		t.Fatal("runner never started")
	}
	cur, _ := f.svc.Get(context.Background(), op.ID)
	assert.Equal(t, ops.StatusRunning, cur.Status)
	assert.Equal(t, int64(1), cur.BytesDone, "live progress from the runner")
	assert.Equal(t, int64(4), cur.BytesTotal)

	ok, err := f.svc.Cancel(context.Background(), op.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	deadline := time.Now().Add(3 * time.Second)
	for {
		cur, _ = f.svc.Get(context.Background(), op.ID)
		if cur.Status == ops.StatusCancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("op did not end as cancelled; status=%s", cur.Status)
		}
		time.Sleep(15 * time.Millisecond)
	}
	assert.Empty(t, cur.Error, "cancelled is not an error")
}

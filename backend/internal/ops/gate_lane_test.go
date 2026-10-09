package ops_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/rowgate"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// sec055: the operations queue's worker runs every tenant's moves, deletes,
// copies and upload commits one job at a time. A job that holds its storage's
// row gate used to wait in rowgate.Move while a judgement of that storage held
// (or, up to 0.55's first cut, merely waited for) the gate - and every job of
// every other storage queued behind it waited too. Now a job whose storage is
// being judged goes back to the queue, and the other storages' jobs run
// (gate_lane.go); the job runs once the judgement is over.
//
// Break: in Service.drain, execute the claimed job without asking
// waitsForAJudgement - the worker stands in storage A's gate and storage B's
// move never finishes.
func TestOpsWorker_AStorageBeingJudgedDoesNotHoldAnotherStoragesJobs(t *testing.T) {
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	drivers := map[int64]*local.Driver{}
	mk := func(name string) *model.Storage {
		dir := t.TempDir()
		base := &local.Driver{}
		require.NoError(t, base.Init(ctx, map[string]any{"root": dir}))
		cfg, _ := json.Marshal(map[string]any{"root": dir})
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, ConfigJSON: cfg,
		})
		require.NoError(t, err)
		drivers[st.ID] = base
		return st
	}
	stA, stB := mk("tenant-a"), mk("tenant-b")
	resolver := func(id int64) (storage.Driver, error) {
		d, ok := drivers[id]
		if !ok {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return d, nil
	}
	require.NoError(t, drivers[stA.ID].Write(ctx, "a.txt", strings.NewReader("a"), 1))
	require.NoError(t, drivers[stB.ID].Write(ctx, "b.txt", strings.NewReader("b"), 1))
	svc := ops.New(sqlDB, resolver)
	require.NoError(t, svc.Migrate(ctx))
	svc.SetSync(handlers.NewManager(store, resolver))

	// A scan is judging storage A: it holds A's gate for one directory.
	judged := rowgate.Judge(stA.ID)
	let := false
	letGo := func() {
		if !let {
			let = true
			judged()
		}
	}
	t.Cleanup(letGo)

	// A's job is older: the worker reaches it first.
	opA, err := svc.Submit(ctx, ops.OpMove, stA.ID, []string{"a.txt"}, "moved/")
	require.NoError(t, err)
	opB, err := svc.Submit(ctx, ops.OpMove, stB.ID, []string{"b.txt"}, "moved/")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go svc.Run(run)

	doneB := waitForOp(t, svc, opB.ID)
	require.Equal(t, ops.StatusOK, doneB.Status, "storage B's move: %s", doneB.Error)
	_, err = drivers[stB.ID].Stat(ctx, "moved/b.txt")
	require.NoError(t, err, "storage B's move did not land")

	// A's job waits for the judgement - back in the queue, not failed.
	waitForPending(t, svc, opA.ID, 10*time.Second, "a job whose storage is being judged must wait in the queue")
	_, err = drivers[stA.ID].Stat(ctx, "a.txt")
	require.NoError(t, err, "storage A changed while it was being judged")

	letGo()
	doneA := waitForOpWithin(t, svc, opA.ID, 20*time.Second)
	require.Equal(t, ops.StatusOK, doneA.Status, "storage A's move: %s", doneA.Error)
	_, err = drivers[stA.ID].Stat(ctx, "moved/a.txt")
	require.NoError(t, err, "storage A's move did not land once the judgement was over")
	svc.Stop()
}

// waitForPending waits until a job put back for its storage's gate reads
// pending. A round of the worker claims the job (running) and puts it back
// at once, so one read can catch it in between; what may never happen is a
// finish (ok, failed, partial, cancelled) while the judgement holds.
func waitForPending(t *testing.T, svc *ops.Service, id int64, d time.Duration, why string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		op, err := svc.Get(context.Background(), id)
		require.NoError(t, err)
		switch op.Status {
		case ops.StatusPending:
			return
		case ops.StatusOK, ops.StatusFailed, ops.StatusPartial, ops.StatusCancelled:
			t.Fatalf("%s: op %d finished (status %s, error %q)", why, id, op.Status, op.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: op %d did not go back to the queue (status %s)", why, id, op.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForOpWithin is waitForOp with a deadline of its own: a job put back for
// its storage's gate runs at the worker's next round (its 5 s ticker).
func waitForOpWithin(t *testing.T, svc *ops.Service, id int64, d time.Duration) *ops.Op {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		op, err := svc.Get(context.Background(), id)
		require.NoError(t, err)
		switch op.Status {
		case ops.StatusOK, ops.StatusFailed, ops.StatusPartial, ops.StatusCancelled:
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("op %d did not finish (status %s)", id, op.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

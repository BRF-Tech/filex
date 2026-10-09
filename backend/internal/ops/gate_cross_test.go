package ops_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
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

// sec055, the cross-storage move (ops crossTransfer): it ends by deleting its
// source under the source storage's row gate. Up to the first sec055 round it
// took that gate with rowgate.Change - no context, and the queue's one worker
// waited in it for as long as a judgement of the source storage held it.
// Now a move does not start while the source storage is being judged (the
// job goes back to the queue, and the other storages' jobs run), and once its
// bytes are on the far side it waits for the gate on the job's own context,
// so a Cancel ends the wait and keeps the source.

// hookedDriver is a local driver with a hook on the first Stat and the first
// Write of a path.
type hookedDriver struct {
	*local.Driver
	onStat, onWrite func(p string)
	statOnce        sync.Once
	writeOnce       sync.Once
}

func (h *hookedDriver) Stat(ctx context.Context, p string) (storage.Object, error) {
	if h.onStat != nil && strings.Contains(p, "a.txt") {
		h.statOnce.Do(func() { h.onStat(p) })
	}
	return h.Driver.Stat(ctx, p)
}

func (h *hookedDriver) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	err := h.Driver.Write(ctx, p, r, size)
	if err == nil && h.onWrite != nil && strings.Contains(p, "a.txt") {
		h.writeOnce.Do(func() { h.onWrite(p) })
	}
	return err
}

type crossRig struct {
	svc      *ops.Service
	drivers  map[int64]storage.Driver
	locals   map[int64]*local.Driver
	a, b, c  *model.Storage
	hookedTo *hookedDriver
}

// newCrossRig is three storages: a (the move's source), b (its destination,
// served through hookedDriver) and c (another tenant's).
func newCrossRig(t *testing.T) *crossRig {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	r := &crossRig{drivers: map[int64]storage.Driver{}, locals: map[int64]*local.Driver{}}
	mk := func(name string) *model.Storage {
		dir := t.TempDir()
		base := &local.Driver{}
		require.NoError(t, base.Init(ctx, map[string]any{"root": dir}))
		cfg, _ := json.Marshal(map[string]any{"root": dir})
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, ConfigJSON: cfg,
		})
		require.NoError(t, err)
		r.locals[st.ID] = base
		r.drivers[st.ID] = base
		return st
	}
	r.a, r.b, r.c = mk("cross-a"), mk("cross-b"), mk("cross-c")
	r.hookedTo = &hookedDriver{Driver: r.locals[r.b.ID]}
	r.drivers[r.b.ID] = r.hookedTo
	resolver := func(id int64) (storage.Driver, error) {
		d, ok := r.drivers[id]
		if !ok {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return d, nil
	}
	require.NoError(t, r.locals[r.a.ID].Write(ctx, "a.txt", strings.NewReader("a"), 1))
	require.NoError(t, r.locals[r.c.ID].Write(ctx, "c.txt", strings.NewReader("c"), 1))
	r.svc = ops.New(sqlDB, resolver)
	require.NoError(t, r.svc.Migrate(ctx))
	r.svc.SetSync(handlers.NewManager(store, resolver))
	return r
}

func (r *crossRig) exists(id int64, p string) bool {
	_, err := r.locals[id].Stat(context.Background(), p)
	return err == nil
}

// Break: in crossTransfer, drop the rowgate.Judged question before the copy -
// the move copies, then stands in the source's gate, and storage c's job
// never runs.
func TestOpsWorker_ACrossMoveGoesBackWhileItsSourceIsBeingJudged(t *testing.T) {
	r := newCrossRig(t)
	ctx := context.Background()
	var judged func()
	var mu sync.Mutex
	letGo := func() {
		mu.Lock()
		defer mu.Unlock()
		if judged != nil {
			judged()
			judged = nil
		}
	}
	t.Cleanup(letGo)
	// A scan of storage a takes its gate just as the move picks its name on
	// b (before a byte travels).
	r.hookedTo.onStat = func(string) {
		mu.Lock()
		judged = rowgate.Judge(r.a.ID)
		mu.Unlock()
	}

	move, err := r.svc.SubmitTo(ctx, ops.OpMove, r.a.ID, r.b.ID, []string{"a.txt"}, "/")
	require.NoError(t, err)
	other, err := r.svc.Submit(ctx, ops.OpMove, r.c.ID, []string{"c.txt"}, "moved/")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go r.svc.Run(run)

	done := waitForOp(t, r.svc, other.ID)
	require.Equal(t, ops.StatusOK, done.Status, "storage c's move: %s", done.Error)
	require.True(t, r.exists(r.c.ID, "moved/c.txt"))

	waitForPending(t, r.svc, move.ID, 10*time.Second, "a move whose source is being judged must wait in the queue, not fail")
	require.True(t, r.exists(r.a.ID, "a.txt"), "the source left while its storage was being judged")
	require.False(t, r.exists(r.b.ID, "a.txt"), "the move copied while its source storage was being judged")

	letGo()
	finished := waitForOpWithin(t, r.svc, move.ID, 20*time.Second)
	require.Equal(t, ops.StatusOK, finished.Status, "the move once the judgement was over: %s", finished.Error)
	require.True(t, r.exists(r.b.ID, "a.txt"), "the move did not land")
	require.False(t, r.exists(r.a.ID, "a.txt"), "the move kept its source")
	require.Equal(t, 1, finished.Done)
	r.svc.Stop()
}

// Break: take the source's gate with rowgate.Change again in crossTransfer -
// the Cancel does not reach the wait, and the job stands in the gate until
// the judgement ends.
func TestOpsWorker_ACancelledCrossMoveKeepsItsSourceWhileTheGateIsJudged(t *testing.T) {
	r := newCrossRig(t)
	ctx := context.Background()
	var judged func()
	var mu sync.Mutex
	letGo := func() {
		mu.Lock()
		defer mu.Unlock()
		if judged != nil {
			judged()
			judged = nil
		}
	}
	t.Cleanup(letGo)
	copied := make(chan struct{})
	// The bytes have landed on b; a scan of storage a takes its gate before
	// the move deletes the source.
	r.hookedTo.onWrite = func(string) {
		mu.Lock()
		judged = rowgate.Judge(r.a.ID)
		mu.Unlock()
		close(copied)
	}

	move, err := r.svc.SubmitTo(ctx, ops.OpMove, r.a.ID, r.b.ID, []string{"a.txt"}, "/")
	require.NoError(t, err)
	run, stop := context.WithCancel(ctx)
	defer stop()
	go r.svc.Run(run)
	select {
	case <-copied:
	case <-time.After(10 * time.Second):
		t.Fatal("the move never copied")
	}
	time.Sleep(200 * time.Millisecond) // the move now waits for the source's gate
	ok, err := r.svc.Cancel(ctx, move.ID)
	require.NoError(t, err)
	require.True(t, ok, "the running move could not be cancelled")

	done := waitForOp(t, r.svc, move.ID)
	require.Equal(t, ops.StatusCancelled, done.Status, "error: %s", done.Error)
	require.True(t, r.exists(r.a.ID, "a.txt"), "a cancelled move deleted its source")
	require.True(t, r.exists(r.b.ID, "a.txt"), "the copy that had landed is gone")
	letGo()
	r.svc.Stop()
}

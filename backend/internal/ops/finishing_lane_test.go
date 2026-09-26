package ops_test

// Renames, restores and purges on a lane of their own (runFinishingLane).
//
// ⚠⚠ Each of them lasts as long as its folder does — a folder on an object
// store is renamed, restored or purged one object at a time — and they ran on
// the queue's single worker, where every copy, move, delete and staged-upload
// commit of the instance waited behind them (lesson #444; the trap "empty the
// trash" and the archive jobs fell into first).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// hold is a point a test stops a job at until it lets it go.
type hold struct {
	entered chan struct{}
	release chan struct{}
	in      sync.Once
	out     sync.Once
}

func newHold() *hold {
	return &hold{entered: make(chan struct{}), release: make(chan struct{})}
}

func (h *hold) wait() {
	h.in.Do(func() { close(h.entered) })
	<-h.release
}

func (h *hold) let() { h.out.Do(func() { close(h.release) }) }

// heldMover is the local driver with the move of one path held.
type heldMover struct {
	*local.Driver
	path string
	h    *hold
}

func (d *heldMover) Move(ctx context.Context, src, dst string) error {
	if strings.Trim(src, "/") == d.path {
		d.h.wait()
	}
	return d.Driver.Move(ctx, src, dst)
}

// heldTrash is a Restorer and a Purger whose first entry is held, and which
// records every entry it was handed.
type heldTrash struct {
	h     *hold
	mu    sync.Mutex
	calls []int64
}

func (t *heldTrash) take(id int64) error {
	t.mu.Lock()
	first := len(t.calls) == 0
	t.calls = append(t.calls, id)
	t.mu.Unlock()
	if first {
		t.h.wait()
	}
	return nil
}

func (t *heldTrash) RestoreNode(_ context.Context, _ int64, id int64) error { return t.take(id) }
func (t *heldTrash) PurgeNode(_ context.Context, _ int64, id int64) error   { return t.take(id) }

func (t *heldTrash) seen() []int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]int64(nil), t.calls...)
}

// newLaneFixture is newOpsFixture over a driver the test wraps, with the raw
// database a restart is simulated on.
func newLaneFixture(t *testing.T, wrap func(*local.Driver) storage.Driver) (*opsFixture, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	dir := t.TempDir()
	base := &local.Driver{}
	require.NoError(t, base.Init(ctx, map[string]any{"root": dir}))
	drv := wrap(base)
	cfg, _ := json.Marshal(map[string]any{"root": dir})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true, ConfigJSON: cfg,
	})
	require.NoError(t, err)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	svc := ops.New(sqlDB, resolver)
	require.NoError(t, svc.Migrate(ctx))
	svc.SetSync(handlers.NewManager(store, resolver))
	return &opsFixture{svc: svc, store: store, drv: base, st: st}, sqlDB
}

// statusWithin polls op id until it leaves pending/running or the time is up,
// and returns what it last read.
func statusWithin(t *testing.T, svc *ops.Service, id int64, d time.Duration) *ops.Op {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		op, err := svc.Get(context.Background(), id)
		require.NoError(t, err)
		if (op.Status != ops.StatusPending && op.Status != ops.StatusRunning) || time.Now().After(deadline) {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFinishingLane_DoesNotHoldTheWorker(t *testing.T) {
	for _, kind := range []string{ops.OpRename, ops.OpRestore, ops.OpPurge} {
		t.Run(kind, func(t *testing.T) {
			h := newHold()
			t.Cleanup(h.let)
			f, _ := newLaneFixture(t, func(d *local.Driver) storage.Driver {
				return &heldMover{Driver: d, path: "Leon", h: h}
			})
			ht := &heldTrash{h: h}
			f.svc.SetRestorer(ht)
			f.svc.SetPurger(ht)
			f.seedDir(t, "Leon")
			f.seedFile(t, "sil.txt", "x")
			ctx := context.Background()

			var long *ops.Op
			var err error
			if kind == ops.OpRename {
				long, err = f.svc.Submit(ctx, kind, f.st.ID, []string{"Leon"}, "Leo")
			} else {
				long, err = f.svc.Submit(ctx, kind, f.st.ID, []string{"7"}, "")
			}
			require.NoError(t, err)
			run, cancel := context.WithCancel(ctx)
			defer cancel()
			go f.svc.Run(run)
			defer f.svc.Stop()
			select {
			case <-h.entered:
			case <-time.After(10 * time.Second):
				t.Fatalf("the %s never started", kind)
			}

			// A delete queued behind it runs while it is still going.
			del, err := f.svc.Submit(ctx, ops.OpDelete, f.st.ID, []string{"sil.txt"}, "")
			require.NoError(t, err)
			got := statusWithin(t, f.svc, del.ID, 3*time.Second)
			assert.Equal(t, ops.StatusOK, got.Status, "the delete waited behind the %s (status %s)", kind, got.Status)
			cur, err := f.svc.Get(ctx, long.ID)
			require.NoError(t, err)
			assert.Equal(t, ops.StatusRunning, cur.Status, "while the %s is still going", kind)

			h.let()
			end := statusWithin(t, f.svc, long.ID, 10*time.Second)
			assert.Equal(t, ops.StatusOK, end.Status, "%s: %s", kind, end.Error)
		})
	}
}

// Two of them never run at once: a second rename of the same folder, or a
// restore and a purge of one entry, would work on the same objects.
func TestFinishingLane_RunsOneAtATime(t *testing.T) {
	h := newHold()
	t.Cleanup(h.let)
	f, _ := newLaneFixture(t, func(d *local.Driver) storage.Driver { return d })
	ht := &heldTrash{h: h}
	f.svc.SetRestorer(ht)
	f.svc.SetPurger(ht)
	ctx := context.Background()
	first, err := f.svc.Submit(ctx, ops.OpRestore, f.st.ID, []string{"1"}, "")
	require.NoError(t, err)
	second, err := f.svc.Submit(ctx, ops.OpPurge, f.st.ID, []string{"1"}, "")
	require.NoError(t, err)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.svc.Run(run)
	defer f.svc.Stop()
	<-h.entered
	time.Sleep(100 * time.Millisecond)
	cur, err := f.svc.Get(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusPending, cur.Status, "the purge started beside the restore")
	h.let()
	assert.Equal(t, ops.StatusOK, statusWithin(t, f.svc, first.ID, 5*time.Second).Status)
	assert.Equal(t, ops.StatusOK, statusWithin(t, f.svc, second.ID, 5*time.Second).Status)
	assert.Equal(t, []int64{1, 1}, ht.seen())
}

// A rename that finishes while the server stops finished: `ok`, not
// `cancelled`.
//
// RED PROOF (PR #61, 2026-09-26): the job's storage work ran to the end on a
// detached context, and the status switch then read the worker's cancelled
// one: the renamed folder's row said `cancelled`.
func TestFinishingLane_ARenameThatEndsWhileTheServerStopsIsOK(t *testing.T) {
	h := newHold()
	t.Cleanup(h.let)
	f, _ := newLaneFixture(t, func(d *local.Driver) storage.Driver {
		return &heldMover{Driver: d, path: "Leon", h: h}
	})
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "a")
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
	require.NoError(t, err)
	run, cancel := context.WithCancel(ctx)
	go f.svc.Run(run)
	defer f.svc.Stop()
	<-h.entered

	cancel() // the server is stopping
	h.let()
	end := statusWithin(t, f.svc, op.ID, 5*time.Second)
	assert.Equal(t, ops.StatusOK, end.Status, "a rename that finished is recorded as %s", end.Status)
	assert.Equal(t, "a", readAll(t, f, "Leo/a.txt"))
}

// A restore of several entries that the server stops part-way is not over:
// the entries it had not reached are carried on at the next start.
//
// RED PROOF (PR #61, 2026-09-26): the job stopped after the entry in hand and
// ended `cancelled` — terminal, so the next start never requeued it — and the
// other entries silently stayed in the trash.
func TestFinishingLane_AStopBetweenEntriesLeavesTheRestForTheNextStart(t *testing.T) {
	h := newHold()
	t.Cleanup(h.let)
	f, sqlDB := newLaneFixture(t, func(d *local.Driver) storage.Driver { return d })
	ht := &heldTrash{h: h}
	f.svc.SetRestorer(ht)
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpRestore, f.st.ID, []string{"1", "2", "3"}, "")
	require.NoError(t, err)
	run, cancel := context.WithCancel(ctx)
	go f.svc.Run(run)
	<-h.entered

	cancel() // the server is stopping while the first entry is under way
	h.let()
	f.svc.Stop()
	mid, err := f.svc.Get(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusRunning, mid.Status, "the job was ended at shutdown with entries left")
	assert.Equal(t, []int64{1}, ht.seen(), "the job went on to the next entry while the server stopped")

	// The next start.
	next := ops.New(sqlDB, func(int64) (storage.Driver, error) { return f.drv, nil })
	next.SetRestorer(ht)
	require.NoError(t, next.Migrate(ctx))
	run2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go next.Run(run2)
	defer next.Stop()
	end := statusWithin(t, next, op.ID, 5*time.Second)
	assert.Equal(t, ops.StatusOK, end.Status, "restore: %s", end.Error)
	assert.Equal(t, 3, end.Done, "the counts start again with the entries")
	assert.Subset(t, ht.seen(), []int64{1, 2, 3}, "an entry was never restored")
}

// Stop does not wait without bound for a folder that takes minutes: past the
// grace it returns, the row stays running (the next start carries it on), and
// the job still writes how it ended if the process lives that long.
//
// RED PROOF (PR #61, 2026-09-26): Stop waited for the whole rename.
func TestFinishingLane_StopDoesNotWaitForARunningRenameForever(t *testing.T) {
	h := newHold()
	t.Cleanup(h.let)
	f, _ := newLaneFixture(t, func(d *local.Driver) storage.Driver {
		return &heldMover{Driver: d, path: "Leon", h: h}
	})
	f.svc.SetFinishGrace(200 * time.Millisecond)
	f.seedDir(t, "Leon")
	ctx := context.Background()
	op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
	require.NoError(t, err)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	go f.svc.Run(run)
	<-h.entered

	stopped := make(chan struct{})
	go func() {
		f.svc.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		h.let()
		<-stopped
		t.Fatal("Stop waited for the running rename")
	}
	cur, err := f.svc.Get(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, ops.StatusRunning, cur.Status, "the row of a rename still under way")

	h.let()
	end := statusWithin(t, f.svc, op.ID, 5*time.Second)
	assert.Equal(t, ops.StatusOK, end.Status, "rename: %s", end.Error)
}

// objectFolders is the local driver moving a folder the way an object store
// does: one object at a time, each landing under the destination whether or
// not some of its siblings already have (the S3 driver's copyDir).
type objectFolders struct{ *local.Driver }

func (d *objectFolders) Move(ctx context.Context, src, dst string) error {
	from := filepath.Join(d.Root(), filepath.FromSlash(strings.Trim(src, "/")))
	fi, err := os.Stat(from)
	if err != nil || !fi.IsDir() {
		return d.Driver.Move(ctx, src, dst)
	}
	var files []string
	if err := filepath.WalkDir(from, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(from, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	}); err != nil {
		return err
	}
	for _, rel := range files {
		if err := d.Driver.Move(ctx, path.Join(src, rel), path.Join(dst, rel)); err != nil {
			return err
		}
	}
	return os.RemoveAll(from)
}

// A rename the previous process was part-way through when it stopped is
// carried on at the next start: the half that had arrived holds the new name,
// and it is the rename's own.
//
// RED PROOF (PR #61, 2026-09-26): the requeued job failed with "something with
// that name already exists here" — the folder stayed in two places, and the
// explorer advised the person to rename part of their own folder.
func TestFinishingLane_ARenameCarriedOnAfterARestartFinishesTheMove(t *testing.T) {
	for _, tc := range []struct {
		name  string
		moved func(t *testing.T, f *opsFixture)
	}{
		{"half of the folder had moved", func(t *testing.T, f *opsFixture) {
			require.NoError(t, f.drv.Move(context.Background(), "Leon/a.txt", "Leo/a.txt"))
		}},
		{"all of it had moved, the catalogue had not", func(t *testing.T, f *opsFixture) {
			require.NoError(t, f.drv.Move(context.Background(), "Leon", "Leo"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, sqlDB := newLaneFixture(t, func(d *local.Driver) storage.Driver { return &objectFolders{Driver: d} })
			f.seedDir(t, "Leon")
			f.seedIn(t, "Leon", "a.txt", "a")
			f.seedIn(t, "Leon", "b.txt", "b")
			ctx := context.Background()
			op, err := f.svc.Submit(ctx, ops.OpRename, f.st.ID, []string{"Leon"}, "Leo")
			require.NoError(t, err)

			// The previous process took the job, got this far and died.
			_, err = sqlDB.ExecContext(ctx, `UPDATE pending_ops SET status='running', started_at=CURRENT_TIMESTAMP WHERE id=?`, op.ID)
			require.NoError(t, err)
			tc.moved(t, f)

			// The next start.
			require.NoError(t, f.svc.Migrate(ctx))
			run, cancel := context.WithCancel(ctx)
			defer cancel()
			go f.svc.Run(run)
			defer f.svc.Stop()
			end := statusWithin(t, f.svc, op.ID, 5*time.Second)
			require.Equal(t, ops.StatusOK, end.Status, "rename: %s", end.Error)
			assert.Equal(t, "a", readAll(t, f, "Leo/a.txt"))
			assert.Equal(t, "b", readAll(t, f, "Leo/b.txt"))
			_, err = f.drv.Stat(ctx, "Leon")
			assert.ErrorIs(t, err, storage.ErrNotFound, "part of the folder stayed at the old name")
			assert.NotNil(t, f.nodeAt(t, "Leo/b.txt"), "the catalogue did not follow the rename")
			assert.Nil(t, f.nodeAt(t, "Leon"), "the catalogue still has the old name")
		})
	}
}

// Only the rename's own half: a rename that had NOT begun is refused a taken
// name as always, whatever else is at the new name.
func TestFinishingLane_ARenameThatHadNotBegunStillRefusesATakenName(t *testing.T) {
	f, _ := newLaneFixture(t, func(d *local.Driver) storage.Driver { return &objectFolders{Driver: d} })
	f.seedDir(t, "Leon")
	f.seedIn(t, "Leon", "a.txt", "mine")
	f.seedDir(t, "Leo")
	f.seedIn(t, "Leo", "a.txt", "somebody else's")
	ctx := context.Background()
	require.NoError(t, f.svc.Migrate(ctx)) // a restart before it was ever taken
	op := f.runOp(t, ops.OpRename, []string{"Leon"}, "Leo")
	assert.Equal(t, ops.StatusFailed, op.Status)
	assert.Contains(t, op.Error, "already exists")
	assert.Equal(t, "somebody else's", readAll(t, f, "Leo/a.txt"))
	assert.Equal(t, "mine", readAll(t, f, "Leon/a.txt"))
}

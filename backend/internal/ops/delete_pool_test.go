package ops_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// A delete job used to trash its items strictly one after another. On an
// object store every trashed file is several round trips (a copy into
// `.filex-trash/`, a delete, the lookups around them), so a job of tens of
// thousands of files ran for hours — and the queue has ONE worker, so every
// other queued op (a paste, the transfer of a staged upload) waited behind it.

// movingDriver is the local driver with every Move counted — trash.Put moves
// each item into the trash — held for a moment so overlapping calls can be
// observed, and refused for one path when asked.
type movingDriver struct {
	*local.Driver
	hold    time.Duration
	failFor string

	mu       sync.Mutex
	inFlight int
	peak     int
	moved    []string
}

func (d *movingDriver) Move(ctx context.Context, src, dst string) error {
	d.mu.Lock()
	d.inFlight++
	if d.inFlight > d.peak {
		d.peak = d.inFlight
	}
	d.moved = append(d.moved, strings.Trim(src, "/"))
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.inFlight--
		d.mu.Unlock()
	}()
	time.Sleep(d.hold)
	if d.failFor != "" && strings.Trim(src, "/") == d.failFor {
		return errors.New("backend refused the move")
	}
	return d.Driver.Move(ctx, src, dst)
}

func (d *movingDriver) stats() (peak int, moved []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.peak, append([]string(nil), d.moved...)
}

// newPoolFixture is newOpsFixture with the driver wrapped and the delete pool
// sized.
func newPoolFixture(t *testing.T, workers int, drv *movingDriver) *opsFixture {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	dir := t.TempDir()
	base := &local.Driver{}
	require.NoError(t, base.Init(ctx, map[string]any{"root": dir}))
	drv.Driver = base

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
	svc.SetDeleteWorkers(workers)
	return &opsFixture{svc: svc, store: store, drv: base, st: st}
}

func (f *opsFixture) trashed(t *testing.T, rel string) bool {
	t.Helper()
	n, err := f.store.GetNodeByPath(context.Background(), f.st.ID, opsPathHash(f.st.ID, rel))
	return err != nil || n == nil // no live row left at the path
}

func TestOpsWorker_Delete_TrashesSeveralAtOnce(t *testing.T) {
	drv := &movingDriver{hold: 80 * time.Millisecond}
	f := newPoolFixture(t, 4, drv)
	var sources []string
	for i := 0; i < 8; i++ {
		rel := fmt.Sprintf("f%d.txt", i)
		f.seedFile(t, rel, "x")
		sources = append(sources, rel)
	}

	op := f.runOp(t, ops.OpDelete, sources, "")
	require.Equal(t, ops.StatusOK, op.Status, "delete op error: %s", op.Error)
	assert.Equal(t, 8, op.Done)
	assert.Equal(t, 0, op.Failed)
	for _, rel := range sources {
		assert.True(t, f.trashed(t, rel), "%s is still live", rel)
	}
	peak, _ := drv.stats()
	assert.GreaterOrEqual(t, peak, 2, "the job still trashes one item at a time")
	assert.LessOrEqual(t, peak, 4, "more at once than the pool allows")
}

func TestOpsWorker_Delete_OneWorkerIsOneAtATime(t *testing.T) {
	drv := &movingDriver{hold: 20 * time.Millisecond}
	f := newPoolFixture(t, 1, drv)
	for i := 0; i < 4; i++ {
		f.seedFile(t, fmt.Sprintf("f%d.txt", i), "x")
	}
	op := f.runOp(t, ops.OpDelete, []string{"f0.txt", "f1.txt", "f2.txt", "f3.txt"}, "")
	require.Equal(t, ops.StatusOK, op.Status, op.Error)
	assert.Equal(t, 4, op.Done)
	peak, _ := drv.stats()
	assert.Equal(t, 1, peak)
}

// The counters are shared by every worker: a failure in one must be counted
// exactly once, the others still finish, and the job reports partial.
func TestOpsWorker_Delete_CountsAFailureAmongParallelItems(t *testing.T) {
	drv := &movingDriver{hold: 20 * time.Millisecond, failFor: "f3.txt"}
	f := newPoolFixture(t, 4, drv)
	var sources []string
	for i := 0; i < 6; i++ {
		rel := fmt.Sprintf("f%d.txt", i)
		f.seedFile(t, rel, "x")
		sources = append(sources, rel)
	}
	op := f.runOp(t, ops.OpDelete, sources, "")
	require.Equal(t, ops.StatusPartial, op.Status)
	assert.Equal(t, 5, op.Done)
	assert.Equal(t, 1, op.Failed)
	assert.NotEmpty(t, op.Error)
	assert.False(t, f.trashed(t, "f3.txt"), "the item that failed must still be live")
	assert.True(t, f.trashed(t, "f5.txt"))
}

// A source inside another source of the same job is dropped: trashing the
// folder takes it along, and trashing it on its own first would split the
// folder across two trash entries — restoring the folder would come back
// without that file.
func TestOpsWorker_Delete_DropsSourcesInsideAnotherSource(t *testing.T) {
	drv := &movingDriver{}
	f := newPoolFixture(t, 4, drv)
	f.seedDir(t, "docs")
	f.seedFile(t, "docs/a.txt", "a")
	f.seedDir(t, "docs/sub")
	f.seedFile(t, "docs/sub/b.txt", "b")
	f.seedFile(t, "other.txt", "o")

	op := f.runOp(t, ops.OpDelete, []string{"docs/a.txt", "docs", "docs/sub/b.txt", "other.txt"}, "")
	require.Equal(t, ops.StatusOK, op.Status, op.Error)
	assert.Equal(t, 4, op.Total)
	assert.Equal(t, 4, op.Done, "an item taken along by its folder is done")

	_, moved := drv.stats()
	assert.ElementsMatch(t, []string{"docs", "other.txt"}, moved)

	// a.txt went into the trash INSIDE the folder, so restoring the folder
	// brings it back.
	matches, err := filepath.Glob(filepath.Join(f.drv.Root(), ".filex-trash", "*__docs", "a.txt"))
	require.NoError(t, err)
	assert.Len(t, matches, 1, "a.txt was trashed on its own, apart from its folder")
	_, err = os.Stat(filepath.Join(f.drv.Root(), "docs"))
	assert.True(t, os.IsNotExist(err))
}

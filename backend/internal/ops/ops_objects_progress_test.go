package ops_test

// A copy, move or delete of ONE folder on one storage says how far it has got,
// in objects.
//
// ⚠ A job's counters are its sources, and a folder is one source: "0 of 1"
// until the whole folder was done, which on an object store (the driver
// copies, and deletes, one object at a time) is minutes of a badge reading
// "0%". The driver now counts on the context (storage.Tally), and the running
// job carries `objects_done` / `objects_total` in Get and List.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// objectStoreLike is the local driver with an object store's habit: a folder
// move or copy is one call that finds three objects and works through them,
// held after the first until the test lets it go.
type objectStoreLike struct {
	*local.Driver
	entered chan struct{}
	release chan struct{}
}

func (d *objectStoreLike) work(ctx context.Context) {
	t := storage.TallyOf(ctx)
	t.Found(3)
	t.Done(1)
	select {
	case d.entered <- struct{}{}:
	default:
	}
	<-d.release
	t.Done(2)
}

func (d *objectStoreLike) Move(ctx context.Context, src, dst string) error {
	d.work(ctx)
	return d.Driver.Move(ctx, src, dst)
}

func (d *objectStoreLike) Copy(ctx context.Context, src, dst string) error {
	d.work(ctx)
	return d.Driver.Copy(ctx, src, dst)
}

func newObjectsFixture(t *testing.T) (*opsFixture, *objectStoreLike) {
	t.Helper()
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	dir := t.TempDir()
	base := &local.Driver{}
	require.NoError(t, base.Init(ctx, map[string]any{"root": dir}))
	drv := &objectStoreLike{Driver: base, entered: make(chan struct{}, 1), release: make(chan struct{})}

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
	return &opsFixture{svc: svc, store: store, drv: base, st: st}, drv
}

func TestSameStorage_AFolderJobReportsItsObjects(t *testing.T) {
	for _, kind := range []string{ops.OpMove, ops.OpCopy, ops.OpDelete} {
		t.Run(kind, func(t *testing.T) {
			f, drv := newObjectsFixture(t)
			f.seedDir(t, "/kaynak")
			f.seedFile(t, "/kaynak/a.txt", "a")
			f.seedDir(t, "/hedef")

			ctx := context.Background()
			dest := "/hedef"
			if kind == ops.OpDelete {
				dest = ""
			}
			op, err := f.svc.Submit(ctx, kind, f.st.ID, []string{"/kaynak"}, dest)
			require.NoError(t, err)
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go f.svc.Run(runCtx)
			defer f.svc.Stop()
			release := sync.OnceFunc(func() { close(drv.release) })
			defer release()

			select {
			case <-drv.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the job never reached the storage")
			}
			mid, err := f.svc.Get(ctx, op.ID)
			require.NoError(t, err)
			require.EqualValues(t, 3, mid.ObjectsTotal, "the running job does not say how many objects it has")
			require.EqualValues(t, 1, mid.ObjectsDone)
			require.Equal(t, 0, mid.Done, "the source count stays 0 of 1, which is why objects are needed")

			listed, err := f.svc.List(ctx, "")
			require.NoError(t, err)
			var row *ops.Op
			for _, o := range listed {
				if o.ID == op.ID {
					row = o
				}
			}
			require.NotNil(t, row)
			require.EqualValues(t, 3, row.ObjectsTotal, "the tray polls List, so List carries the objects too")

			release()
			require.Eventually(t, func() bool {
				cur, err := f.svc.Get(ctx, op.ID)
				return err == nil && cur.Status == ops.StatusOK && cur.ObjectsTotal == 0
			}, 10*time.Second, 15*time.Millisecond, "a finished job no longer carries live counters")
		})
	}
}

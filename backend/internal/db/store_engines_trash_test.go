package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// The trash sweep's two reads, on every engine: a page walked by id cursor
// meets every expired row exactly once, and the per-storage tally counts the
// same rows and only the bytes of files (a folder's size is a cached total of
// what it holds). The tally's SUM is NUMERIC on PostgreSQL and DECIMAL on
// MySQL, which is the part a SQLite-only test would never have exercised.
func TestTrashSweepReadsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			mk := func(name string, typ model.NodeType, size int64, trash bool) int64 {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: name, Path: "/" + name,
					PathHash: pathkey.Hash(st.ID, "/"+name), Type: typ, Size: size,
				})
				require.NoError(t, err, "create %s", name)
				if trash {
					require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
				}
				return n.ID
			}
			want := map[int64]bool{}
			for i := 0; i < 7; i++ {
				want[mk(fmt.Sprintf("gone-%d.bin", i), model.NodeTypeFile, 10, true)] = true
			}
			want[mk("gone-folder", model.NodeTypeDirectory, 70, true)] = true
			mk("alive.bin", model.NodeTypeFile, 1000, false)

			before := time.Now().Add(24 * time.Hour)
			seen := map[int64]bool{}
			var after int64
			for pages := 0; ; pages++ {
				require.Less(t, pages, 10, "the cursor is not advancing")
				page, err := store.ListTrashedExpired(ctx, before, nil, after, 3)
				require.NoError(t, err)
				for _, n := range page {
					require.Greater(t, n.ID, after, "rows come in id order, after the cursor")
					require.False(t, seen[n.ID], "row %d returned twice", n.ID)
					seen[n.ID] = true
					after = n.ID
				}
				if len(page) < 3 {
					break
				}
			}
			require.Equal(t, want, seen, "every trashed row, once; no live row")

			// The same walk narrowed in the SQL: the cursor and the storage
			// clause together, and an empty narrowing reads nothing.
			other := createNamedEngineStorage(t, store, "other")
			for i := 0; i < 4; i++ {
				name := fmt.Sprintf("elsewhere-%d.bin", i)
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: other.ID, Name: name, Path: "/" + name,
					PathHash: pathkey.Hash(other.ID, "/"+name), Type: model.NodeTypeFile, Size: 1,
				})
				require.NoError(t, err)
				require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
			}
			narrowed := map[int64]bool{}
			after = 0
			for pages := 0; ; pages++ {
				require.Less(t, pages, 10, "the narrowed cursor is not advancing")
				page, err := store.ListTrashedExpired(ctx, before, []int64{st.ID}, after, 3)
				require.NoError(t, err)
				for _, n := range page {
					require.Equal(t, st.ID, n.StorageID, "the narrowing is in the SQL")
					narrowed[n.ID] = true
					after = n.ID
				}
				if len(page) < 3 {
					break
				}
			}
			require.Equal(t, want, narrowed)
			nothing, err := store.ListTrashedExpired(ctx, before, []int64{}, 0, 3)
			require.NoError(t, err)
			require.Empty(t, nothing, "a narrowing to no storage reads nothing, never everything")

			tally, err := store.CountTrashedExpired(ctx, before)
			require.NoError(t, err)
			require.Equal(t, map[int64]db.TrashTally{st.ID: {Count: 8, Bytes: 70}, other.ID: {Count: 4, Bytes: 4}}, tally,
				"eight rows; seventy bytes of files — the folder's cached size is not added again")

			none, err := store.CountTrashedExpired(ctx, time.Now().Add(-24*time.Hour))
			require.NoError(t, err)
			require.Empty(t, none, "nothing was deleted before yesterday")
		})
	}
}

func createNamedEngineStorage(t *testing.T, store db.Store, name string) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/data/" + name, Enabled: true,
		ConfigJSON: []byte(`{"root":"/data/` + name + `"}`), SyncMode: model.SyncModePoll, SyncIntervalS: 900,
	})
	require.NoError(t, err)
	return st
}

// "Empty the trash" as an ops job, on every engine: the queue's own SQL (the
// insert, the tenant's list and its latest run) and — the part a SQLite-only
// test cannot vouch for — the cutoff. The run purges what was deleted before
// its row's created_at, a value read back from the database through the same
// driver that stamped every deleted_at: on PostgreSQL a timestamptz, on MySQL
// a DATETIME in the session's zone, on SQLite a TEXT compared as TEXT. A
// mismatch there purges nothing (or, worse, too much) with every test green.
func TestTrashEmptyOpOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)
			mk := func(name string) int64 {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: name, Path: "/" + name,
					PathHash: pathkey.Hash(st.ID, "/"+name), Type: model.NodeTypeFile, Size: 4,
				})
				require.NoError(t, err)
				require.NoError(t, store.SoftDeleteNode(ctx, n.ID))
				return n.ID
			}
			for i := 0; i < 3; i++ {
				mk(fmt.Sprintf("gone-%d.txt", i))
			}

			purger := trash.New(store, nil, nil)
			queue := ops.NewForDialect(sqlDB, e.driver, nil)
			require.NoError(t, queue.Migrate(ctx))
			queue.SetTrashEmptier(purger)
			t.Cleanup(queue.Stop)

			op, err := queue.SubmitTrashEmpty(ctx, ops.TrashEmptyRequest{Reach: []int64{st.ID}, Tenant: "tenant:5"})
			require.NoError(t, err)
			require.Equal(t, 3, op.Total, "counted against its own created_at")
			deadline := time.Now().Add(20 * time.Second)
			for op.Status == ops.StatusPending || op.Status == ops.StatusRunning {
				require.True(t, time.Now().Before(deadline), "the run never finished")
				time.Sleep(20 * time.Millisecond)
				op, err = queue.Get(ctx, op.ID)
				require.NoError(t, err)
			}
			require.Equal(t, ops.StatusOK, op.Status, op.Error)
			require.Equal(t, 3, op.Done, "everything deleted before the ask is purged")

			mine, err := queue.ListFor(ctx, "", ops.Viewer{StorageIDs: []int64{st.ID + 1000}, Tenant: "tenant:5"})
			require.NoError(t, err)
			require.Len(t, mine, 1, "its tenant lists it")
			theirs, err := queue.ListFor(ctx, "", ops.Viewer{StorageIDs: []int64{st.ID + 1000}, Tenant: "tenant:6"})
			require.NoError(t, err)
			require.Empty(t, theirs, "another tenant does not")
			latest, err := queue.LatestTrashEmpty(ctx, "tenant:5")
			require.NoError(t, err)
			require.NotNil(t, latest)
			require.Equal(t, op.ID, latest.ID)

			// Deleted after the ask: outside the run's cutoff on this engine.
			time.Sleep(1100 * time.Millisecond)
			mk("late.txt")
			n, _, err := purger.Tally(ctx, trash.EmptyJob{Before: trash.EmptyCutoff(op.CreatedAt, 0), Reach: []int64{st.ID}})
			require.NoError(t, err)
			require.Zero(t, n, "a file deleted after the ask is not the run's")
			n, _, err = purger.Tally(ctx, trash.EmptyJob{Before: time.Now().Add(time.Minute), Reach: []int64{st.ID}})
			require.NoError(t, err)
			require.Equal(t, 1, n, "but it is in the trash")
		})
	}
}

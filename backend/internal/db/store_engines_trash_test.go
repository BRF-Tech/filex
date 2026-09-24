package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
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
				page, err := store.ListTrashedExpired(ctx, before, after, 3)
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

			tally, err := store.CountTrashedExpired(ctx, before)
			require.NoError(t, err)
			require.Equal(t, map[int64]db.TrashTally{st.ID: {Count: 8, Bytes: 70}}, tally,
				"eight rows; seventy bytes of files — the folder's cached size is not added again")

			none, err := store.CountTrashedExpired(ctx, time.Now().Add(-24*time.Hour))
			require.NoError(t, err)
			require.Empty(t, none, "nothing was deleted before yesterday")
		})
	}
}

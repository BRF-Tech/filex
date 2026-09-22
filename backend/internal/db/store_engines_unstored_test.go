package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// ListUnstoredNodes feeds the staged-upload boot pass: every live row whose
// bytes were never confirmed on the storage, oldest id first, in pages.
func TestListUnstoredNodesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			mk := func(p, state string) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: p[1:], Path: p, PathHash: pathkey.Hash(st.ID, p),
					StorageKey: p, Type: model.NodeTypeFile, Size: 1, TransferState: state,
				})
				require.NoError(t, err)
				return n
			}
			mk("/stored.bin", model.TransferStateStored)
			staged := mk("/staged.bin", model.TransferStateStaged)
			failed := mk("/failed.bin", model.TransferStateFailed)
			gone := mk("/trashed.bin", model.TransferStateStaged)
			require.NoError(t, store.SoftDeleteNode(ctx, gone.ID))
			later := mk("/later.bin", model.TransferStateStaged)

			all, err := store.ListUnstoredNodes(ctx, 0, 100)
			require.NoError(t, err)
			ids := []int64{}
			for _, n := range all {
				ids = append(ids, n.ID)
			}
			require.Equal(t, []int64{staged.ID, failed.ID, later.ID}, ids,
				"live staged and failed rows only, oldest first — never a stored or a trashed one")

			page, err := store.ListUnstoredNodes(ctx, staged.ID, 1)
			require.NoError(t, err)
			require.Len(t, page, 1)
			require.Equal(t, failed.ID, page[0].ID, "afterID and limit page through them")
		})
	}
}

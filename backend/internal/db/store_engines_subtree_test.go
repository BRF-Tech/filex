package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// A folder rescan decides what to move to the trash with ListStaleNodesUnder
// and whether it may with CountLiveNodesUnder. Both must be bounded by the
// folder's exact prefix on every engine — strictly below it, never the folder
// itself, never a sibling that only starts with its name, never a trashed row,
// and a non-ASCII name as reliably as an ASCII one.
func TestSubtreeStaleQueriesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			seedTreeRow(t, store, st.ID, "/Müşteri", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/Müşteri/sözleşme.pdf", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, "/Müşteri/2026", model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, "/Müşteri/2026/fatura.pdf", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, "Müşteri/çıplak.txt", model.NodeTypeFile) // the bare spelling
			gone := seedTreeRow(t, store, st.ID, "/Müşteri/silinmiş.txt", model.NodeTypeFile)
			require.NoError(t, store.SoftDeleteNode(ctx, gone.ID))
			seedTreeRow(t, store, st.ID, "/Müşteri2/ek.pdf", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, "/Müşteri_x/ek.pdf", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, "/diğer.txt", model.NodeTypeFile)

			n, err := store.CountLiveNodesUnder(ctx, st.ID, "Müşteri")
			require.NoError(t, err)
			require.EqualValues(t, 4, n, "strictly below the folder, live only, both spellings")

			stale, err := store.ListStaleNodesUnder(ctx, st.ID, "/Müşteri/", time.Now().Add(time.Hour))
			require.NoError(t, err)
			require.Equal(t, []string{"/Müşteri/2026", "/Müşteri/2026/fatura.pdf", "/Müşteri/sözleşme.pdf", "Müşteri/çıplak.txt"}, pathsOf(stale))

			fresh, err := store.ListStaleNodesUnder(ctx, st.ID, "/Müşteri", time.Now().Add(-time.Hour))
			require.NoError(t, err)
			require.Empty(t, fresh, "rows seen after the cut-off are not stale")

			n, err = store.CountLiveNodesUnder(ctx, st.ID, "/")
			require.NoError(t, err)
			require.Zero(t, n, "the storage root is never a subtree")
		})
	}
}

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The storage list's badge reads the LAST run, and a page following a scan to
// its end reads it too. Two runs that start in the same second (an edit's
// aborted scan and the one that replaces it, a "Sync now" straight after a
// restart) tie on started_at, which SQLite stores to the second. Nothing in the
// query said which of the two is last; the index order happened to answer it.
// The id now does, as it already did for GetLastSyncRunByStatus.
func TestLastSyncRunBreaksASameSecondTieOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			first, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, first.ID, "", 0, 0, 0, 0, "aborted", "interrupted"))
			second, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)

			last, err := store.GetLastSyncRun(ctx, st.ID)
			require.NoError(t, err)
			require.Equal(t, second.ID, last.ID, "the older of two same-second runs came back as the last")
		})
	}
}

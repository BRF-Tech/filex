package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A run the process never finished stays `running` for ever unless something
// closes it; the worker does at start, through AbortUnfinishedSyncRuns. The
// tombstone guard's baseline is the last run that finished ok, through
// GetLastSyncRunByStatus. Both are plain SQL that has to mean the same thing
// on every engine.
func TestUnfinishedSyncRunsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			ok, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, ok.ID, "", 7, 7, 0, 0, "ok", ""))
			failed, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, failed.ID, "", 2, 0, 0, 0, "failed", "503 Slow Down"))
			open, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)

			const why = "interrupted: the server stopped during the scan"
			n, err := store.AbortUnfinishedSyncRuns(ctx, why)
			require.NoError(t, err)
			require.EqualValues(t, 1, n, "only the run with no finished_at is closed")

			got, err := store.GetSyncRun(ctx, open.ID)
			require.NoError(t, err)
			require.Equal(t, "aborted", got.Status)
			require.NotNil(t, got.FinishedAt)
			require.Equal(t, why, got.Error)

			kept, err := store.GetSyncRun(ctx, failed.ID)
			require.NoError(t, err)
			require.Equal(t, "failed", kept.Status, "a finished run is history")
			require.Equal(t, "503 Slow Down", kept.Error)

			n, err = store.AbortUnfinishedSyncRuns(ctx, why)
			require.NoError(t, err)
			require.EqualValues(t, 0, n, "closing is idempotent")

			last, err := store.GetLastSyncRunByStatus(ctx, st.ID, "ok")
			require.NoError(t, err)
			require.Equal(t, ok.ID, last.ID, "the latest ok run, past a later failed and a later aborted one")
			require.Equal(t, 7, last.SeenCount)

			_, err = store.GetLastSyncRunByStatus(ctx, st.ID, "partial")
			require.Error(t, err, "no run with that status is an error, like GetLastSyncRun")
		})
	}
}

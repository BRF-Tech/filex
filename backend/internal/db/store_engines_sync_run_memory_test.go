package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task #353: a full scan records the most memory the process held while it
// walked (migration 00139), and the storage form's interval advice reads the
// last scan that finished "ok" with that figure - on every engine. A failed or
// aborted scan after it is not the measurement: it did not walk the whole
// storage.
func TestSyncRunPeakMemoryOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			_, err := store.LastMeasuredSyncRun(ctx, st.ID)
			require.True(t, errors.Is(err, sql.ErrNoRows), "no finished scan yet: %v", err)

			ok, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, ok.ID, "", 1234, 1234, 0, 0, "ok", ""))
			require.NoError(t, store.SetSyncRunPeakMemory(ctx, ok.ID, 512<<20))

			failed, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, failed.ID, "", 10, 0, 0, 0, "failed", "disk gone"))
			require.NoError(t, store.SetSyncRunPeakMemory(ctx, failed.ID, 900<<20))

			last, err := store.LastMeasuredSyncRun(ctx, st.ID)
			require.NoError(t, err)
			assert.Equal(t, ok.ID, last.ID, "the last scan that finished ok, not the failed one after it")
			assert.Equal(t, 1234, last.SeenCount)
			assert.Equal(t, int64(512<<20), last.PeakMemBytes)
			require.NotNil(t, last.FinishedAt)
			assert.False(t, last.FinishedAt.Before(last.StartedAt))

			running, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			peaks, err := store.SyncRunPeaks(ctx, []int64{ok.ID, failed.ID, running.ID})
			require.NoError(t, err)
			assert.Equal(t, map[int64]int64{ok.ID: 512 << 20, failed.ID: 900 << 20}, peaks,
				"a run without a measurement is absent")

			// The common run reads leave the column alone (see
			// SyncRunMemorySQL): a run read the ordinary way carries no peak.
			plain, err := store.GetSyncRun(ctx, ok.ID)
			require.NoError(t, err)
			assert.Zero(t, plain.PeakMemBytes)
		})
	}
}

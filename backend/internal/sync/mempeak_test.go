package sync

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// swapMemInUse makes memPeak read next() instead of the runtime, sampling at
// test speed.
func swapMemInUse(t *testing.T, next func() uint64) {
	t.Helper()
	was, every := memInUse, memPeakEvery
	memInUse, memPeakEvery = next, time.Millisecond
	t.Cleanup(func() { memInUse, memPeakEvery = was, every })
}

// The highest sample wins, the last one (at the end) included, and asking
// twice gives the same figure.
func TestMemPeak_KeepsTheHighestSample(t *testing.T) {
	var n atomic.Uint64
	swapMemInUse(t, func() uint64 {
		v := n.Add(1)
		if v == 3 {
			return 900
		}
		return 100
	})
	m := startMemPeak()
	require.Eventually(t, func() bool { return n.Load() >= 4 }, time.Second, time.Millisecond)
	assert.Equal(t, uint64(900), m.end())
	assert.Equal(t, uint64(900), m.end(), "end is idempotent")
}

// Task #353: a full scan records the most memory the process held while it
// walked, on its own run, where the storage form's interval advice reads it.
//
// Break: drop the SetSyncRunPeakMemory call from finishRun - the run's peak
// stays 0 and the form never warns about memory.
func TestRunOnce_RecordsThePeakMemoryOnTheRun(t *testing.T) {
	swapMemInUse(t, func() uint64 { return 3 << 30 })
	_, store := dbtest.NewTestDB(t)
	root := t.TempDir()
	cfg := map[string]any{"root": root}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "olcum", Driver: "local", MountPath: "/olcum", ConfigJSON: raw,
		SyncMode: model.SyncModeOnDemand, Enabled: true,
	})
	require.NoError(t, err)
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), cfg))
	s := &storageSyncer{store: store, storage: st, driver: drv, rule: ruleFor(st, cfg), ctx: context.Background(), fallback: time.Minute}

	require.NoError(t, s.RunOnce(context.Background()))

	last, err := store.LastMeasuredSyncRun(context.Background(), st.ID)
	require.NoError(t, err)
	assert.Equal(t, "ok", last.Status)
	assert.Equal(t, int64(3<<30), last.PeakMemBytes)
}

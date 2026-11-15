package server

// Task #351: the storage every store package (CasaOS, Umbrel, Unraid,
// TrueNAS) seeds from the environment is a local folder people also write to
// from outside filex. It was seeded `poll` every 15 minutes, so a file copied
// into it stayed invisible until the next scan. It is seeded with the driver's
// default now - live watching (`lazy`) for a local storage - and any other
// driver still polls.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Break: seed model.SyncModePoll again - the local row comes back `poll`.
func TestSeedDefaultStorage_ALocalStorageIsSeededWithLiveWatching(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Seed.Storage = config.SeedStorage{Driver: "local", Path: t.TempDir()}

	seedDefaultStorage(ctx, store, cfg)

	rows, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, model.SyncModeLazy, rows[0].SyncMode)
	assert.Equal(t, model.DefaultSyncIntervalS, rows[0].SyncIntervalS, "the interval is the safety net under the watches")
	assert.Empty(t, model.SyncModeSuggestion(rows[0]), "a storage already on live watching is offered nothing")
}

func TestSeedDefaultStorage_AnObjectStoreStillPolls(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Seed.Storage = config.SeedStorage{Driver: "s3", Bucket: "b", Prefix: "p", Endpoint: "https://s3.example.com"}

	seedDefaultStorage(ctx, store, cfg)

	rows, err := store.ListStorages(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, model.SyncModePoll, rows[0].SyncMode)
}

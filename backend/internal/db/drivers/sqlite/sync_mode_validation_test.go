package sqlite_test

// sync_mode had NO server-side validation: any string the admin API decoded
// was persisted, and the sync worker's switch has no branch for it, so the
// storage silently ran the poll loop. The operator reads back their own
// configuration — `fsnotifiy`, `push` — and believes it is in effect.
//
// The gate lives in the store, not in one handler, because three writers
// reach this column: the admin API, the config seed, and the CLI.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func storageFixture(mode model.SyncMode) *model.Storage {
	cfg, _ := json.Marshal(map[string]any{"root": "/tmp"})
	return &model.Storage{
		Name:          "s",
		Driver:        "local",
		MountPath:     "/",
		ConfigJSON:    cfg,
		SyncMode:      mode,
		SyncIntervalS: 900,
		Enabled:       true,
	}
}

func TestCreateStorage_RejectsTypoSyncMode(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	_, err := store.CreateStorage(ctx, storageFixture("fsnotifiy"))
	require.Error(t, err, "a typo must not store and silently poll")
	assert.Contains(t, err.Error(), "invalid sync_mode")
	assert.Contains(t, err.Error(), "poll, fsnotify, ondemand", "the message must name the modes that exist")

	list, err := store.ListStorages(ctx)
	require.NoError(t, err)
	assert.Empty(t, list, "nothing may be persisted by a rejected write")
}

func TestCreateStorage_RejectsPushAsUnimplemented(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	_, err := store.CreateStorage(ctx, storageFixture(model.SyncModePush))
	require.Error(t, err, `"push" is a declared enum member with no branch behind it`)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Contains(t, err.Error(), "ondemand", "the message must name what to use instead")
}

func TestCreateStorage_AcceptsImplementedModes(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	for _, m := range []model.SyncMode{"", model.SyncModePoll, model.SyncModeFSNotify, model.SyncModeOnDemand} {
		st := storageFixture(m)
		st.Name = "s-" + string(m)
		created, err := store.CreateStorage(ctx, st)
		require.NoError(t, err, "mode %q must be accepted", m)
		assert.Equal(t, m, created.SyncMode)
	}
}

// A row that already carries `push` (written before the gate existed) stays
// editable: refusing an unrelated edit would strand the operator with a row
// they cannot rename or disable. Only a CHANGE to an unsupported mode fails.
func TestUpdateStorage_LegacyUnsupportedModeStaysEditable(t *testing.T) {
	db, store := testutil.NewTestDB(t)
	ctx := context.Background()

	created, err := store.CreateStorage(ctx, storageFixture(model.SyncModePoll))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE storages SET sync_mode='push' WHERE id=?`, created.ID)
	require.NoError(t, err)

	legacy, err := store.GetStorage(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, model.SyncModePush, legacy.SyncMode)

	legacy.Name = "renamed"
	require.NoError(t, store.UpdateStorage(ctx, legacy), "an unrelated edit to a legacy row must still save")

	after, err := store.GetStorage(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "renamed", after.Name)
	assert.Equal(t, model.SyncModePush, after.SyncMode, "the legacy value is preserved, not rewritten behind the operator's back")

	// …but moving a healthy row TO an unsupported mode is refused.
	healthy, err := store.CreateStorage(ctx, func() *model.Storage { s := storageFixture(model.SyncModePoll); s.Name = "other"; return s }())
	require.NoError(t, err)
	healthy.SyncMode = model.SyncModePush
	require.Error(t, store.UpdateStorage(ctx, healthy))
}

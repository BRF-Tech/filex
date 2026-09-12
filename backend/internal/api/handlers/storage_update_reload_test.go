package handlers_test

// Editing a storage must take effect on the RUNNING process.
//
// Creating a storage starts a syncer for it; deleting one stops it. Editing
// one did neither: it wrote the database row and left every live consumer
// holding the copy it had taken at boot — the syncer's own snapshot (name,
// config, root path, schedule, enabled flag, and its separately initialised
// driver) and the server's process-lifetime driver cache.
//
// So an operator who fixed a wrong bucket in the admin page watched the same
// failure continue, and an operator who renamed a storage kept reading the old
// name in the log (issue #21: "I renamed it to ps-hot, but it still shows
// previous one — Garage S3"). Both were told the save succeeded, because it
// had. Only a restart applied it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// dirWith returns a fresh directory holding one named file.
func dirWith(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	return dir
}

func TestStorageUpdate_AppliesWithoutRestart(t *testing.T) {
	ctx := context.Background()
	before, after := dirWith(t, "before.txt"), dirWith(t, "after.txt")

	var worker *filexsync.Worker
	var forgotten []int64
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		worker = d.Worker
		d.ForgetStorage = func(id int64) { forgotten = append(forgotten, id) }
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	cfg, _ := json.Marshal(map[string]any{"path": before})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "Garage S3", Driver: "local", MountPath: "/",
		ConfigJSON: cfg, SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, worker.AddStorage(ctx, st))
	require.NoError(t, worker.Trigger(ctx, st.ID))

	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/before.txt"))
	require.NoError(t, err)
	require.NotNil(t, n, "the first walk must have catalogued the original directory")

	// Rename it and point it somewhere else, exactly as the admin page does.
	put := func(t *testing.T, body map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/api/admin/storages/%d", srv.URL, st.ID), strings.NewReader(string(raw)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	put(t, map[string]any{
		"name": "ps-hot", "driver": "local", "mount_path": "/",
		"config":  map[string]any{"path": after},
		"enabled": true, "sync_mode": string(model.SyncModeOnDemand), "sync_interval_s": 900,
	})

	t.Run("the syncer walks the new configuration", func(t *testing.T) {
		require.NoError(t, worker.Trigger(ctx, st.ID))
		got, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/after.txt"))
		require.NoError(t, err)
		require.NotNil(t, got,
			"the sync is still walking the directory the storage was created with")
	})

	t.Run("the cached driver is dropped", func(t *testing.T) {
		require.Contains(t, forgotten, st.ID,
			"the process keeps serving reads through the driver built from the old config")
	})

	t.Run("disabling it stops the syncer", func(t *testing.T) {
		put(t, map[string]any{
			"name": "ps-hot", "driver": "local", "mount_path": "/",
			"config":  map[string]any{"path": after},
			"enabled": false, "sync_mode": string(model.SyncModeOnDemand), "sync_interval_s": 900,
		})
		require.False(t, worker.Known(st.ID),
			"a disabled storage is still being synced on its schedule")
	})

	t.Run("re-enabling it starts one again", func(t *testing.T) {
		put(t, map[string]any{
			"name": "ps-hot", "driver": "local", "mount_path": "/",
			"config":  map[string]any{"path": after},
			"enabled": true, "sync_mode": string(model.SyncModeOnDemand), "sync_interval_s": 900,
		})
		require.True(t, worker.Known(st.ID),
			"turning a storage back on left it without a syncer until the next restart")
	})
}

// Deleting a storage has to drop the cached driver too — otherwise the
// process holds its connection (and its credentials) for its whole life.
func TestStorageDelete_DropsCachedDriver(t *testing.T) {
	ctx := context.Background()
	var forgotten []int64
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.ForgetStorage = func(id int64) { forgotten = append(forgotten, id) }
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	cfg, _ := json.Marshal(map[string]any{"path": dirWith(t, "a.txt")})
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "gone", Driver: "local", MountPath: "/",
		ConfigJSON: cfg, SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/admin/storages/%d", srv.URL, st.ID), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.Contains(t, forgotten, st.ID)
}

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The dashboard's per-storage state is read from the last sync run's status.
// It compared that status with "error", a value the sync worker has never
// written — a failed run is recorded as "failed" — so a storage whose last
// scan failed showed as "ok" on the admin's first page. A run the server
// stopped in the middle of ("aborted") leaves the catalogue behind the
// backend: that is "stale", not "ok".
func TestDashboard_StorageStateReadsTheStatusTheSyncWrites(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	ctx := context.Background()

	withLastRun := func(name, status string) int64 {
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true,
			ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(t.TempDir()) + `"}`),
			SyncMode:   model.SyncModeOnDemand,
		})
		require.NoError(t, err)
		run, err := store.CreateSyncRun(ctx, st.ID, "")
		require.NoError(t, err)
		require.NoError(t, store.FinishSyncRun(ctx, run.ID, "", 1, 0, 0, 0, status, ""))
		return st.ID
	}
	failed := withLastRun("bozuk", "failed")
	aborted := withLastRun("yarim", "aborted")
	healthy := withLastRun("saglam", "ok")

	code, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/dashboard", nil)
	require.Equal(t, http.StatusOK, code)
	state := map[int64]string{}
	rows, _ := body["storages"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		id, _ := row["id"].(float64)
		state[int64(id)], _ = row["state"].(string)
	}
	assert.Equal(t, "error", state[failed], "a failed last run is an error")
	assert.Equal(t, "stale", state[aborted], "an interrupted last run leaves the catalogue behind")
	assert.Equal(t, "ok", state[healthy])
}

package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestGrants_UnqualifiedPathIsRejected — POST /api/files/permissions with a
// bare "/deneme" silently fell back to storages[0] and wrote the grant
// there. olivov watched a grant meant for their own storage land on a
// different tenant's (H6, 2026-08-05); a `storage`/`storage_id` field in the
// body changed nothing, because there is no such field.
//
// A grant is durable authorization state, so guessing the storage is the
// wrong move: say which one.
func TestGrants_UnqualifiedPathIsRejected(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	first := createRBACStorage(t, srv.URL, client, "BirinciDepo")
	second := createRBACStorage(t, srv.URL, client, "IkinciDepo")
	require.NotEqual(t, first, second)

	target := createUser(t, srv.URL, client, "hedef@test.local", "HedefPass!1", "user")

	t.Run("unqualified is a 400", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/files/permissions",
			map[string]any{"path": "/deneme", "user_id": target, "level": "viewer", "is_dir": true})
		require.Equal(t, http.StatusBadRequest, status,
			"an unqualified path must be refused, not resolved to the first storage (%v)", body)
	})

	t.Run("qualified still works", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodPost, srv.URL+"/api/files/permissions",
			map[string]any{"path": "IkinciDepo://deneme", "user_id": target, "level": "viewer", "is_dir": true})
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.Equal(t, float64(second), body["storage_id"],
			"the grant must land on the storage the path named")
	})
}

// createRBACStorage makes an RBAC-enabled local storage and returns its id.
func createRBACStorage(t *testing.T, baseURL string, client *http.Client, name string) int64 {
	t.Helper()
	status, raw := doReq(t, client, http.MethodPost, baseURL+"/api/admin/storages", model.Storage{
		Name:        name,
		Driver:      "local",
		MountPath:   "/" + name,
		ConfigJSON:  json.RawMessage(`{"path":"` + t.TempDir() + `"}`),
		SyncMode:    model.SyncModeOnDemand,
		Enabled:     true,
		RBACEnabled: true,
	})
	require.Equal(t, http.StatusOK, status, "create storage %s: %s", name, raw)
	var st struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &st))
	require.NotZero(t, st.ID)
	return st.ID
}

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestAdminUsers_ExposeUsageAndQuota — "how much room is this user taking?"
// had no answer on the user object: GET /api/admin/users/{id} returned
// created_at, display_name, email, id, last_login_at, locale, provider_id,
// role, timezone, totp_enabled, updated_at and nothing else, even though
// users.usage_bytes and users.quota_bytes have existed since migration
// 00003. Only the caller's own /quota/me could see them.
//
// olivov needs both in one call to fill a table (G3 + G2, 2026-08-04).
func TestAdminUsers_ExposeUsageAndQuota(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	ctx := context.Background()

	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	require.NoError(t, store.SetUserQuota(ctx, admin.ID, 5*1024*1024*1024))
	require.NoError(t, store.IncrementUserUsage(ctx, admin.ID, 1234))

	t.Run("single user", func(t *testing.T) {
		status, body := doJSON(t, client, http.MethodGet,
			fmt.Sprintf("%s/api/admin/users/%d", srv.URL, admin.ID), nil)
		require.Equal(t, http.StatusOK, status, "%v", body)
		require.Equal(t, float64(1234), body["used_bytes"])
		require.Equal(t, float64(5*1024*1024*1024), body["quota_bytes"])
	})

	t.Run("list", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/admin/users", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var users []map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&users))
		require.NotEmpty(t, users)

		var found bool
		for _, u := range users {
			if int64(u["id"].(float64)) != admin.ID {
				continue
			}
			found = true
			require.Equal(t, float64(1234), u["used_bytes"],
				"the list must carry usage too, so a table is one call")
			require.Equal(t, float64(5*1024*1024*1024), u["quota_bytes"])
		}
		require.True(t, found, "seeded admin missing from the list")
	})
}

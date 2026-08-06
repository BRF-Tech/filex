package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// seedUserIn creates a plain user homed in providerID and returns its id.
func seedUserIn(t *testing.T, store db.Store, providerID int64, email string) int64 {
	t.Helper()
	ctx := context.Background()
	hash, err := local.HashPassword("VictimPass!1")
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, providerID, ""))
	return u.ID
}

// TestUsersAdmin_TenantGate — /api/admin/users confined LIST only.
//
// tenantstore wraps exactly three methods (ListStorages, ListEnabledStorages,
// ListUsers); GetUser and every mutation take a raw id. So a tenant admin
// could read, rename, re-password, DISABLE and DELETE another tenant's users
// by id — including that tenant's last admin, which locks the whole tenant
// out. Same class as the /dav leak (H4) and left explicitly out of scope by
// the olivov PR; this is the follow-up.
//
// Out-of-tenant must answer 404, not 403: a foreign id has to be
// indistinguishable from one that does not exist.
func TestUsersAdmin_TenantGate(t *testing.T) {
	srv, client, store := multiTenantServer(t)

	// Two tenants, each with its own admin, plus a victim user in tenant B.
	_, meEmail, mePass := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	otherID, _, _ := seedTenant(t, store, "arasboya", "admin@arasboya.test", false)
	victim := seedUserIn(t, store, otherID, "victim@arasboya.test")

	testutil.LoginAs(t, srv, client, meEmail, mePass)

	url := func(id int64) string {
		return srv.URL + "/api/admin/users/" + itoa(id)
	}

	t.Run("cannot read another tenant's user", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodGet, url(victim), nil)
		require.Equal(t, http.StatusNotFound, status,
			"a foreign user id must 404, not disclose the account")
	})

	t.Run("cannot disable another tenant's user", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodPatch, url(victim), map[string]any{
			"enabled": false,
		})
		require.Equal(t, http.StatusNotFound, status)

		// Status code alone is not proof — measure the row.
		u, err := store.GetUser(context.Background(), victim)
		require.NoError(t, err)
		require.True(t, u.Enabled,
			"the refused PATCH must not have disabled the account anyway")
	})

	t.Run("cannot re-password another tenant's user", func(t *testing.T) {
		before, err := store.GetUser(context.Background(), victim)
		require.NoError(t, err)

		status, _ := doJSON(t, client, http.MethodPatch, url(victim), map[string]any{
			"password": "Hijacked!1",
		})
		require.Equal(t, http.StatusNotFound, status)

		after, err := store.GetUser(context.Background(), victim)
		require.NoError(t, err)
		require.Equal(t, before.PasswordHash, after.PasswordHash,
			"account takeover: the password hash changed on a refused request")
	})

	t.Run("cannot delete another tenant's user", func(t *testing.T) {
		status, _ := doJSON(t, client, http.MethodDelete, url(victim), nil)
		require.Equal(t, http.StatusNotFound, status)

		u, err := store.GetUser(context.Background(), victim)
		require.NoError(t, err)
		require.NotNil(t, u, "the user must still exist after a refused DELETE")
	})

	t.Run("own tenant's user still reachable", func(t *testing.T) {
		mine := seedUserIn(t, store, providerOf(t, store, mustUserID(t, store, meEmail)), "mine@diyetlif.test")

		status, _ := doJSON(t, client, http.MethodGet, url(mine), nil)
		require.Equal(t, http.StatusOK, status, "the gate must not break same-tenant admin")

		status, _ = doJSON(t, client, http.MethodPatch, url(mine), map[string]any{
			"display_name": "Yeniden Adlandırıldı",
		})
		require.Equal(t, http.StatusOK, status)
	})
}

// TestUsersAdmin_SupertenantUnaffected — "the operator sees everything" must
// survive the gate, or the platform admin loses the repair path for accounts
// stranded in the wrong tenant.
func TestUsersAdmin_SupertenantUnaffected(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, password := testutil.SeedAdmin(t, store) // provider 1 = supertenant
	testutil.LoginAs(t, srv, client, email, password)

	tenantID, _, _ := seedTenant(t, store, "diyetlif", "admin@diyetlif.test", false)
	target := seedUserIn(t, store, tenantID, "user@diyetlif.test")

	status, body := doJSON(t, client, http.MethodGet,
		srv.URL+"/api/admin/users/"+itoa(target), nil)
	require.Equal(t, http.StatusOK, status, "%v", body)

	status, _ = doJSON(t, client, http.MethodPatch,
		srv.URL+"/api/admin/users/"+itoa(target), map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status,
		"the supertenant must still be able to disable any account")
}

func mustUserID(t *testing.T, store db.Store, email string) int64 {
	t.Helper()
	u, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, u)
	return u.ID
}

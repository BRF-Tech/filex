package handlers_test

// The built-in User and Viewer roles, under multi-tenancy.
//
// A custom role belongs to a tenant (permission_rules.provider_id) and a
// tenant admin only ever sees and edits its own. The built-in User and Viewer
// roles do not: each is ONE instance-wide row (perm.SaveRoleBase →
// settings), and it is what every account of every tenant without a custom
// role holds. PUT /api/admin/roles/builtin sat behind auth.RequireAdmin
// alone, which in multi-tenant mode admits the admin of any tenant — so the
// admin of one customer could take downloads away from every other customer,
// or put admin.users into the User role and make every account on the
// platform a delegated administrator of its own tenant. Reading the built-in
// roles stays open: a tenant admin needs to see what their people start from.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestBuiltinRoles_TenantAdminCannotRewriteThemForEveryTenant asserts the harm
// on the stored roles, not on the status code: both rewrites a tenant admin
// could make — narrowing everybody, and widening everybody into the admin
// area — must leave the platform's User and Viewer roles as they were.
func TestBuiltinRoles_TenantAdminCannotRewriteThemForEveryTenant(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	_, email, password := seedTenant(t, store, "globex", "admin@globex.test", false)
	testutil.LoginAs(t, srv, client, email, password)
	ctx := t.Context()

	for _, role := range []string{model.RoleUser, model.RoleViewer} {
		before, err := perm.LoadRoleBase(ctx, store, role)
		require.NoError(t, err)
		require.True(t, before.Has(perm.FilesDownload), "the %s role starts with downloads", role)

		rewrites := map[string][]string{
			"narrow everybody": {string(perm.AccountEdit)},
		}
		if role == model.RoleUser {
			// The Viewer role refuses admin.* on its own (perm.ValidateRoleBase);
			// the User role takes it, with a session — which a signed-in tenant
			// admin has.
			rewrites["widen everybody into administration"] =
				append(before.Strings(), string(perm.AdminUsers))
		}
		for name, perms := range rewrites {
			t.Run(role+"/"+name, func(t *testing.T) {
				status, body := doJSON(t, client, http.MethodPut,
					srv.URL+"/api/admin/roles/builtin?role="+role,
					map[string]any{"permissions": perms})
				require.Equal(t, http.StatusForbidden, status,
					"a tenant admin rewrote the platform's %s role: %v", role, body)
				assert.Equal(t, "supertenant_only", body["error"])

				after, err := perm.LoadRoleBase(ctx, store, role)
				require.NoError(t, err)
				assert.Equal(t, before.Strings(), after.Strings(),
					"the %s role of EVERY tenant was rewritten by an admin of one of them", role)
			})
		}
	}

	// Reading stays open: the Users page shows each person's answer and its
	// source, and a tenant's admin must see what the built-in role gives.
	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/roles/builtin", nil)
	assert.Equal(t, http.StatusOK, status, "%v", body)
}

// TestBuiltinRoles_SupertenantStillEditsThem — the gate refuses the tenant, not
// the surface: the platform operator edits the built-in roles as before.
func TestBuiltinRoles_SupertenantStillEditsThem(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	email, password := testutil.SeedAdmin(t, store) // provider 1 = supertenant
	testutil.LoginAs(t, srv, client, email, password)
	assertBuiltinRoleEditable(t, srv.URL, client, store)
}

// TestBuiltinRoles_SingleTenantAdminUnaffected is the case a careless gate
// breaks: with multi-tenancy off no scope is attached, and the admin of the
// install keeps editing the built-in roles.
func TestBuiltinRoles_SingleTenantAdminUnaffected(t *testing.T) {
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.MultiTenant = false
	})
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)
	assertBuiltinRoleEditable(t, srv.URL, client, store)
}

// assertBuiltinRoleEditable takes one permission out of the User role and
// checks the stored role changed.
func assertBuiltinRoleEditable(t *testing.T, base string, client *http.Client, store db.Store) {
	t.Helper()
	before, err := perm.LoadRoleBase(t.Context(), store, model.RoleUser)
	require.NoError(t, err)
	require.True(t, before.Has(perm.FilesTag))

	status, body := doJSON(t, client, http.MethodPut, base+"/api/admin/roles/builtin",
		map[string]any{"permissions": before.Without(perm.FilesTag).Strings()})
	require.Equal(t, http.StatusOK, status, "%v", body)

	after, err := perm.LoadRoleBase(t.Context(), store, model.RoleUser)
	require.NoError(t, err)
	assert.False(t, after.Has(perm.FilesTag), "the edit did not reach the stored role")
}

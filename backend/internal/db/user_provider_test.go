package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestUserProviderAndMaintenanceMode covers the phase-4 auth core: new users join
// the default (supertenant) provider, JIT re-homes them to a tenant, lookups are
// provider-scoped, and maintenance mode (multi-tenant off + tenants present)
// locks out tenant users while the supertenant keeps signing in.
func TestUserProviderAndMaintenanceMode(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	// A new user defaults to the always-present default provider (supertenant).
	u, err := store.CreateUser(ctx, "a@x.test", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID, "new user is stamped with a provider")
	def, err := store.GetProviderBySlug(ctx, model.DefaultProviderSlug)
	require.NoError(t, err)
	assert.Equal(t, def.ID, *u.ProviderID, "new user joins the default provider")

	// Maintenance mode allows a default/supertenant user (single-tenant unchanged).
	assert.True(t, auth.LoginAllowed(ctx, store, false, u))
	assert.True(t, auth.LoginAllowed(ctx, store, true, u)) // mode on: allowed too

	// Create a tenant provider and re-home the user to it (JIT stamp).
	p, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: "a.test", Enabled: true})
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, p.ID, "oidc-sub-123"))

	// Provider-scoped lookup finds it under acme, not under default.
	got, err := store.GetUserByProviderEmail(ctx, p.ID, "a@x.test")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "oidc-sub-123", got.OIDCSubject)
	require.NotNil(t, got.ProviderID)
	assert.Equal(t, p.ID, *got.ProviderID)

	miss, err := store.GetUserByProviderEmail(ctx, def.ID, "a@x.test")
	require.NoError(t, err)
	assert.Nil(t, miss, "user is no longer in the default provider")

	// Maintenance mode (off + tenant present) LOCKS OUT the tenant user...
	assert.False(t, auth.LoginAllowed(ctx, store, false, got))
	// ...but mode-on allows it, and a nil-provider bootstrap admin is always ok.
	assert.True(t, auth.LoginAllowed(ctx, store, true, got))
	assert.True(t, auth.LoginAllowed(ctx, store, false, &model.User{}))

	// SUSPEND: a disabled tenant's users cannot log in — in either mode.
	p.Enabled = false
	require.NoError(t, store.UpdateProvider(ctx, p))
	assert.False(t, auth.LoginAllowed(ctx, store, true, got))
	assert.False(t, auth.LoginAllowed(ctx, store, false, got))
	p.Enabled = true
	require.NoError(t, store.UpdateProvider(ctx, p))
	assert.True(t, auth.LoginAllowed(ctx, store, true, got))
}

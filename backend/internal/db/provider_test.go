package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestProviderStore exercises the tenant/provider store on a real (sqlite)
// migrated DB: the migration-seeded default supertenant, CRUD, host resolution,
// storage links (idempotent) and the reverse lookup workers use.
func TestProviderStore(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()

	// Migration 00014 seeds a "default" provider that is the supertenant.
	def, err := store.GetProviderBySlug(ctx, model.DefaultProviderSlug)
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.True(t, def.IsSupertenant, "default provider is the supertenant")

	st, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, def.ID, st.ID)

	// Create a host-bound tenant.
	p, err := store.CreateProvider(ctx, &model.Provider{
		Slug: "acme", Name: "Acme", Host: "files.acme.test",
		AuthType: model.AuthTypeOIDC, OIDCIssuer: "https://kc/realms/acme",
		AdminGroup: "admins", Enabled: true,
	})
	require.NoError(t, err)
	assert.NotZero(t, p.ID)
	assert.False(t, p.IsSupertenant)
	assert.Equal(t, "files.acme.test", p.Host)

	// Resolve by host; unknown host → (nil, nil).
	byHost, err := store.GetProviderByHost(ctx, "files.acme.test")
	require.NoError(t, err)
	require.NotNil(t, byHost)
	assert.Equal(t, p.ID, byHost.ID)

	none, err := store.GetProviderByHost(ctx, "nobody.test")
	require.NoError(t, err)
	assert.Nil(t, none)

	// A disabled provider does not resolve by host.
	p.Enabled = false
	require.NoError(t, store.UpdateProvider(ctx, p))
	off, err := store.GetProviderByHost(ctx, "files.acme.test")
	require.NoError(t, err)
	assert.Nil(t, off)
	p.Enabled = true
	require.NoError(t, store.UpdateProvider(ctx, p))

	// Link a storage; linking is idempotent.
	stor, err := store.CreateStorage(ctx, &model.Storage{
		Name: "acme-store", Driver: "local", MountPath: "/",
		ConfigJSON: []byte(`{"path":"/tmp/acme"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, store.LinkProviderStorage(ctx, p.ID, stor.ID))
	require.NoError(t, store.LinkProviderStorage(ctx, p.ID, stor.ID))
	ids, err := store.ListProviderStorageIDs(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, []int64{stor.ID}, ids)

	// Reverse lookup — a worker deriving tenancy from a storage.
	pid, ok, err := store.GetProviderIDForStorage(ctx, stor.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, p.ID, pid)

	unlinked, ok, err := store.GetProviderIDForStorage(ctx, 999999)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, unlinked)

	// Unlink.
	require.NoError(t, store.UnlinkProviderStorage(ctx, p.ID, stor.ID))
	ids, err = store.ListProviderStorageIDs(ctx, p.ID)
	require.NoError(t, err)
	assert.Empty(t, ids)

	// List includes default + acme.
	all, err := store.ListProviders(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(all), 2)
}

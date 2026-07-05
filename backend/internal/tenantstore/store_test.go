package tenantstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestScopedStorageListing verifies the storage-listing chokepoint honours the
// context tenant scope: unscoped/worker + supertenant see all; a tenant sees
// only its linked storages; DenyAll sees nothing.
func TestScopedStorageListing(t *testing.T) {
	_, raw := testutil.NewTestDB(t)
	ctx := context.Background()

	mk := func(name string) int64 {
		st, err := raw.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/",
			ConfigJSON: []byte(`{"path":"/tmp/` + name + `"}`),
			SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
		})
		require.NoError(t, err)
		return st.ID
	}
	a := mk("a")
	mk("b")

	s := tenantstore.New(raw)
	names := func(ctx context.Context) []string {
		list, err := s.ListEnabledStorages(ctx)
		require.NoError(t, err)
		out := make([]string, 0, len(list))
		for _, st := range list {
			out = append(out, st.Name)
		}
		return out
	}

	// No scope (worker / single-tenant mode) → sees everything.
	assert.ElementsMatch(t, []string{"a", "b"}, names(ctx))

	// Tenant scope linked only to "a" → sees only "a".
	scoped := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 1, StorageIDs: []int64{a}})
	assert.Equal(t, []string{"a"}, names(scoped))

	// Supertenant → sees everything.
	super := tenant.WithScope(ctx, &tenant.Scope{ProviderID: 2, IsSupertenant: true})
	assert.ElementsMatch(t, []string{"a", "b"}, names(super))

	// DenyAll (unresolvable tenant) → fails closed, sees nothing.
	deny := tenant.WithScope(ctx, tenant.DenyAll)
	assert.Empty(t, names(deny))
}

// TestScopedUserDirectory is the guarantee Burak cares most about: users of
// different tenants do not see each other on the permission/grant picker (which
// lists through ListUsers).
func TestScopedUserDirectory(t *testing.T) {
	_, raw := testutil.NewTestDB(t)
	ctx := context.Background()

	pa, err := raw.CreateProvider(ctx, &model.Provider{Slug: "ta", Name: "TA", Enabled: true})
	require.NoError(t, err)
	pb, err := raw.CreateProvider(ctx, &model.Provider{Slug: "tb", Name: "TB", Enabled: true})
	require.NoError(t, err)

	ua, err := raw.CreateUser(ctx, "a@ta.test", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, raw.SetUserProvider(ctx, ua.ID, pa.ID, ""))
	ub, err := raw.CreateUser(ctx, "b@tb.test", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, raw.SetUserProvider(ctx, ub.ID, pb.ID, ""))

	s := tenantstore.New(raw)
	emails := func(ctx context.Context) []string {
		list, err := s.ListUsers(ctx)
		require.NoError(t, err)
		out := make([]string, 0, len(list))
		for _, u := range list {
			out = append(out, u.Email)
		}
		return out
	}

	// Tenant A sees only its own user, never tenant B's.
	got := emails(tenant.WithScope(ctx, &tenant.Scope{ProviderID: pa.ID}))
	assert.Contains(t, got, "a@ta.test")
	assert.NotContains(t, got, "b@tb.test")

	// Supertenant sees across tenants.
	super := emails(tenant.WithScope(ctx, &tenant.Scope{ProviderID: 999, IsSupertenant: true}))
	assert.Subset(t, super, []string{"a@ta.test", "b@tb.test"})

	// DenyAll sees nobody.
	assert.Empty(t, emails(tenant.WithScope(ctx, tenant.DenyAll)))
}

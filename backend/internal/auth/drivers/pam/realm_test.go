//go:build linux

package pam

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func realmCtx(t *testing.T, store db.Store, realm string) context.Context {
	t.Helper()
	lr, err := auth.ResolveLoginRealm(context.Background(), store, realm, realm != "", "", "")
	require.NoError(t, err)
	return auth.WithLoginRealm(context.Background(), lr)
}

// An operating-system login in a tenant's realm (#128): `alice@acme.local`, homed
// in acme — the same machine account in beta is beta's own `alice@beta.local`;
// the protocols' `alice@acme.local` is read back in its realm and nowhere else.
func TestRealm_TheAddressCarriesTheRealm(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true, "multi_tenant": true})
	ctx := context.Background()
	acme, err := r.store.CreateProvider(ctx, &model.Provider{Slug: "acme", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	beta, err := r.store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	require.NoError(t, err)

	ua, err := r.d.VerifyPassword(realmCtx(t, r.store, "acme"), "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, "alice@acme.local", ua.Email)
	assert.Equal(t, acme.ID, *ua.ProviderID)

	ub, err := r.d.VerifyPassword(realmCtx(t, r.store, "beta"), "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, "alice@beta.local", ub.Email)
	assert.Equal(t, beta.ID, *ub.ProviderID)
	assert.NotEqual(t, ua.ID, ub.ID)

	again, err := r.d.VerifyPassword(realmCtx(t, r.store, "acme"), "alice@acme.local", realPW)
	require.NoError(t, err)
	assert.Equal(t, ua.ID, again.ID, "the protocols' address, in its own realm")

	_, err = r.d.VerifyPassword(realmCtx(t, r.store, "acme"), "alice@beta.local", realPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "another realm's address is not this realm's to take apart")
	_, err = r.d.VerifyPassword(realmCtx(t, r.store, "acme"), "alice@local", realPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "nor is the platform's")
}

// With email_domain the address is the same in every realm — so it can be
// another tenant's account, which is refused and not written to.
func TestRealm_ADomainAddressOfAnotherTenantIsRefused(t *testing.T) {
	r := newRig(t, map[string]any{"auto_create": true, "multi_tenant": true, "email_domain": "corp.example"})
	ctx := context.Background()
	_, err := r.store.CreateProvider(ctx, &model.Provider{Slug: "acme", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	beta, err := r.store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	theirs := addUser(t, r.store, "alice@corp.example")
	require.NoError(t, r.store.SetUserProvider(ctx, theirs.ID, beta.ID, ""))

	_, err = r.d.VerifyPassword(realmCtx(t, r.store, "acme"), "alice", realPW)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	groups, err := r.store.ListUserSSOGroups(ctx, theirs.ID)
	require.NoError(t, err)
	assert.Empty(t, groups, "nothing was written to another tenant's account")

	u, err := r.d.VerifyPassword(realmCtx(t, r.store, "beta"), "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, theirs.ID, u.ID)
}

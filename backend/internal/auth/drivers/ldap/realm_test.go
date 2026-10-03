package ldap

// A directory sign-in in a tenant's realm (#128). The realm goes into the
// address a directory entry with no e-mail gets — `alex@acme.local`, not
// `alex@local` — so two tenants' `alex` are two accounts; the account is homed
// realm > host > pin; an older account is adopted only in its own realm; and
// whatever the directory answers, an account of another tenant is never signed
// in to (or written to) through this realm.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func inRealm(t *testing.T, store db.Store, realm string) context.Context {
	t.Helper()
	lr, err := auth.ResolveLoginRealm(context.Background(), store, realm, realm != "", "", "")
	require.NoError(t, err)
	return auth.WithLoginRealm(context.Background(), lr)
}

func providerBySlug(t *testing.T, store db.Store, slug string) *model.Provider {
	t.Helper()
	p, err := store.GetProviderBySlug(context.Background(), slug)
	require.NoError(t, err)
	require.NotNil(t, p)
	return p
}

func TestLDAPRealm_TwoTenantsTwoAlexes(t *testing.T) {
	d, store, dir := uidFixture(t, map[string]any{"multi_tenant": true}, "alex")
	seedProvider(t, store, "acme", "")
	seedProvider(t, store, "beta", "")
	acme, beta := providerBySlug(t, store, "acme"), providerBySlug(t, store, "beta")

	ua, _, err := d.Login(inRealm(t, store, "acme"), "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, "alex@acme.local", ua.Email)
	require.NotNil(t, ua.ProviderID)
	assert.Equal(t, acme.ID, *ua.ProviderID)

	ub, _, err := d.Login(inRealm(t, store, "beta"), "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, "alex@beta.local", ub.Email)
	assert.Equal(t, beta.ID, *ub.ProviderID)
	assert.NotEqual(t, ua.ID, ub.ID, "two tenants, two people")

	// The file protocols ask with the account's address; in its realm it is
	// taken apart back into the login name the directory knows.
	dir.searches = nil
	u, err := d.VerifyPassword(inRealm(t, store, "acme"), "alex@acme.local", "pw")
	require.NoError(t, err)
	assert.Equal(t, ua.ID, u.ID)
	assert.Contains(t, dir.filters(), "(uid=alex)")

	// The platform's own realm, named by nothing and pinned to nothing: the
	// account would be the supertenant's, which a directory never makes.
	_, _, err = d.Login(inRealm(t, store, ""), "alex", "pw")
	require.ErrorIs(t, err, auth.ErrNoTenantForLogin)
	_, err = store.GetUserByEmail(context.Background(), "alex@local")
	assert.Error(t, err, "no alex@local was made")
}

// The pin homes an UN-named sign-in in its tenant, with that tenant's realm in
// the address, and the sign-in's realm admits the account for that sign-in.
func TestLDAPRealm_ThePinGivesItsRealm(t *testing.T) {
	d, store, _ := uidFixture(t, map[string]any{"multi_tenant": true, "provider": "acme"}, "alex")
	seedProvider(t, store, "acme", "")
	acme := providerBySlug(t, store, "acme")

	ctx := inRealm(t, store, "")
	u, err := d.VerifyPassword(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, "alex@acme.local", u.Email)
	assert.Equal(t, acme.ID, *u.ProviderID)
	assert.True(t, auth.LoginRealmAdmits(ctx, u), "the pin extends this sign-in to its tenant")
	assert.False(t, auth.LoginRealmAdmits(inRealm(t, store, ""), u), "…and only this one")
}

// Adoption (auth.AdoptAccount) works in the realm: an older bare `alex` in acme
// becomes `alex@acme.local`; one in beta is not touched by a sign-in to acme.
func TestLDAPRealm_AdoptionStaysInItsRealm(t *testing.T) {
	d, store, _ := uidFixture(t, map[string]any{"multi_tenant": true}, "alex", "bob")
	seedProvider(t, store, "acme", "")
	seedProvider(t, store, "beta", "")
	acme, beta := providerBySlug(t, store, "acme"), providerBySlug(t, store, "beta")
	ctx := context.Background()

	oldAlex := oldAccount(t, store, "alex")
	require.NoError(t, store.SetUserProvider(ctx, oldAlex.ID, acme.ID, ""))
	oldBob := oldAccount(t, store, "bob")
	require.NoError(t, store.SetUserProvider(ctx, oldBob.ID, beta.ID, ""))

	u, _, err := d.Login(inRealm(t, store, "acme"), "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, oldAlex.ID, u.ID, "adopted, not doubled")
	assert.Equal(t, "alex@acme.local", u.Email)

	b, _, err := d.Login(inRealm(t, store, "acme"), "bob", "pw")
	require.NoError(t, err)
	assert.NotEqual(t, oldBob.ID, b.ID, "beta's older account is not acme's to take")
	assert.Equal(t, "bob@acme.local", b.Email)
	still, err := store.GetUser(ctx, oldBob.ID)
	require.NoError(t, err)
	assert.Equal(t, "bob", still.Email, "left exactly as it was")
	assert.Equal(t, beta.ID, *still.ProviderID)
}

// ⚠ The 0.50 form `alex@local` is the platform's own tenant's address. A
// tenant's sign-in never looks at it — 0.50 has not shipped, so no tenant
// account can carry it; pinned here so it stays that way.
func TestLDAPRealm_ThePlatformsAddressIsNotATenants(t *testing.T) {
	d, store, _ := uidFixture(t, map[string]any{"multi_tenant": true}, "alex")
	seedProvider(t, store, "acme", "")
	acme := providerBySlug(t, store, "acme")
	ctx := context.Background()

	main, err := store.CreateUser(ctx, "alex@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	u, _, err := d.Login(inRealm(t, store, "acme"), "alex", "pw")
	require.NoError(t, err)
	assert.NotEqual(t, main.ID, u.ID)
	assert.Equal(t, "alex@acme.local", u.Email)
	assert.Equal(t, acme.ID, *u.ProviderID)
	again, err := store.GetUser(ctx, main.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex@local", again.Email, "the platform's account is untouched")
	assert.Equal(t, supertenantID(t, store), *again.ProviderID)
}

// A directory address (the mail attribute) is the same in every realm, so it
// can name another tenant's account: that account is refused, and nothing is
// written to it.
func TestLDAPRealm_AnotherTenantsAccountIsRefused(t *testing.T) {
	d, store := ldapFixture(t, map[string]any{"multi_tenant": true, "group_attr": "memberOf"})
	seedProvider(t, store, "acme", "")
	seedProvider(t, store, "beta", "")
	beta := providerBySlug(t, store, "beta")
	ctx := context.Background()
	theirs, err := store.CreateUser(ctx, "ayse@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, theirs.ID, beta.ID, ""))

	_, _, err = d.Login(inRealm(t, store, "acme"), "ayse@example.com", "directory-pw")
	require.ErrorIs(t, err, auth.ErrUnauthorized)
	groups, err := store.ListUserSSOGroups(ctx, theirs.ID)
	require.NoError(t, err)
	assert.Empty(t, groups, "no group was recorded on another tenant's account")

	u, _, err := d.Login(inRealm(t, store, "beta"), "ayse@example.com", "directory-pw")
	require.NoError(t, err)
	assert.Equal(t, theirs.ID, u.ID, "in its own realm it signs in")
}

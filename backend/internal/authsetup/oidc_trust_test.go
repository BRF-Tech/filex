package authsetup_test

// Two 0.50 rules of the OIDC construction (docs/SSO.md, "Which account an SSO
// sign-in opens"):
//
//   - every OIDC driver is bound to a tenant when it is built: a tenant's own
//     instance to its tenant (pinned), every other one to the platform's own
//     tenant until a flow names another;
//   - an upgrade keeps the behaviour of the OIDC providers that already
//     existed: their "trust this provider's email addresses" comes ON, once,
//     and the page is told the upgrade set it (UpgradeOIDCTrust). A fresh
//     install trusts nothing.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

func TestOIDCBuild_ATenantsOwnInstanceIsPinnedToItsTenant(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{authsetup.InstancesSetting: "1"})
	acme := tenantOf(t, store, "acme")
	require.NoError(t, store.SetProviderAllowInsecureAuth(ctx, acme.ID, true))
	idp := fakeidp.New(t)
	cfg := `{"issuer":"` + idp.Issuer() + `","client_id":"acme","redirect_url":"https://files.example/api/auth/oidc/callback"}`
	owner := acme.ID
	own, err := store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "acme-oidc", Driver: "oidc", Origin: model.AuthOriginTenant,
		OwnerProviderID: &owner, Enabled: true, ConfigJSON: cfg})
	require.NoError(t, err)
	require.NoError(t, store.BindAuthInstance(ctx, acme.ID, own.ID, model.AuthBindExplicit))
	shared, err := store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "corp-sso", Driver: "oidc", Origin: model.AuthOriginPage,
		Enabled: true, ConfigJSON: cfg})
	require.NoError(t, err)
	require.NoError(t, store.BindAuthInstance(ctx, acme.ID, shared.ID, model.AuthBindExplicit))
	l := multiLive(t, store, &fakeDirectory{}, "")
	require.NoError(t, l.Reload(ctx))

	tenantOfEntry := func(slug string) (authoidc.Tenant, bool) {
		t.Helper()
		e, ok := l.Current().Entry(slug)
		require.True(t, ok, slug)
		require.NotNil(t, e.Driver, "%s: %s", slug, e.Err)
		d, ok := e.Driver.(*authoidc.Driver)
		require.True(t, ok)
		return d.BoundTenant()
	}
	got, pinned := tenantOfEntry("acme-oidc")
	assert.True(t, pinned, "a tenant's own OIDC serves its tenant only")
	assert.Equal(t, authoidc.Tenant{ProviderID: acme.ID}, got)
	got, pinned = tenantOfEntry("corp-sso")
	assert.False(t, pinned, "a shared OIDC is told its tenant by each flow")
	assert.Equal(t, authoidc.MainTenant(), got, "and is the platform's own tenant's until then - never every tenant's")
}

// seedExisting is what a database upgraded from 0.49 holds: the page's OIDC
// in settings, a further OIDC instance, a tenant's own OIDC on its row, an
// account opened by an SSO sign-in (no password), and the mark migration
// 00079 writes on such a database.
func seedExisting(t *testing.T, store db.Store) (*model.Provider, *model.Provider) {
	t.Helper()
	ctx := context.Background()
	put(t, store, map[string]string{
		"auth.oidc.issuer": "https://idp.example/realms/main", "auth.oidc.client_id": "filex", "auth.oidc.enabled": "true",
		"auth.oidc_trust.upgrade": "pending",
	})
	_, err := store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "corp-sso", Driver: "oidc", Origin: model.AuthOriginPage,
		Enabled: true, ConfigJSON: `{"issuer":"https://corp.example","client_id":"filex"}`})
	require.NoError(t, err)
	_, err = store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "corp-dir", Driver: "ldap", Origin: model.AuthOriginPage,
		Enabled: true, ConfigJSON: `{"url":"ldaps://dir.example","base_dn":"dc=example"}`})
	require.NoError(t, err)
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true, AuthType: model.AuthTypeOIDC,
		OIDCIssuer: "https://acme.example", OIDCClientID: "acme"})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true, AuthType: "local"})
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "sso@example.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	return acme, beta
}

func TestUpgradeOIDCTrust_TheProvidersThatExistedKeepTrusting(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	acme, beta := seedExisting(t, store)

	require.NoError(t, authsetup.UpgradeOIDCTrust(ctx, store, nil))

	assert.Equal(t, "true", get(t, store, "auth.oidc.trust_email"), "the page's OIDC")
	row, err := store.GetAuthInstanceBySlug(ctx, "corp-sso")
	require.NoError(t, err)
	assert.Contains(t, row.ConfigJSON, `"trust_email":"true"`, "a further instance")
	dir, err := store.GetAuthInstanceBySlug(ctx, "corp-dir")
	require.NoError(t, err)
	assert.NotContains(t, dir.ConfigJSON, "trust_email", "an LDAP has no such setting")
	p, err := store.GetProvider(ctx, acme.ID)
	require.NoError(t, err)
	assert.True(t, p.OIDCTrustEmail, "a tenant's own OIDC on its row")
	p, err = store.GetProvider(ctx, beta.ID)
	require.NoError(t, err)
	assert.False(t, p.OIDCTrustEmail, "a tenant with no OIDC of its own")

	rec := authsetup.ReadTrustUpgrade(ctx, store)
	assert.ElementsMatch(t, []string{"oidc", "corp-sso"}, rec.Instances)
	assert.Equal(t, []int64{acme.ID}, rec.Tenants)
	trust, byUpgrade := authsetup.EnvTrustEmail(ctx, store, nil)
	assert.True(t, trust, "an account came through SSO: the environment's OIDC keeps trusting")
	assert.True(t, byUpgrade)
	assert.Equal(t, "done", get(t, store, "auth.oidc_trust.upgrade"))

	// Once: what an operator switches off afterwards stays off.
	require.NoError(t, store.SetProviderOIDCTrustEmail(ctx, acme.ID, false))
	put(t, store, map[string]string{"auth.oidc.trust_email": "false"})
	require.NoError(t, authsetup.UpgradeOIDCTrust(ctx, store, nil))
	p, err = store.GetProvider(ctx, acme.ID)
	require.NoError(t, err)
	assert.False(t, p.OIDCTrustEmail)
	assert.Equal(t, "false", get(t, store, "auth.oidc.trust_email"))

	// Saved by someone: no longer the upgrade's.
	authsetup.ForgetTrustUpgradeInstance(ctx, store, "corp-sso")
	authsetup.ForgetTrustUpgradeTenant(ctx, store, acme.ID)
	rec = authsetup.ReadTrustUpgrade(ctx, store)
	assert.Equal(t, []string{"oidc"}, rec.Instances)
	assert.Empty(t, rec.Tenants)
}

func TestUpgradeOIDCTrust_AFreshInstallTrustsNothing(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{"auth.oidc.issuer": "https://idp.example", "auth.oidc.client_id": "filex"})
	require.NoError(t, authsetup.UpgradeOIDCTrust(ctx, store, nil))
	assert.Empty(t, get(t, store, "auth.oidc.trust_email"), "no mark: a provider made on 0.50 starts with trust off")
	trust, byUpgrade := authsetup.EnvTrustEmail(ctx, store, nil)
	assert.False(t, trust)
	assert.False(t, byUpgrade)
	assert.Empty(t, authsetup.ReadTrustUpgrade(ctx, store).Instances)
}

func TestUpgradeOIDCTrust_NoSSOAccountsNoTrustForTheEnvironment(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	testutil.SeedAdmin(t, store)
	put(t, store, map[string]string{"auth.oidc_trust.upgrade": "pending"})
	require.NoError(t, authsetup.UpgradeOIDCTrust(ctx, store, nil))
	trust, _ := authsetup.EnvTrustEmail(ctx, store, nil)
	assert.False(t, trust, "every account has a password of its own: nobody came through SSO")
}

func TestEnvTrustEmail_TheEnvironmentSaysItWhenItSaysIt(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{"auth.oidc_trust.environment": "true"})
	off, on := false, true
	trust, byUpgrade := authsetup.EnvTrustEmail(ctx, store, &off)
	assert.False(t, trust, "FILEX_OIDC_TRUST_EMAIL=false wins over the upgrade's answer")
	assert.False(t, byUpgrade)
	trust, byUpgrade = authsetup.EnvTrustEmail(ctx, store, &on)
	assert.True(t, trust)
	assert.False(t, byUpgrade, "set by the operator, not by the upgrade")
}

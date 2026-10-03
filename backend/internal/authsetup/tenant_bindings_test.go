package authsetup_test

// Sign-in providers bound to tenants (docs/TENANT-ADMIN.md):
//
//   - the upgrade binds every provider that exists to every tenant that
//     exists (what happened before: one LDAP served every tenant) and asks the
//     operator to review it; a provider added afterwards serves the platform's
//     own tenant only; a 0.50 pin is a binding to the pinned tenant;
//   - on a multi-tenant install a password sign-in is judged by the providers
//     of the tenant it is for, and so is a file-protocol password: a person in
//     a directory gets nothing from a tenant the directory does not serve;
//   - `local` (filex's own passwords) is not an instance and serves everybody;
//   - a single-tenant install is unchanged: every provider serves every
//     sign-in.

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// fakeDirectory is an environment "ldap" that says yes to one password and
// counts how often it was asked.
type fakeDirectory struct {
	asked atomic.Int32
	user  *model.User
}

func (f *fakeDirectory) Name() string                               { return "ldap" }
func (f *fakeDirectory) Init(context.Context, map[string]any) error { return nil }
func (f *fakeDirectory) Authenticate(*http.Request) (*model.User, error) {
	return nil, auth.ErrUnauthorized
}
func (f *fakeDirectory) Capabilities() auth.Capabilities      { return auth.Capabilities{SignIn: true} }
func (f *fakeDirectory) Logout(context.Context, string) error { return nil }
func (f *fakeDirectory) Login(_ context.Context, id, pw string) (*model.User, string, error) {
	f.asked.Add(1)
	if pw != "dir-pw" {
		return nil, "", auth.ErrUnauthorized
	}
	return f.user, "session-from-directory", nil
}
func (f *fakeDirectory) VerifyPassword(_ context.Context, id, pw string) (*model.User, error) {
	f.asked.Add(1)
	if pw != "dir-pw" {
		return nil, auth.ErrUnauthorized
	}
	return f.user, nil
}

func tenantOf(t *testing.T, store db.Store, slug string) *model.Provider {
	t.Helper()
	p, err := store.CreateProvider(context.Background(), &model.Provider{Slug: slug, Name: slug, Enabled: true})
	require.NoError(t, err)
	return p
}

// multiLive is a multi-tenant Live with `local` and an environment directory
// (optionally pinned to a tenant), started: its first reload runs the upgrade.
func multiLive(t *testing.T, store db.Store, dir *fakeDirectory, pin string) *authsetup.Live {
	t.Helper()
	local, err := authsetup.Build(context.Background(), store, "local", nil)
	require.NoError(t, err)
	cfg := map[string]any{"url": "ldap://dir.example", "base_dn": "dc=example"}
	if pin != "" {
		cfg["provider"] = pin
	}
	env := []authsetup.Entry{
		authsetup.NewEnvEntry("local", "FILEX_AUTH_DRIVERS", nil, local, nil, false),
		authsetup.NewEnvEntry("ldap", "FILEX_AUTH_DRIVERS", cfg, dir, nil, true),
	}
	l, err := authsetup.New(context.Background(), authsetup.Options{Store: store, Box: box(t, testKey), MultiTenant: true,
		RecoveryLogin: true, PublicURL: "https://files.example", BuildTimeout: 3 * time.Second}, env)
	require.NoError(t, err)
	l.Start(context.Background())
	return l
}

func realmCtx(p *model.Provider) context.Context {
	return auth.WithLoginRealm(context.Background(), &auth.LoginRealm{Tenant: p, Named: true})
}

func TestUpgrade_BindsEveryProviderToEveryTenantAndAsksForAReview(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	acme, beta := tenantOf(t, store, "acme"), tenantOf(t, store, "beta")
	put(t, store, map[string]string{"auth.oidc.enabled": "false", "auth.oidc.issuer": "https://idp.example"})
	l := multiLive(t, store, &fakeDirectory{}, "")

	main, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	b := l.Current().Bindings()
	require.NotNil(t, b)
	for _, slug := range []string{"ldap", "oidc"} {
		row, err := store.GetAuthInstanceBySlug(ctx, slug)
		require.NoError(t, err)
		require.NotNil(t, row, "%s has its row", slug)
		assert.Equal(t, []int64{main.ID, acme.ID, beta.ID}, b.TenantsOf(row.ID), "%s serves every tenant that existed", slug)
	}
	local, err := store.GetAuthInstanceBySlug(ctx, "local")
	require.NoError(t, err)
	assert.Nil(t, local, "local is not an instance")
	assert.Equal(t, "1", get(t, store, authsetup.ReviewSetting), "the operator is asked to review the bindings")
	assert.Equal(t, "1", get(t, store, authsetup.InstancesSetting))

	// After the upgrade: a new tenant gets no shared provider, and a provider
	// saved afterwards serves the platform's own tenant only.
	gamma := tenantOf(t, store, "gamma")
	put(t, store, map[string]string{"auth.proxy-header.enabled": "true", "auth.proxy-header.trusted_proxies": "10.0.0.0/8"})
	require.NoError(t, l.Reload(ctx))
	b = l.Current().Bindings()
	ph, err := store.GetAuthInstanceBySlug(ctx, "proxy-header")
	require.NoError(t, err)
	require.NotNil(t, ph)
	assert.Equal(t, []int64{main.ID}, b.TenantsOf(ph.ID))
	ldap, _ := store.GetAuthInstanceBySlug(ctx, "ldap")
	assert.NotContains(t, b.TenantsOf(ldap.ID), gamma.ID, "a tenant made after the upgrade is not bound by it")

	// Once per database: a second start binds nothing anew.
	require.NoError(t, store.UnbindAuthInstance(ctx, beta.ID, ldap.ID))
	l2 := multiLive(t, store, &fakeDirectory{}, "")
	assert.NotContains(t, l2.Current().Bindings().TenantsOf(ldap.ID), beta.ID, "the operator's unbinding stands")
}

// An operating-system provider the page had already switched on promoted its
// test account in the platform's tenant before the upgrade: its row starts
// with that scope taken, so a later test there promotes nobody. One that was
// off has promoted nobody yet.
func TestUpgrade_AnOSProviderAlreadyOnHasPromotedInThePlatform(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	// A 0.49 database: the page's rows were applied (v0.43.0's import ran).
	put(t, store, map[string]string{
		authsetup.SchemaSetting: "2",
		"auth.pam.enabled":      "true", "auth.pam.service": "login",
		"auth.windows.enabled": "false", "auth.windows.domain": "CORP",
	})
	multiLive(t, store, &fakeDirectory{}, "")
	pam, err := store.GetAuthInstanceBySlug(ctx, "pam")
	require.NoError(t, err)
	require.NotNil(t, pam)
	assert.True(t, pam.PromotedIn(0))
	win, err := store.GetAuthInstanceBySlug(ctx, "windows")
	require.NoError(t, err)
	require.NotNil(t, win)
	assert.False(t, win.PromotedIn(0))
}

func TestUpgrade_APinIsABindingToThePinnedTenant(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	// The upgrade already ran on this database (a tenant made since).
	put(t, store, map[string]string{authsetup.InstancesSetting: "1"})
	acme := tenantOf(t, store, "acme")
	l := multiLive(t, store, &fakeDirectory{}, "acme")
	ldap, err := store.GetAuthInstanceBySlug(ctx, "ldap")
	require.NoError(t, err)
	main, _ := store.GetSupertenant(ctx)
	assert.Equal(t, []int64{main.ID, acme.ID}, l.Current().Bindings().TenantsOf(ldap.ID))
	rows, err := store.ListAuthBindings(ctx)
	require.NoError(t, err)
	sources := map[int64]string{}
	for _, r := range rows {
		if r.InstanceID == ldap.ID {
			sources[r.ProviderID] = r.Source
		}
	}
	assert.Equal(t, model.AuthBindPin, sources[acme.ID])
}

func TestLive_APasswordIsJudgedByTheTenantsProviders(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{authsetup.InstancesSetting: "1"})
	acme, beta := tenantOf(t, store, "acme"), tenantOf(t, store, "beta")
	dir := &fakeDirectory{user: &model.User{ID: 99, Email: "alex@acme.local", Enabled: true}}
	l := multiLive(t, store, dir, "")
	ldap, err := store.GetAuthInstanceBySlug(ctx, "ldap")
	require.NoError(t, err)
	require.NoError(t, store.BindAuthInstance(ctx, acme.ID, ldap.ID, model.AuthBindExplicit))
	require.NoError(t, l.Reload(ctx))

	// acme is served by the directory.
	u, tok, err := l.Login().Login(realmCtx(acme), "alex", "dir-pw")
	require.NoError(t, err)
	assert.Equal(t, int64(99), u.ID)
	assert.Equal(t, "session-from-directory", tok)
	asked := dir.asked.Load()
	require.Positive(t, asked)

	// beta is not: the directory is never asked, the answer is a refusal.
	_, _, err = l.Login().Login(realmCtx(beta), "alex", "dir-pw")
	assert.Error(t, err)
	assert.Equal(t, asked, dir.asked.Load(), "a directory that does not serve beta was asked for a beta sign-in")

	// The file protocols ask the same question.
	_, err = l.Dir().VerifyPassword(realmCtx(beta), "alex", "dir-pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, asked, dir.asked.Load())
	du, err := l.Dir().VerifyPassword(realmCtx(acme), "alex", "dir-pw")
	require.NoError(t, err)
	assert.Equal(t, int64(99), du.ID)

	// `local` serves every tenant: beta's own administrator signs in.
	email, pw := testutil.SeedAdmin(t, store)
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, admin.ID, beta.ID, ""))
	u, _, err = l.Login().Login(realmCtx(beta), email, pw)
	require.NoError(t, err)
	assert.Equal(t, admin.ID, u.ID)

	// Unbinding takes it away at the next reload.
	require.NoError(t, store.UnbindAuthInstance(ctx, acme.ID, ldap.ID))
	require.NoError(t, l.Reload(ctx))
	_, _, err = l.Login().Login(realmCtx(acme), "alex", "dir-pw")
	assert.Error(t, err)
}

func TestLive_SingleTenantEveryProviderServesEverySignIn(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	acme := tenantOf(t, store, "acme")
	dir := &fakeDirectory{user: &model.User{ID: 7, Email: "alex@local", Enabled: true}}
	local, err := authsetup.Build(ctx, store, "local", nil)
	require.NoError(t, err)
	l, err := authsetup.New(ctx, authsetup.Options{Store: store, Box: box(t, testKey), BuildTimeout: 3 * time.Second}, []authsetup.Entry{
		authsetup.NewEnvEntry("local", "FILEX_AUTH_DRIVERS", nil, local, nil, false),
		authsetup.NewEnvEntry("ldap", "FILEX_AUTH_DRIVERS", map[string]any{}, dir, nil, true),
	})
	require.NoError(t, err)
	l.Start(ctx)
	ldap, err := store.GetAuthInstanceBySlug(ctx, "ldap")
	require.NoError(t, err)
	require.NoError(t, store.UnbindAuthInstance(ctx, acme.ID, ldap.ID))
	require.NoError(t, l.Reload(ctx))
	// Bindings are not read on a single-tenant install.
	u, _, err := l.Login().Login(realmCtx(acme), "alex", "dir-pw")
	require.NoError(t, err)
	assert.Equal(t, int64(7), u.ID)
	assert.Equal(t, "login-chain(local,ldap)", l.LoginName())
}

func TestLive_RecoveryIsThePlatformsOnly(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{authsetup.InstancesSetting: "1"})
	acme := tenantOf(t, store, "acme")
	// No `local` in the environment: the platform's own tenant gets the
	// bootstrap administrator's recovery sign-in, acme does not.
	l, err := authsetup.New(ctx, authsetup.Options{Store: store, Box: box(t, testKey), MultiTenant: true, RecoveryLogin: true}, nil)
	require.NoError(t, err)
	l.Start(ctx)
	main, _ := store.GetSupertenant(ctx)
	assert.True(t, l.Current().For(main.ID).Recovery())
	assert.True(t, l.Current().For(main.ID).PasswordLogin())
	assert.False(t, l.Current().For(acme.ID).Recovery())
	assert.False(t, l.Current().For(acme.ID).PasswordLogin())
}

// The last way in, per tenant: acme's only way in is the directory, the
// platform's own tenant keeps its administrator's password.
func TestAdminPathsIn_PerTenant(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	put(t, store, map[string]string{authsetup.InstancesSetting: "1"})
	acme := tenantOf(t, store, "acme")
	testutil.SeedAdmin(t, store)
	dir := &fakeDirectory{}
	l := multiLive(t, store, dir, "")
	ldap, _ := store.GetAuthInstanceBySlug(ctx, "ldap")
	require.NoError(t, store.BindAuthInstance(ctx, acme.ID, ldap.ID, model.AuthBindExplicit))
	require.NoError(t, l.Reload(ctx))

	without := func(e authsetup.Entry) bool { return e.Slug == "ldap" }
	assert.Equal(t, []string{"ldap"}, l.AdminPathsIn(ctx, acme.ID, nil))
	assert.Empty(t, l.AdminPathsIn(ctx, acme.ID, without), "acme has no administrator with a password: the directory is its last way in")
	main, _ := store.GetSupertenant(ctx)
	assert.Contains(t, l.AdminPathsIn(ctx, main.ID, without), "password", "the platform's administrator still has the password form")
}

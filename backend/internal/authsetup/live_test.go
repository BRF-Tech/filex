package authsetup_test

// The Identity providers page manages sign-in (owner's decision, v0.43.0):
// what it saves is built through the one construction path, applied without
// a restart, never in front of the environment's providers, and never able
// to take the administrator's way in down with it.

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authldap "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const testKey = "authsetup-test-secret-key"

func box(t *testing.T, key string) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(key)
	require.NoError(t, err)
	return b
}

func put(t *testing.T, store db.Store, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		require.NoError(t, store.UpsertSetting(context.Background(), k, v))
	}
}

func get(t *testing.T, store db.Store, k string) string {
	t.Helper()
	v, _ := store.GetSetting(context.Background(), k)
	return v
}

// live builds a Live the way server.go does: `local` from the environment
// (plus any extra environment providers), the page's on top.
func live(t *testing.T, store db.Store, logBuf *bytes.Buffer, extraEnv ...authsetup.Entry) *authsetup.Live {
	t.Helper()
	local, err := authsetup.Build(context.Background(), store, "local", nil)
	require.NoError(t, err)
	env := append([]authsetup.Entry{authsetup.NewEnvEntry("local", "FILEX_AUTH_DRIVERS", nil, local, nil, false)}, extraEnv...)
	opts := authsetup.Options{Store: store, Box: box(t, testKey), RecoveryLogin: true, PublicURL: "https://files.example",
		BuildTimeout: 3 * time.Second}
	if logBuf != nil {
		opts.Log = slog.New(slog.NewTextHandler(logBuf, nil))
	}
	l, err := authsetup.New(context.Background(), opts, env)
	require.NoError(t, err)
	l.Start(context.Background())
	return l
}

func TestBuild_IsTheOneConstructionPath(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d, err := authsetup.Build(context.Background(), store, "ldap", map[string]any{"url": "ldap://dc.example:389", "base_dn": "dc=example"})
	require.NoError(t, err)
	_, isLDAP := d.(*authldap.Driver)
	assert.True(t, isLDAP)
	_, err = authsetup.Build(context.Background(), store, "ldap", map[string]any{})
	assert.Error(t, err, "Init's own validation runs")
	_, err = authsetup.Build(context.Background(), store, "kerberos", nil)
	assert.Error(t, err)
	d, err = authsetup.Build(context.Background(), store, "header_proxy", map[string]any{"trusted_proxies": "10.0.0.0/8"})
	require.NoError(t, err)
	assert.Equal(t, "proxy-header", d.Name(), "the environment's other spellings reach the same driver")
}

// Rows the page saved before v0.43.0 were applied by no server. Switching
// them on at upgrade would switch on sign-in configuration an operator may
// have typed long ago and forgotten: they are imported OFF and marked.
func TestUpgradeLegacy_SavedButNeverAppliedIsImportedSwitchedOff(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	put(t, store, map[string]string{
		"auth.oidc.enabled":       "true",
		"auth.oidc.issuer":        "https://idp.example/realms/main",
		"auth.oidc.client_secret": "old-plain-secret",
		"auth.bootstrap_admin_id": "1",
	})
	require.NoError(t, authsetup.UpgradeLegacy(context.Background(), store, box(t, testKey), slog.Default()))

	assert.Equal(t, "false", get(t, store, "auth.oidc.enabled"), "imported switched OFF")
	assert.Equal(t, "true", get(t, store, "auth.oidc.legacy_unapplied"), "and marked for the page")
	sealed := get(t, store, "auth.oidc.client_secret")
	assert.True(t, secretbox.IsSealed(sealed), "the secret is sealed at rest")
	plain, err := box(t, testKey).Open(sealed)
	require.NoError(t, err)
	assert.Equal(t, "old-plain-secret", plain)
	assert.Equal(t, "1", get(t, store, "auth.bootstrap_admin_id"), "an unrelated auth.* row is not touched")
	assert.Equal(t, "", get(t, store, "auth.ldap.enabled"), "a provider with no rows gets none")

	// Once per database: the operator's own decision afterwards stands.
	put(t, store, map[string]string{"auth.oidc.enabled": "true"})
	require.NoError(t, authsetup.UpgradeLegacy(context.Background(), store, box(t, testKey), slog.Default()))
	assert.Equal(t, "true", get(t, store, "auth.oidc.enabled"))
}

// With no FILEX_SECRET_KEY a legacy secret cannot be sealed — and it is not
// kept in the clear either. It was never used; the page asks for it again.
func TestUpgradeLegacy_WithoutAKeyTheClearSecretGoes(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	put(t, store, map[string]string{"auth.ldap.bind_password": "clear-bind-pw", "auth.ldap.url": "ldap://dc:389"})
	var logs bytes.Buffer
	require.NoError(t, authsetup.UpgradeLegacy(context.Background(), store, box(t, ""), slog.New(slog.NewTextHandler(&logs, nil))))
	assert.Equal(t, "", get(t, store, "auth.ldap.bind_password"))
	assert.Equal(t, "ldap://dc:389", get(t, store, "auth.ldap.url"))
	assert.Contains(t, logs.String(), "bind_password", "the log names the field")
	assert.NotContains(t, logs.String(), "clear-bind-pw", "and never the value")
}

// Saved → running, without a restart; switched off → gone. The login page's
// list follows (OnSwap feeds the capability service).
func TestLive_APageProviderAppliesWithoutARestart(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	l := live(t, store, nil)
	var offered []string
	l.OnSwap(func(s *authsetup.Set) { offered = s.Names() })
	assert.Equal(t, []string{"local"}, offered)

	put(t, store, map[string]string{"auth.ldap.enabled": "true", "auth.ldap.url": "ldap://127.0.0.1:1", "auth.ldap.base_dn": "dc=example"})
	require.NoError(t, l.Reload(context.Background()))
	e, ok := l.Current().Entry("ldap")
	require.True(t, ok)
	assert.True(t, e.Live(), e.Err)
	assert.Equal(t, authsetup.OriginPage, e.Origin)
	assert.Equal(t, []string{"local", "ldap"}, offered)
	// ⚠ The page's directory goes AFTER the environment's `local`: the
	// administrator's password is judged before any network round trip.
	assert.Equal(t, "login-chain(local,ldap)", l.LoginName())
	assert.True(t, l.Current().HasDirectory(), "protocol_login defaults on, as in the environment")

	put(t, store, map[string]string{"auth.ldap.enabled": "false"})
	require.NoError(t, l.Reload(context.Background()))
	_, ok = l.Current().Entry("ldap")
	assert.False(t, ok)
	assert.Equal(t, []string{"local"}, offered)
	assert.Equal(t, "local", l.LoginName())
}

// The environment wins, and the page's rows under the same name are kept but
// not built.
func TestLive_TheEnvironmentWins(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	envCfg := map[string]any{"url": "ldap://env-dc:389", "base_dn": "dc=env", "bind_password": "env-secret"}
	d, err := authsetup.Build(context.Background(), store, "ldap", envCfg)
	require.NoError(t, err)
	l := live(t, store, nil, authsetup.NewEnvEntry("ldap", "FILEX_AUTH_DRIVERS", envCfg, d, nil, true))
	// Saved by this version (after the start's one-time import of older rows,
	// which would switch them off and prove nothing here).
	put(t, store, map[string]string{"auth.ldap.enabled": "true", "auth.ldap.url": "ldap://page-dc:389", "auth.ldap.base_dn": "dc=page"})
	require.NoError(t, l.Reload(context.Background()))

	var ldaps []authsetup.Entry
	for _, e := range l.Current().Entries {
		if e.Name == "ldap" {
			ldaps = append(ldaps, e)
		}
	}
	require.Len(t, ldaps, 1, "one LDAP, not two")
	assert.Equal(t, authsetup.OriginEnvironment, ldaps[0].Origin)
	assert.Same(t, d, ldaps[0].Driver)
	env, ok := l.EnvDefines("ldap")
	require.True(t, ok)
	assert.Equal(t, "ldap://env-dc:389", env.Display["url"])
	assert.NotContains(t, env.Display, "bind_password", "a secret is never displayed")
	assert.Equal(t, []string{"bind_password"}, env.Secrets)
}

// A page provider that cannot start is left out with its reason — the
// administrator's password sign-in is untouched and answers at once.
func TestLive_ABrokenPageProviderNeverTakesThePasswordSignInDown(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	email, pw := testutil.SeedAdmin(t, store)
	var logs bytes.Buffer
	l := live(t, store, &logs)
	sealed, err := box(t, testKey).Seal("S3cr3t-never-logged")
	require.NoError(t, err)
	put(t, store, map[string]string{
		"auth.oidc.enabled": "true", "auth.oidc.issuer": "http://127.0.0.1:1/realms/none",
		"auth.oidc.client_id": "filex", "auth.oidc.client_secret": sealed,
		"auth.ldap.enabled": "true", "auth.ldap.url": "ldap://127.0.0.1:1", "auth.ldap.base_dn": "dc=example",
	})
	require.NoError(t, l.Reload(context.Background()))

	oidc, ok := l.Current().Entry("oidc")
	require.True(t, ok)
	assert.False(t, oidc.Live(), "an unreachable issuer does not start")
	assert.NotEmpty(t, oidc.Err)

	start := time.Now()
	u, tok, err := l.Login().Login(context.Background(), email, pw)
	require.NoError(t, err, "the administrator's password still signs in")
	assert.Equal(t, email, u.Email)
	assert.NotEmpty(t, tok)
	assert.Less(t, time.Since(start), 2*time.Second, "judged before the unreachable directory")

	assert.NotContains(t, logs.String(), "S3cr3t-never-logged", "a secret is never logged")
}

// The ways an administrator can still sign in, for the "never switch off the
// last one" rule.
func TestLive_AdminPaths(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	id, _ := testutil.SeedAdminUser(t, store)
	l := live(t, store, nil)
	put(t, store, map[string]string{"auth.proxy-header.enabled": "true", "auth.proxy-header.trusted_proxies": "10.0.0.0/8"})
	require.NoError(t, l.Reload(context.Background()))
	assert.Equal(t, []string{"password"}, l.AdminPaths(context.Background(), "proxy-header"))
	assert.Equal(t, []string{"password", "proxy-header"}, l.AdminPaths(context.Background(), ""))

	// Administrators who came in through SSO have no password: the password
	// form is not a way in for them.
	require.NoError(t, store.UpdateUserPassword(context.Background(), id, ""))
	assert.Empty(t, l.AdminPaths(context.Background(), "proxy-header"))
	assert.Equal(t, []string{"proxy-header"}, l.AdminPaths(context.Background(), ""))
}

// With password sign-in off in the environment, the recovery sign-in is the
// bootstrap administrator's break-glass — and it counts as a way in.
func TestLive_RecoveryIsAWayInWhenPasswordSignInIsOff(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	id, _ := testutil.SeedAdminUser(t, store)
	put(t, store, map[string]string{"auth.bootstrap_admin_id": strconv.FormatInt(id, 10)})
	l, err := authsetup.New(context.Background(), authsetup.Options{Store: store, Box: box(t, testKey), RecoveryLogin: true}, nil)
	require.NoError(t, err)
	l.Start(context.Background())
	assert.True(t, l.Current().Recovery())
	assert.Equal(t, []string{"recovery"}, l.AdminPaths(context.Background(), ""))
	assert.True(t, l.Current().PasswordLogin(), "the recovery form answers")
	_, _, err = l.Login().Login(context.Background(), "nobody@example", "x")
	assert.Error(t, err, "and it accepts nobody but the bootstrap administrator")
}

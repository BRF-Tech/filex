package handlers_test

// Admin → Identity providers really manages sign-in (owner's decision,
// v0.43.0). Until then the page saved rows nothing read, and said "restart
// the server" for a restart that changed nothing. These walk the page's
// promises end to end over HTTP: an OIDC provider configured here signs a
// person in through a real (mock) issuer without a restart; a broken secret
// never takes the administrator's password sign-in down; an environment
// provider is read-only; a secret is sealed and never sent back; switching
// ON a provider whose test fails needs a confirmation that names the step;
// the last way in cannot be switched off; and every change is one audit row
// that names fields, never values.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

const liveTestKey = "handlers-live-auth-test-key"

// The OIDC issuer these tests sign in through is internal/testutil/fakeidp —
// the one fake IdP (it used to be a second copy here): discovery under a realm
// path, an authorize step that signs "ada@idp.example" in at once, and a token
// endpoint that knows one confidential client (WithClient), so a wrong secret
// is refused exactly as a real IdP refuses it.
func newAdaIssuer(t *testing.T, secret string) *fakeidp.IdP {
	t.Helper()
	idp := fakeidp.New(t, fakeidp.WithClient("filex", secret), fakeidp.WithIssuerPath("/realms/test"))
	idp.SignIn("ada@idp.example", "ada-1")
	return idp
}

// liveServer is the test server with a FILEX_SECRET_KEY, signed in as the
// administrator.
func liveServer(t *testing.T, mutate func(*api.Deps)) (*httptest.Server, *http.Client, db.Store, string, string) {
	t.Helper()
	srv, client, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.SecretKey = liveTestKey
	}, mutate)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return srv, client, store, email, pw
}

func patchProvider(t *testing.T, client *http.Client, base, name string, body map[string]any) (int, map[string]any) {
	t.Helper()
	status, raw := doReq(t, client, http.MethodPatch, base+"/api/admin/auth-providers/"+name, body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return status, out
}

func listProviders(t *testing.T, client *http.Client, base string) (string, map[string]map[string]any) {
	t.Helper()
	status, raw := doReq(t, client, http.MethodGet, base+"/api/admin/auth-providers", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var body struct {
		Providers []map[string]any `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	out := map[string]map[string]any{}
	for _, p := range body.Providers {
		out[p["name"].(string)] = p
	}
	return string(raw), out
}

func capsDrivers(t *testing.T, client *http.Client, base string) []any {
	t.Helper()
	status, raw := doReq(t, client, http.MethodGet, base+"/api/capabilities", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var caps map[string]any
	require.NoError(t, json.Unmarshal(raw, &caps))
	d, _ := caps["auth_drivers"].([]any)
	return d
}

// oidcSignIn walks the browser flow with a fresh cookie jar and reports
// whether it ended signed in.
func oidcSignIn(t *testing.T, base string) (bool, string) {
	t.Helper()
	_, ok, email := oidcBrowser(t, base)
	return ok, email
}

// oidcBrowser is oidcSignIn that keeps the browser — its cookie jar holds the
// session the sign-in made, for what the test does next (sign out).
func oidcBrowser(t *testing.T, base string) (*http.Client, bool, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 20 * time.Second,
		// A failed callback sends the browser to the login page on the
		// PUBLIC address (test.local here, which does not resolve): stop there.
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Host == "test.local" {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	resp, err := c.Get(base + "/api/auth/oidc/start")
	require.NoError(t, err)
	_ = resp.Body.Close()
	me, err := c.Get(base + "/api/auth/me")
	require.NoError(t, err)
	defer me.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(me.Body).Decode(&body)
	u, _ := body["user"].(map[string]any)
	email, _ := u["email"].(string)
	return c, me.StatusCode == http.StatusOK, email
}

// The owner's measurement, as a test: configure an OIDC provider on the
// page, sign in through it (no restart), break its secret, and the
// administrator's password sign-in still works.
func TestIdentityProviders_OIDCFromThePageSignsInWithoutARestart(t *testing.T) {
	idp := newAdaIssuer(t, "right-secret")
	srv, client, store, email, pw := liveServer(t, nil)

	cfg := map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "right-secret",
		"redirect_url": srv.URL + "/api/auth/oidc/callback",
	}
	status, body := patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": true, "config": cfg})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["test_ok"], "the real test ran and passed: %v", body["checks"])
	assert.Contains(t, capsDrivers(t, client, srv.URL), "oidc", "the login page offers it at once")

	ok, who := oidcSignIn(t, srv.URL)
	require.True(t, ok, "signed in through the provider the page configured")
	assert.Equal(t, "ada@idp.example", who)

	// The secret is sealed at rest and never comes back.
	stored, _ := store.GetSetting(context.Background(), "auth.oidc.client_secret")
	assert.True(t, secretbox.IsSealed(stored))
	raw, list := listProviders(t, client, srv.URL)
	assert.NotContains(t, raw, "right-secret")
	assert.Equal(t, map[string]any{"client_secret": true}, list["oidc"]["secrets_set"])
	assert.NotContains(t, list["oidc"]["config_redacted"], "client_secret", "not even sealed")
	assert.Equal(t, "running", list["oidc"]["state"])

	// Break the secret: the test fails, so it is not saved without a
	// confirmation that names the step.
	status, body = patchProvider(t, client, srv.URL, "oidc", map[string]any{"config": map[string]any{"client_secret": "wrong"}})
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "test_failed", body["error"])
	assert.Contains(t, body["failed"], "client")
	status, body = patchProvider(t, client, srv.URL, "oidc", map[string]any{"config": map[string]any{"client_secret": "wrong"}, "confirm_failed_test": true})
	require.Equal(t, http.StatusOK, status, "%v", body)

	ok, _ = oidcSignIn(t, srv.URL)
	assert.False(t, ok, "the broken secret no longer signs anyone in")

	// …and the administrator's password sign-in is untouched.
	jar, _ := cookiejar.New(nil)
	fresh := &http.Client{Jar: jar}
	testutil.LoginAs(t, srv, fresh, email, pw)
}

// Switching ON a provider whose test fails needs a confirmation naming what
// failed; a DISABLED provider saves with a failing test. Nothing is written
// by a refused save.
func TestIdentityProviders_EnablingAFailingProviderNeedsConfirmation(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, nil)
	cfg := map[string]any{"url": "ldap://127.0.0.1:1", "base_dn": "dc=example"}

	status, body := patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": true, "config": cfg})
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "test_failed", body["error"])
	assert.Contains(t, body["failed"], "connect")
	v, _ := store.GetSetting(context.Background(), "auth.ldap.url")
	assert.Empty(t, v, "a refused save writes nothing")

	status, body = patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": false, "config": cfg})
	require.Equal(t, http.StatusOK, status, "a disabled provider saves with a failing test: %v", body)
	assert.Equal(t, false, body["test_ok"])

	status, body = patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": true, "confirm_failed_test": true})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Contains(t, capsDrivers(t, client, srv.URL), "ldap", "applied without a restart")

	// One audit row per change, naming fields and switches — never a value.
	rows, _, err := store.ListAuditFiltered(context.Background(), nil, "auth_provider.update", nil, nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	last := rows[0].Entry
	assert.Equal(t, "ldap", last.TargetID)
	assert.Equal(t, true, last.Metadata["enabled_after"])
	assert.Equal(t, false, last.Metadata["enabled_before"])
	assert.Equal(t, true, last.Metadata["confirmed_failed_test"])
	assert.Contains(t, last.Metadata["test_failed"], "connect")
	first := rows[1].Entry
	assert.Equal(t, []any{"base_dn", "url"}, first.Metadata["changed_fields"])
}

// A secret with no FILEX_SECRET_KEY to seal it is refused, not stored in
// the clear.
func TestIdentityProviders_ASecretNeedsTheKey(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	status, body := patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": false,
		"config": map[string]any{"issuer": "https://idp.example", "client_id": "filex", "client_secret": "plain-would-leak"}})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	assert.Equal(t, "secret_key_required", body["error"])
	v, _ := store.GetSetting(context.Background(), "auth.oidc.client_secret")
	assert.Empty(t, v)
}

// A provider the environment defines shows read-only, says where it comes
// from, and cannot be changed here. Password sign-in is not the page's.
func TestIdentityProviders_TheEnvironmentsProvidersAreReadOnly(t *testing.T) {
	srv, client, store, _, _ := liveServer(t, func(d *api.Deps) {
		ctx := context.Background()
		box, _ := secretbox.New(liveTestKey)
		local, err := authsetup.Build(ctx, d.Store, "local", nil)
		require.NoError(t, err)
		envCfg := map[string]any{"url": "ldap://env-dc:389", "base_dn": "dc=env", "bind_password": "env-bind-secret"}
		ldap, err := authsetup.Build(ctx, d.Store, "ldap", envCfg)
		require.NoError(t, err)
		live, err := authsetup.New(ctx, authsetup.Options{Store: d.Store, Box: box, RecoveryLogin: true}, []authsetup.Entry{
			authsetup.NewEnvEntry("local", "FILEX_AUTH_DRIVERS", nil, local, nil, false),
			authsetup.NewEnvEntry("ldap", "FILEX_AUTH_DRIVERS", envCfg, ldap, nil, true),
		})
		require.NoError(t, err)
		live.Start(ctx)
		d.AuthLive, d.LocalAuth, d.OIDCAuth, d.Directory = live, live.Login(), live.OIDC(), live.Dir()
	})
	require.NoError(t, store.UpsertSetting(context.Background(), "auth.ldap.url", "ldap://page-dc:389"))

	raw, list := listProviders(t, client, srv.URL)
	ldap := list["ldap"]
	assert.Equal(t, "environment", ldap["origin"])
	assert.Equal(t, "FILEX_AUTH_DRIVERS", ldap["from"])
	assert.Equal(t, false, ldap["managed"])
	assert.Equal(t, true, ldap["shadowed"], "the page's own rows are there, and not used")
	assert.Equal(t, "ldap://env-dc:389", ldap["config_redacted"].(map[string]any)["url"])
	assert.NotContains(t, raw, "env-bind-secret")

	status, body := patchProvider(t, client, srv.URL, "ldap", map[string]any{"enabled": false})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "environment_managed", body["error"])
	assert.Equal(t, "FILEX_AUTH_DRIVERS", body["from"])

	// `local` is the environment's here too, and API tokens are nobody's.
	status, body = patchProvider(t, client, srv.URL, "local", map[string]any{"enabled": false})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "environment_managed", body["error"])
	status, body = patchProvider(t, client, srv.URL, "api-token", map[string]any{"enabled": false})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "not_managed_here", body["error"])
}

// The last way an administrator can sign in cannot be switched off here.
func TestIdentityProviders_TheLastWayInStaysOn(t *testing.T) {
	srv, client, store, email, _ := liveServer(t, nil)
	status, body := patchProvider(t, client, srv.URL, "proxy-header", map[string]any{"enabled": true,
		"confirm_failed_test": true, "config": map[string]any{"trusted_proxies": "10.0.0.0/8"}})
	require.Equal(t, http.StatusOK, status, "%v", body)

	// Every administrator came in through SSO: nobody can use the password
	// form, so the header provider is the only way in.
	u, err := store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserPassword(context.Background(), u.ID, ""))

	status, body = patchProvider(t, client, srv.URL, "proxy-header", map[string]any{"enabled": false})
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "last_sign_in_method", body["error"])
	assert.Contains(t, capsDrivers(t, client, srv.URL), "proxy-header", "still on")

	// With a password back there is another way in, and it may go.
	require.NoError(t, store.UpdateUserPassword(context.Background(), u.ID, "$2a$10$abcdefghijklmnopqrstuuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"))
	status, body = patchProvider(t, client, srv.URL, "proxy-header", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.NotContains(t, capsDrivers(t, client, srv.URL), "proxy-header")
}

// A tenant administrator neither reads nor changes the instance's sign-in.
func TestIdentityProviders_ATenantAdminIsRefused(t *testing.T) {
	srv, client, store := multiTenantServer(t)
	_, email, pw := seedTenant(t, store, "acme", "boss@acme.example", false)
	testutil.LoginAs(t, srv, client, email, pw)
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		u := srv.URL + "/api/admin/auth-providers"
		if m == http.MethodPatch {
			u += "/oidc"
		}
		status, raw := doReq(t, client, m, u, map[string]any{"enabled": true})
		assert.Equal(t, http.StatusForbidden, status, "%s %s", m, strings.TrimSpace(string(raw)))
	}
}

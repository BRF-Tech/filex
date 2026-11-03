package handlers_test

// The bot check on the sign-in and its admin page, through the real router
// (internal/captcha, task #294): the sign-in is refused before any password
// is compared, a tenant's address and a typed realm answer to the same
// instance-wide setting, the demo account is its own form, and the settings
// page never says the secret.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/captcha"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type captchaRouterEnv struct {
	srv      *httptest.Server
	store    db.Store
	v        *captcha.Verifier
	provider *fakeSiteverify
}

func newCaptchaRouterEnv(t *testing.T, cfgMutate func(*config.Config)) *captchaRouterEnv {
	t.Helper()
	e := &captchaRouterEnv{provider: &fakeSiteverify{status: 200, body: `{"success":true}`}}
	e.srv, _, e.store = testutil.NewTestServerWith(t, func(c *config.Config) {
		c.SecretKey = "captcha-router-test-secret-key"
		if cfgMutate != nil {
			cfgMutate(c)
		}
	}, func(d *api.Deps) {
		e.v = newTestCaptcha(d.Store, e.provider, nil)
		box, err := secretbox.New("captcha-router-test-secret-key")
		require.NoError(t, err)
		e.v.Settings.Box = box
		d.Captcha = e.v
	})
	return e
}

func (e *captchaRouterEnv) post(t *testing.T, host, path, cookie string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	if host != "" {
		req.Host = host
	}
	if cookie != "" {
		req.Header.Set("Authorization", "Bearer "+cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (e *captchaRouterEnv) send(t *testing.T, method, path, token string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestCaptchaLogin_RefusedBeforeThePasswordIsCompared(t *testing.T) {
	e := newCaptchaRouterEnv(t, nil)
	email, pass := testutil.SeedAdmin(t, e.store)

	st, out := e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass})
	require.Equal(t, http.StatusUnprocessableEntity, st, out)
	assert.Equal(t, "captcha_required", out["error"])
	assert.Equal(t, "missing_token", out["captcha"])
	assert.NotEmpty(t, out["message"])
	assert.Equal(t, int64(0), e.provider.calls.Load())

	st, out = e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass, "captcha_token": "0.router_token-1"})
	require.Equal(t, http.StatusOK, st, out)
	assert.NotEmpty(t, out["token"])

	// Spent: the next attempt needs a new one, the right password or not.
	st, out = e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass, "captcha_token": "0.router_token-1"})
	require.Equal(t, http.StatusUnprocessableEntity, st, out)
	assert.Equal(t, "captcha_expired", out["error"])
	assert.Equal(t, "replayed", out["captcha"])

	// A refusal from the provider is a 422 and never the fallback.
	e.provider.body = `{"success":false,"error-codes":["invalid-input-response"]}`
	st, out = e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": "wrong", "captcha_token": "0.router_token-2"})
	require.Equal(t, http.StatusUnprocessableEntity, st, out)
	assert.Equal(t, "invalid_token", out["captcha"])
}

func TestCaptchaLogin_AnOutageIsTheLocalFallbackAndTheSignInGoesOn(t *testing.T) {
	e := newCaptchaRouterEnv(t, nil)
	email, pass := testutil.SeedAdmin(t, e.store)
	e.provider.status, e.provider.body = 503, "unavailable"

	st, out := e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass, "captcha_token": "0.router_token-3"})
	require.Equal(t, http.StatusConflict, st, out)
	assert.Equal(t, "fallback", out["captcha"])
	raw, _ := json.Marshal(out["challenge"])
	var c captcha.Challenge
	require.NoError(t, json.Unmarshal(raw, &c))

	st, out = e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass, "captcha_local": captcha.EncodeSolution(c, captcha.Solve(c))})
	require.Equal(t, http.StatusOK, st, out)
}

func TestCaptchaLogin_ALocalSolutionWithoutAnOutageIsRefused(t *testing.T) {
	e := newCaptchaRouterEnv(t, nil)
	email, pass := testutil.SeedAdmin(t, e.store)
	ch, err := e.v.Local.Issue()
	require.NoError(t, err)
	st, out := e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass, "captcha_local": captcha.EncodeSolution(ch, captcha.Solve(ch))})
	require.Equal(t, http.StatusUnprocessableEntity, st, out)
	assert.Equal(t, "captcha_fallback_inactive", out["error"])
}

// A tenant's own address and a typed realm are signed in through the same
// door and answer to the same instance-wide setting: nobody gets around the
// platform's check by naming a tenant.
func TestCaptchaLogin_ATenantsAddressOrRealmAnswersToThePlatformSetting(t *testing.T) {
	e := newCaptchaRouterEnv(t, func(c *config.Config) { c.MultiTenant = true })
	ctx := context.Background()
	_, err := e.store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: "files.acme.test", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		host string
		body map[string]string
	}{
		"the tenant's address": {"files.acme.test", map[string]string{"email": "alex@acme.local", "password": "x"}},
		"a typed realm":        {"", map[string]string{"email": "alex", "password": "x", "realm": "acme"}},
		"a realm nobody has":   {"", map[string]string{"email": "alex", "password": "x", "realm": "nobody"}},
	} {
		st, out := e.post(t, tc.host, "/api/auth/login", "", tc.body)
		assert.Equal(t, http.StatusUnprocessableEntity, st, name)
		assert.Equal(t, "missing_token", out["captcha"], name)
	}
}

func TestCaptchaLogin_TheDemoAccountIsItsOwnForm(t *testing.T) {
	e := newCaptchaRouterEnv(t, func(c *config.Config) { c.Demo.Mode = true; c.Demo.User = "demo@demo.com" })
	e.v.Settings.Env = func(k string) string {
		return map[string]string{
			captcha.EnvProvider: "turnstile", captcha.EnvSiteKey: "s", captcha.EnvSecret: "k", captcha.EnvForms: "login",
		}[k]
	}
	st, out := e.post(t, "", "/api/auth/login", "", map[string]string{"email": "Demo@Demo.com", "password": "demo"})
	assert.NotEqual(t, http.StatusUnprocessableEntity, st, "demo_login is not protected: %v", out)
	st, out = e.post(t, "", "/api/auth/login", "", map[string]string{"email": "someone@else", "password": "x"})
	assert.Equal(t, http.StatusUnprocessableEntity, st, out)
}

func TestCaptchaAdmin_TheSecretGoesInAndNeverComesOut(t *testing.T) {
	e := newCaptchaRouterEnv(t, nil)
	email, pass := testutil.SeedAdmin(t, e.store)
	// The admin signs in through the check like anybody.
	_, out := e.post(t, "", "/api/auth/login", "", map[string]string{"email": email, "password": pass, "captcha_token": "0.router_token-admin"})
	token, _ := out["token"].(string)
	require.NotEmpty(t, token)

	st, body := e.send(t, http.MethodGet, "/api/admin/captcha", token, nil)
	require.Equal(t, http.StatusOK, st, body)
	assert.NotContains(t, body, "1x0000000000000000000000000000000AA")
	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &view))
	assert.Equal(t, "env", view["sources"].(map[string]any)["secret"])

	st, body = e.send(t, http.MethodPatch, "/api/admin/captcha", token, map[string]any{
		"provider": "recaptcha", "site_key": "db-site-key", "secret": "db-secret-value", "forms": []string{"login", "file_request"},
	})
	require.Equal(t, http.StatusOK, st, body)
	assert.NotContains(t, body, "db-secret-value", "the secret is never answered")
	require.NoError(t, json.Unmarshal([]byte(body), &view))
	eff := view["effective"].(map[string]any)
	assert.Equal(t, "recaptcha", eff["provider"])
	assert.Equal(t, true, eff["has_secret"])
	assert.Equal(t, "db", view["sources"].(map[string]any)["secret"])
	sealed, err := e.store.GetSetting(context.Background(), captcha.KeySecret)
	require.NoError(t, err)
	assert.True(t, secretbox.IsSealed(sealed))
	assert.False(t, strings.Contains(sealed, "db-secret-value"))

	st, body = e.send(t, http.MethodPatch, "/api/admin/captcha", token, map[string]any{"provider": "hcaptcha"})
	assert.Equal(t, http.StatusBadRequest, st, body)
	assert.Contains(t, body, "captcha_bad_value")

	// The test asks the provider and says what it found.
	e.provider.body = `{"success":false,"error-codes":["invalid-input-response"]}`
	st, body = e.send(t, http.MethodPost, "/api/admin/captcha/test", token, map[string]any{})
	require.Equal(t, http.StatusOK, st, body)
	assert.Contains(t, body, `"secret_ok":true`)
	assert.NotContains(t, body, "db-secret-value")
}

package handlers_test

// Multi-tenant auth handler behaviour: OIDC callback redirects must target
// the TENANT's host (not the operator PublicURL) and the session cookie
// Domain resolves per provider (explicit > derived from host > global).
// See docs/MULTI-TENANCY.md.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// fakeOIDC satisfies auth.OIDCDriver with canned results.
type fakeOIDC struct {
	user  *model.User
	token string
	err   error
}

func (f *fakeOIDC) StartFlow(http.ResponseWriter, *http.Request) error { return nil }
func (f *fakeOIDC) HandleCallback(http.ResponseWriter, *http.Request) (*model.User, string, error) {
	return f.user, f.token, f.err
}

func seedProvider(t *testing.T, store db.Store, p *model.Provider) *model.Provider {
	t.Helper()
	p.Enabled = true
	out, err := store.CreateProvider(context.Background(), p)
	require.NoError(t, err)
	return out
}

func callbackLocation(t *testing.T, a *handlers.Auth, host string, hdr map[string]string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=x&state=y", nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.OIDCCallback(rec, req)
	require.Equal(t, http.StatusFound, rec.Code)
	return rec.Header().Get("Location")
}

func TestOIDCCallback_MultiTenant_RedirectsToTenantHost(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	seedProvider(t, store, &model.Provider{Slug: "tenant-a", Host: "files.tenant-a.test", AuthType: model.AuthTypeOIDC})

	// Error path — back to the TENANT's login, not the operator's.
	a := handlers.NewAuth(store, nil, &fakeOIDC{err: errors.New("boom")}, "https://operator.test", true, "")
	loc := callbackLocation(t, a, "files.tenant-a.test", nil)
	assert.Equal(t, "https://files.tenant-a.test/admin/login?error=oidc", loc)

	// Success path — a 200 HTML bounce (not a 302, so a CDN can't strip the
	// Set-Cookie), the session cookie scoped to the tenant apex (derived from
	// the provider host), and a relative /admin/ target that stays on the
	// tenant host that served the callback.
	a = handlers.NewAuth(store, nil, &fakeOIDC{user: &model.User{ID: 1, Email: "u@tenant-a.test"}, token: "tkn"}, "https://operator.test", true, "")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=x&state=y", nil)
	req.Host = "files.tenant-a.test"
	rec := httptest.NewRecorder()
	a.OIDCCallback(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Location"), "success is a 200 bounce, not a redirect")
	assert.Contains(t, rec.Body.String(), "/admin/")
	assert.Contains(t, sessionSetCookie(t, rec.Result()), "Domain=tenant-a.test")
}

// Reproduces the reported "callback emits no Set-Cookie with cookie-domain"
// bug precisely: multi-tenant, provider with an EXPLICIT cookie_domain, and a
// realistic base64url OIDC session token (oidc.randString uses '-'/'_'). The
// callback MUST emit a Set-Cookie carrying that token + Domain — otherwise the
// browser has no session and loops.
func TestOIDCCallback_MultiTenant_ExplicitCookieDomain_EmitsSetCookie(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	// Explicit cookie_domain, on a multi-label public suffix like the live
	// tenant (.diyetlif.com.tr). Token mimics base64.RawURLEncoding output.
	seedProvider(t, store, &model.Provider{
		Slug: "dtl", Host: "files.tenant.example", CookieDomain: ".tenant.example",
		AuthType: model.AuthTypeOIDC,
	})
	tok := "aB3-cD_9xyZ012ABCdef-gh_"
	a := handlers.NewAuth(store, nil,
		&fakeOIDC{user: &model.User{ID: 1, Email: "u@tenant.example"}, token: tok},
		"https://operator.example", true, "")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=x&state=y", nil)
	req.Host = "files.tenant.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	a.OIDCCallback(rec, req)

	require.Equal(t, http.StatusOK, rec.Code) // 200 bounce, not 302 (CDN 3xx strip)
	assert.Contains(t, rec.Body.String(), "/admin/")
	sc := sessionSetCookie(t, rec.Result()) // fails loudly if no Set-Cookie
	assert.Contains(t, sc, "filex_session="+tok)
	assert.Contains(t, sc, "Domain=tenant.example")
	assert.Contains(t, sc, "Secure")
}

// Same as above but through the REAL router + middleware chain (not a direct
// handler call) — the layer the reported bug pointed at ("origin guard skips
// the cookie-set"). Confirms nothing in the public /api/auth middleware stack
// strips the callback's Set-Cookie in multi-tenant mode.
func TestRouter_OIDCCallback_MultiTenant_EmitsSetCookie(t *testing.T) {
	tok := "aB3-cD_9xyZ012ABCdef-gh_"
	fake := &fakeOIDC{user: &model.User{ID: 1, Email: "u@tenant.example"}, token: tok}
	srv, client, store := testutil.NewTestServerWith(t,
		func(c *config.Config) { c.MultiTenant = true },
		func(d *api.Deps) { d.OIDCAuth = fake },
	)
	seedProvider(t, store, &model.Provider{
		Slug: "dtl", Host: "files.tenant.example", CookieDomain: ".tenant.example",
		AuthType: model.AuthTypeOIDC,
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/oidc/callback?code=x&state=y", nil)
	req.Host = "files.tenant.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	// 200 HTML bounce carries the Set-Cookie past a CDN that strips it on 3xx.
	require.Equal(t, http.StatusOK, resp.StatusCode)
	sc := sessionSetCookie(t, resp)
	assert.Contains(t, sc, "filex_session="+tok)
	assert.Contains(t, sc, "Domain=tenant.example")
	assert.Contains(t, sc, "Secure")
}

func TestOIDCCallback_MultiTenant_UnknownHostFallsBackToPublicURL(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	a := handlers.NewAuth(store, nil, &fakeOIDC{err: errors.New("boom")}, "https://operator.test", true, "")
	loc := callbackLocation(t, a, "evil.example", nil)
	assert.Equal(t, "https://operator.test/admin/login?error=oidc", loc)
}

func TestOIDCCallback_SingleTenant_KeepsPublicURL(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	seedProvider(t, store, &model.Provider{Slug: "tenant-b", Host: "files.tenant-b.test", AuthType: model.AuthTypeOIDC})
	a := handlers.NewAuth(store, nil, &fakeOIDC{err: errors.New("boom")}, "https://operator.test", false, "")
	loc := callbackLocation(t, a, "files.tenant-b.test", nil)
	assert.Equal(t, "https://operator.test/admin/login?error=oidc", loc)
}

func TestOIDCCallback_MultiTenant_HonorsForwardedProtoHTTP(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	seedProvider(t, store, &model.Provider{Slug: "tenant-c", Host: "files.tenant-c.test", AuthType: model.AuthTypeOIDC})
	a := handlers.NewAuth(store, nil, &fakeOIDC{err: errors.New("boom")}, "https://operator.test", true, "")
	loc := callbackLocation(t, a, "files.tenant-c.test", map[string]string{"X-Forwarded-Proto": "http"})
	assert.Equal(t, "http://files.tenant-c.test/admin/login?error=oidc", loc)
}

// logoutClear drives Logout (public, always clears) and returns the raw
// Set-Cookie header — the clear must carry the SAME Domain as the set.
func logoutClear(t *testing.T, a *handlers.Auth, host string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	a.Logout(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return sessionSetCookie(t, rec.Result())
}

func TestCookieDomain_MultiTenant_Resolution(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	// Explicit cookie_domain always wins.
	seedProvider(t, store, &model.Provider{Slug: "exp", Host: "files.explicit.test", CookieDomain: ".override.test", AuthType: model.AuthTypeLocal})
	// No explicit value → derived from host (drop the first label).
	seedProvider(t, store, &model.Provider{Slug: "der", Host: "files.derived.com.test", AuthType: model.AuthTypeLocal})
	// Derivation impossible (remainder has no dot) → global fallback.
	seedProvider(t, store, &model.Provider{Slug: "loc", Host: "files.localhost", AuthType: model.AuthTypeLocal})

	a := handlers.NewAuth(store, nil, nil, "https://operator.test", true, ".global.test")

	assert.Contains(t, logoutClear(t, a, "files.explicit.test"), "Domain=override.test")
	assert.Contains(t, logoutClear(t, a, "files.derived.com.test"), "Domain=derived.com.test")
	assert.Contains(t, logoutClear(t, a, "files.localhost"), "Domain=global.test")
	// Host with no provider row → global too.
	assert.Contains(t, logoutClear(t, a, "stranger.test"), "Domain=global.test")
}

// Session cookie must be Secure on an HTTPS request — either r.TLS or, behind
// a TLS-terminating proxy, X-Forwarded-Proto=https — and must NOT be Secure on
// plain HTTP so TLS-less installs keep working.
func TestSessionCookie_SecureFollowsForwardedProto(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	body, _ := json.Marshal(map[string]string{"email": email, "password": pw})

	// httptest speaks plain HTTP with no XFP → no Secure.
	resp, err := client.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	assert.NotContains(t, sessionSetCookie(t, resp), "Secure")

	// Same login with X-Forwarded-Proto: https → Secure.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp2, err := client.Do(req)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Contains(t, sessionSetCookie(t, resp2), "Secure")
}

func TestCookieDomain_MultiTenant_NoValuesMeansHostOnly(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	seedProvider(t, store, &model.Provider{Slug: "bare", Host: "files.localhost", AuthType: model.AuthTypeLocal})
	a := handlers.NewAuth(store, nil, nil, "https://operator.test", true, "")
	clear := logoutClear(t, a, "files.localhost")
	assert.False(t, strings.Contains(clear, "Domain="), "expected host-only cookie, got %q", clear)
}

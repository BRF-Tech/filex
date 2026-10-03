package handlers_test

// Signing in to a tenant (docs/TENANT-ADMIN.md):
//
//   - the sign-in page asks a realm's methods; a realm nobody has answers like
//     a tenant with no SSO (no realm oracle);
//   - an OIDC is started for ONE tenant through ONE of its providers
//     (?instance=&realm=); the callback finds them in the flow, not in the
//     address, so the account is made in the realm's tenant even when the
//     tenant has no address of its own;
//   - a tenant WITH an address of its own whose person signed in elsewhere is
//     handed there (a one-use ticket in the URL fragment), as a password
//     sign-in is;
//   - which tenants a provider serves is the operator's, and a tenant's own
//     provider serves its tenant only.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// multiLiveServer is the multi-tenant test server with a FILEX_SECRET_KEY,
// signed in as the platform's administrator.
func multiLiveServer(t *testing.T) (string, *http.Client, db.Store) {
	t.Helper()
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.MultiTenant = true
		c.SecretKey = liveTestKey
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return srv.URL, client, store
}

// browser is a fresh cookie jar that does not follow a redirect off the test
// server (the public address test.local, a tenant's own address).
func browser(base string) *http.Client {
	jar, _ := cookiejar.New(nil)
	bu, _ := url.Parse(base)
	return &http.Client{Jar: jar, Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Host != bu.Host && !strings.HasPrefix(req.URL.Host, "127.0.0.1") {
				return http.ErrUseLastResponse
			}
			return nil
		}}
}

func methods(t *testing.T, c *http.Client, base, realm string) map[string]any {
	t.Helper()
	resp, err := c.Get(base + "/api/auth/methods?realm=" + url.QueryEscape(realm))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

var bounceTarget = regexp.MustCompile(`<a href="([^"]+)">`)

func TestTenantOIDC_HostlessStartHandsOffToTheTenantsAddress(t *testing.T) {
	base, _, store := multiLiveServer(t)
	ctx := context.Background()
	idp := fakeidp.New(t, fakeidp.WithClient("acme-files", "acme-secret"))
	idp.SignIn("ayse@acme.example", "ayse-1")
	acme, err := store.CreateProvider(ctx, &model.Provider{
		Slug: "acme", Name: "Acme", Enabled: true, Host: "files.acme.example", AuthType: model.AuthTypeOIDC,
		OIDCIssuer: idp.Issuer(), OIDCClientID: "acme-files", OIDCClientSecret: "acme-secret",
		// The callback comes back to the address the flow started on: here,
		// the platform's (the test server).
		OIDCRedirectURL: base + "/api/auth/oidc/callback",
	})
	require.NoError(t, err)

	// The realm's methods name its own SSO; a realm nobody has answers like a
	// tenant without one.
	b := browser(base)
	m := methods(t, b, base, "acme")
	sso, _ := m["sso"].([]any)
	require.Len(t, sso, 1, "%v", m)
	assert.Equal(t, "tenant", sso[0].(map[string]any)["id"])
	assert.Equal(t, "Acme", sso[0].(map[string]any)["label"])
	none := methods(t, b, base, "nobody")
	acmeNoSSO := map[string]any{"password": m["password"], "recovery": false, "sso": []any{}}
	assert.Equal(t, acmeNoSSO, none, "a realm nobody has must answer exactly like a tenant with no SSO")

	// Started on the platform's address with the realm.
	resp, err := b.Get(base + "/api/auth/oidc/start?instance=tenant&realm=acme")
	require.NoError(t, err)
	raw := string(readAll(t, resp))
	require.Equal(t, http.StatusOK, resp.StatusCode, raw)

	// The account was made in acme, by the realm the flow was started for.
	u, err := store.GetUserByEmail(ctx, "ayse@acme.example")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, acme.ID, *u.ProviderID, "the account belongs to the realm's tenant")

	// No session here: the bounce goes to acme's own sign-in page with the
	// ticket in the fragment.
	mm := bounceTarget.FindStringSubmatch(raw)
	require.Len(t, mm, 2, raw)
	target := html.UnescapeString(mm[1])
	assert.True(t, strings.HasPrefix(target, "http://files.acme.example/admin/login#handoff="), target)
	me, err := b.Get(base + "/api/auth/me")
	require.NoError(t, err)
	_ = me.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, me.StatusCode, "the session belongs on the tenant's address, not here")

	// Redeemed on acme's address: the session is opened there.
	code := strings.TrimPrefix(target[strings.Index(target, "#"):], "#handoff=")
	req, _ := http.NewRequest(http.MethodPost, base+"/api/auth/handoff", strings.NewReader(`{"code":"`+code+`"}`))
	req.Host = "files.acme.example"
	req.Header.Set("Content-Type", "application/json")
	hr, err := browser(base).Do(req)
	require.NoError(t, err)
	body := string(readAll(t, hr))
	require.Equal(t, http.StatusOK, hr.StatusCode, body)
	assert.Contains(t, body, "ayse@acme.example")

	// A start for an SSO the realm does not have goes back to the sign-in page.
	resp, err = browser(base).Get(base + "/api/auth/oidc/start?instance=nope&realm=acme")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "/admin/login?error=oidc")
}

func TestTenantOIDC_ASharedProviderSignsIntoTheRealmsTenant(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	idp := newAdaIssuer(t, "corp-secret")
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
	require.NoError(t, err)

	// Another OIDC, made by the operator, for beta only.
	status, raw := doReq(t, client, http.MethodPost, base+"/api/admin/auth-providers", map[string]any{
		"driver": "oidc", "slug": "corp-sso", "label": "Corp SSO", "enabled": true,
		"tenants": []int64{beta.ID},
		"config": map[string]any{"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "corp-secret",
			"redirect_url": base + "/api/auth/oidc/callback"},
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	_, list := listProviders(t, client, base)
	require.Contains(t, list, "corp-sso")
	assert.Equal(t, []any{float64(beta.ID)}, list["corp-sso"]["tenants"])
	assert.Equal(t, "oidc", list["corp-sso"]["driver"])

	// The platform's own page does not offer it; beta's realm does.
	b := browser(base)
	assert.Empty(t, methods(t, b, base, "")["sso"], "a provider bound to beta only is no button of the platform's")
	sso := methods(t, b, base, "beta")["sso"].([]any)
	require.Len(t, sso, 1)
	assert.Equal(t, map[string]any{"id": "corp-sso", "label": "Corp SSO"}, sso[0])

	// beta has no address of its own: the session is opened right here, and
	// the account is beta's.
	resp, err := b.Get(base + "/api/auth/oidc/start?instance=corp-sso&realm=beta")
	require.NoError(t, err)
	_ = readAll(t, resp)
	me, err := b.Get(base + "/api/auth/me")
	require.NoError(t, err)
	_ = me.Body.Close()
	assert.Equal(t, http.StatusOK, me.StatusCode)
	u, err := store.GetUserByEmail(ctx, "ada@idp.example")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, beta.ID, *u.ProviderID)

	// Started for the platform's own tenant it is refused: not one of its.
	resp, err = browser(base).Get(base + "/api/auth/oidc/start?instance=corp-sso")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Location"), "error=oidc")
}

func TestProviderBindings_TheOperatorsAndATenantsOwnServesItAlone(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
	require.NoError(t, err)
	plat, err := store.GetSupertenant(ctx)
	require.NoError(t, err)

	status, raw := doReq(t, client, http.MethodPost, base+"/api/admin/auth-providers", map[string]any{
		"driver": "ldap", "slug": "corp-dir", "enabled": false,
		"config": map[string]any{"url": "ldaps://dir.example", "base_dn": "dc=example"},
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	_, list := listProviders(t, client, base)
	assert.Equal(t, []any{float64(plat.ID)}, list["corp-dir"]["tenants"], "a provider made now serves the platform's own tenant only")

	status, raw = doReq(t, client, http.MethodPut, base+"/api/admin/auth-providers/corp-dir/tenants", map[string]any{"tenants": []int64{acme.ID, beta.ID}})
	require.Equal(t, http.StatusOK, status, string(raw))
	_, list = listProviders(t, client, base)
	assert.Equal(t, []any{float64(acme.ID), float64(beta.ID)}, list["corp-dir"]["tenants"])

	status, raw = doReq(t, client, http.MethodPut, base+"/api/admin/auth-providers/corp-dir/tenants", map[string]any{"tenants": []int64{999999}})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))

	// A tenant's own provider serves that tenant only, ever.
	owner := acme.ID
	own, err := store.CreateAuthInstance(ctx, &model.AuthInstance{Slug: "acme-oidc", Driver: "oidc", Origin: model.AuthOriginTenant, OwnerProviderID: &owner})
	require.NoError(t, err)
	require.NoError(t, store.BindAuthInstance(ctx, acme.ID, own.ID, model.AuthBindExplicit))
	status, raw = doReq(t, client, http.MethodPut, base+"/api/admin/auth-providers/acme-oidc/tenants", map[string]any{"tenants": []int64{acme.ID, beta.ID}})
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, string(raw), "tenant_owned")

	// The audit names the change.
	rows, err := store.ListAuditRecent(ctx, 100)
	require.NoError(t, err)
	var actions []string
	for _, r := range rows {
		actions = append(actions, r.Action)
	}
	assert.Contains(t, actions, "auth_provider.create")
	assert.Contains(t, actions, "auth_provider.tenants_set")
}

func TestProviderBindings_ASingleTenantInstallHasNone(t *testing.T) {
	srv, client, _, _, _ := liveServer(t, nil)
	status, raw := doReq(t, client, http.MethodPut, srv.URL+"/api/admin/auth-providers/ldap/tenants", map[string]any{"tenants": []int64{1}})
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, string(raw), "not_multi_tenant")
	_, list := listProviders(t, client, srv.URL)
	_, has := list["oidc"]["tenants"]
	assert.False(t, has, "no bindings are shown where there are none")
}

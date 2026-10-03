package handlers_test

// Which account an SSO sign-in opens (0.50, docs/SSO.md): the regressions of
// the 2026-10-02 review, end to end through the router.
//
// An OIDC callback used to find its account by the e-mail address alone - on
// the WHOLE platform when the driver was not pinned to a tenant, with
// `email_verified` never read and the kept `sub` never compared - and a
// callback that came back without its flow cookie was answered by the set's
// first OIDC. Measured on integ/050 @ f371915c:
//
//	1a  a platform-bound SSO signed a stranger in to an administrator of
//	    tenant beta by sending her address (email_verified false, another sub);
//	1b  the same for tenant gamma, which has an address of its own: the
//	    platform's SSO issued a handoff ticket for gamma's account;
//	2   tenant acme's OWN SSO, its callback sent without the flow cookie,
//	    signed in to the PLATFORM administrator.
//
// Every refusal of an account of another tenant is the generic answer, byte
// for byte the one of a realm nobody has. The reason codes are written out as
// strings (the wire contract with web/src/lib/ssoRefusal.ts), and the tests
// use only what the code before the fix has, so they run against it (red).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

const platformAdminEmail = "admin@test.local"

// person is a browser that follows no redirect, so every hop of the SSO round
// trip can be looked at, and whose cookies are kept for /api/auth/me after.
func person() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// ssoRun runs start → identity provider → callback in c. dropFlow sends the
// callback with the identity provider's state cookie only, as a browser that
// lost (or never had) the flow cookie would.
func ssoRun(t *testing.T, c *http.Client, base, start string, dropFlow bool) answer {
	t.Helper()
	toIdP := hop(t, c, base+start)
	require.Equal(t, http.StatusFound, toIdP.status, toIdP.body)
	back := hop(t, c, toIdP.location)
	require.Equal(t, http.StatusFound, back.status, back.body)
	cb, err := url.Parse(back.location)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(cb.Path, "/api/auth/oidc/callback"), back.location)
	if !dropFlow {
		return hop(t, c, cb.String())
	}
	req, err := http.NewRequest(http.MethodGet, cb.String(), nil)
	require.NoError(t, err)
	state := false
	for _, ck := range c.Jar.Cookies(cb) {
		if ck.Name == "filex_oidc_state" {
			req.AddCookie(ck)
			state = true
		}
	}
	require.True(t, state, "the identity provider's state cookie was set")
	jar, _ := cookiejar.New(nil)
	bare := &http.Client{Jar: jar, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := bare.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body := string(readAll(t, resp))
	// Whatever the callback set, the person's browser keeps it.
	c.Jar.SetCookies(cb, resp.Cookies())
	return answer{status: resp.StatusCode, location: resp.Header.Get("Location"), body: body}
}

// signedInAs is who /api/auth/me says the browser is, "" for nobody.
func signedInAs(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	resp, err := c.Get(base + "/api/auth/me")
	require.NoError(t, err)
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var me struct {
		Email string `json:"email"`
		User  struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	require.NoError(t, json.Unmarshal(raw, &me), string(raw))
	if me.Email != "" {
		return me.Email
	}
	return me.User.Email
}

// tenantAccount opens an account in a tenant.
func tenantAccount(t *testing.T, store db.Store, tenantID int64, email, role string) *model.User {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateUser(ctx, email, "", role, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, tenantID, ""))
	return u
}

// platformSSO is a shared OIDC ("corp-sso") bound to the platform's own
// tenant, signing people in through idp.
func platformSSO(t *testing.T, base string, client *http.Client, store db.Store, idp *fakeidp.IdP, config map[string]any) {
	t.Helper()
	super, err := store.GetSupertenant(context.Background())
	require.NoError(t, err)
	require.NotNil(t, super)
	cfg := map[string]any{"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "corp-secret",
		"redirect_url": base + "/api/auth/oidc/callback"}
	for k, v := range config {
		cfg[k] = v
	}
	status, raw := doReq(t, client, http.MethodPost, base+"/api/admin/auth-providers", map[string]any{
		"driver": "oidc", "slug": "corp-sso", "label": "Corp SSO", "enabled": true,
		"tenants": []int64{super.ID}, "config": cfg,
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
}

// genericAnswer is the answer to a start for a realm nobody has: what every
// refusal that may not say anything about a tenant must look like.
func genericAnswer(t *testing.T, base, instance string) answer {
	t.Helper()
	return hop(t, stepper(), base+"/api/auth/oidc/start?instance="+instance+"&realm=nobody-has-this")
}

// 1a: a platform-bound SSO sends the address of tenant beta's administrator,
// unverified and with another subject. Before 0.50 the account was found by
// the address across the platform and signed in to.
func TestOIDCTenantBoundary_1a_APlatformSSONeverOpensAnotherTenantsAccount(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
	require.NoError(t, err)
	victim := tenantAccount(t, store, beta.ID, "victim@beta.example", model.RoleAdmin)
	idp := fakeidp.New(t, fakeidp.WithClient("filex", "corp-secret"))
	platformSSO(t, base, client, store, idp, nil)
	logs := captureLogs(t)

	for _, verified := range []bool{false, true} {
		idp.SignIn("victim@beta.example", "attacker-1")
		idp.SetExtraClaims(map[string]any{"email_verified": verified})
		c := person()
		got := ssoRun(t, c, base, "/api/auth/oidc/start?instance=corp-sso", false)
		assert.Equal(t, "", signedInAs(t, c, base), "email_verified=%v: signed in to another tenant's account", verified)
		require.Equal(t, http.StatusFound, got.status, "email_verified=%v: %s", verified, got.body)
		assert.Equal(t, genericAnswer(t, base, "corp-sso"), got,
			"email_verified=%v: the refusal must be the generic answer, byte for byte", verified)
		assert.Empty(t, got.reason(t))
	}
	assert.Contains(t, logs(), "registered to another tenant", "the operator still reads why")

	again, err := store.GetUser(ctx, victim.ID)
	require.NoError(t, err)
	require.NotNil(t, again.ProviderID)
	assert.Equal(t, beta.ID, *again.ProviderID, "the account stays beta's")
	assert.True(t, again.Enabled)
}

// 1b: tenant gamma has an address of its own. The platform's SSO sending a
// gamma account's address used to end in a handoff ticket to gamma's page.
func TestOIDCTenantBoundary_1b_NoHandoffTicketForAnotherTenantsAccount(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	gamma, err := store.CreateProvider(ctx, &model.Provider{Slug: "gamma", Name: "Gamma", Enabled: true, Host: "files.gamma.example"})
	require.NoError(t, err)
	tenantAccount(t, store, gamma.ID, "boss@gamma.example", model.RoleAdmin)
	idp := fakeidp.New(t, fakeidp.WithClient("filex", "corp-secret"))
	platformSSO(t, base, client, store, idp, nil)

	idp.SignIn("boss@gamma.example", "attacker-2")
	c := person()
	got := ssoRun(t, c, base, "/api/auth/oidc/start?instance=corp-sso", false)
	assert.NotContains(t, got.body, "#handoff=", "a ticket for gamma's account was issued")
	assert.NotEqual(t, http.StatusOK, got.status, "signed in: %s", got.body)
	assert.Equal(t, genericAnswer(t, base, "corp-sso"), got)
	assert.Equal(t, "", signedInAs(t, c, base))
}

// 2: tenant acme's OWN OIDC. Its callback sent without the flow cookie used to
// be answered by the set's first OIDC - acme's, unpinned - which found the
// platform administrator by the address. With the flow cookie the tenant's
// driver refuses (an account of another tenant: the generic answer).
func TestOIDCTenantBoundary_2_ATenantsOwnSSOWithoutItsFlowOpensNothing(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	acmeID, acmeAdminEmail, acmePassword := seedTenant(t, store, "acme", "admin@acme.test", false)
	status, raw := doReq(t, client, http.MethodPut, base+"/api/admin/tenant/insecure?tenant="+itoa(acmeID), map[string]any{"allow": true})
	require.Equal(t, http.StatusOK, status, "the fake IdP is on 127.0.0.1: %s", raw)

	idp := fakeidp.New(t)
	acmeAdmin := browser(base)
	loginIn(t, base, acmeAdmin, "acme", acmeAdminEmail, acmePassword)
	status, raw = doReq(t, acmeAdmin, http.MethodPost, base+"/api/admin/tenant/auth-providers", map[string]any{
		"driver": "oidc", "label": "Acme SSO", "enabled": true, "confirm_failed_test": true,
		"config": map[string]any{"issuer": idp.Issuer(), "client_id": "acme-files", "client_secret": "acme-secret",
			"redirect_url": base + "/api/auth/oidc/callback"},
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	row, err := store.GetAuthInstanceBySlug(ctx, "acme-oidc")
	require.NoError(t, err)
	require.NotNil(t, row)
	start := "/api/auth/oidc/start?instance=acme-oidc&realm=acme"

	// The identity provider says it is the platform's administrator.
	idp.SignIn(platformAdminEmail, "acme-idp-evil")
	c := person()
	got := ssoRun(t, c, base, start, true)
	assert.Equal(t, "", signedInAs(t, c, base), "acme's identity provider signed in to the platform administrator")
	require.Equal(t, http.StatusFound, got.status, got.body)
	assert.Equal(t, "expired", got.reason(t), "a callback without its flow: start again (%s)", got.location)

	// With its flow: refused as an account of another tenant, generically.
	c = person()
	got = ssoRun(t, c, base, start, false)
	assert.Equal(t, "", signedInAs(t, c, base))
	assert.Equal(t, genericAnswer(t, base, "acme-oidc"), got)

	// A person of acme signs in through it, with or without anybody else.
	idp.SignIn("ayse@acme.example", "ayse-1")
	c = person()
	got = ssoRun(t, c, base, start, false)
	require.Equal(t, http.StatusOK, got.status, got.location)
	assert.Equal(t, "ayse@acme.example", signedInAs(t, c, base))
	ayse, err := store.GetUserByEmail(ctx, "ayse@acme.example")
	require.NoError(t, err)
	require.NotNil(t, ayse.ProviderID)
	assert.Equal(t, acmeID, *ayse.ProviderID)
}

// loginIn signs a browser in with a password in a realm.
func loginIn(t *testing.T, base string, c *http.Client, realm, email, password string) {
	t.Helper()
	status, raw := doReq(t, c, http.MethodPost, base+"/api/auth/login", map[string]any{"email": email, "password": password, "realm": realm})
	require.Equal(t, http.StatusOK, status, string(raw))
}

// The account is bound to the identity it first signed in with: the same
// address arriving with another identity is refused, and says so, until an
// administrator removes the bind.
func TestOIDCTenantBoundary_AnotherIdentityWithTheSameAddressIsRefused(t *testing.T) {
	base, client, store, idp, _ := ssoTenantSetup(t, nil)
	ctx := context.Background()

	c := person()
	got := ssoRun(t, c, base, betaStart, false)
	require.Equal(t, http.StatusOK, got.status, got.location)
	require.Equal(t, "ada@idp.example", signedInAs(t, c, base))

	idp.SignIn("ada@idp.example", "ada-2")
	c = person()
	got = ssoRun(t, c, base, betaStart, false)
	assert.Equal(t, "", signedInAs(t, c, base), "another identity signed in to ada's account")
	require.Equal(t, http.StatusFound, got.status, got.body)
	assert.Equal(t, "identity_mismatch", got.reason(t), got.location)

	ada, err := store.GetUserByEmail(ctx, "ada@idp.example")
	require.NoError(t, err)
	status, raw := doReq(t, client, http.MethodPatch, base+"/api/admin/users/"+itoa(ada.ID), map[string]any{"sso_unlink": true})
	require.Equal(t, http.StatusOK, status, string(raw))
	c = person()
	got = ssoRun(t, c, base, betaStart, false)
	assert.Equal(t, http.StatusOK, got.status, "the bind was removed: matched by the address again (%s)", got.location)
	assert.Equal(t, "ada@idp.example", signedInAs(t, c, base))

	// Bound again, to ada-2 now: ada-1 is the stranger.
	idp.SignIn("ada@idp.example", "ada-1")
	got = ssoRun(t, person(), base, betaStart, false)
	assert.Equal(t, "identity_mismatch", got.reason(t), got.location)
}

// A first sign-in whose address the identity provider does not say is
// verified (false, or no claim at all) opens the account SWITCHED OFF: the
// page says it waits for an administrator, the audit says why, and the
// administrator switching it on is the approval.
func TestOIDCTenantBoundary_AnUnverifiedAddressOpensAnAccountSwitchedOff(t *testing.T) {
	for _, tc := range []struct {
		name  string
		claim any
	}{{"false", false}, {"absent", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			base, client, store, idp, beta := ssoTenantSetup(t, nil)
			ctx := context.Background()
			idp.SetExtraClaims(map[string]any{"email_verified": tc.claim})

			c := person()
			got := ssoRun(t, c, base, betaStart, false)
			assert.Equal(t, "", signedInAs(t, c, base), "signed in with an unverified address")
			require.Equal(t, http.StatusFound, got.status, got.body)
			assert.Equal(t, "account_pending", got.reason(t), got.location)

			ada, err := store.GetUserByEmail(ctx, "ada@idp.example")
			require.NoError(t, err, "the account is opened")
			assert.False(t, ada.Enabled, "switched off")
			require.NotNil(t, ada.ProviderID)
			assert.Equal(t, beta.ID, *ada.ProviderID)
			status, raw := doReq(t, client, http.MethodGet, base+"/api/admin/users/"+itoa(ada.ID), nil)
			require.Equal(t, http.StatusOK, status, string(raw))
			assert.Contains(t, string(raw), `"disabled_reason":"pending_approval"`, "the users page says why it is off")
			rows, err := store.ListAuditRecent(ctx, 100)
			require.NoError(t, err)
			found := false
			for _, r := range rows {
				if r.Action == "auth.account_pending" {
					found = true
				}
			}
			assert.True(t, found, "the audit says an account waits for approval")

			// Its next sign-ins say the same, until an administrator approves it.
			got = ssoRun(t, person(), base, betaStart, false)
			assert.Equal(t, "account_pending", got.reason(t), got.location)
			status, raw = doReq(t, client, http.MethodPatch, base+"/api/admin/users/"+itoa(ada.ID), map[string]any{"enabled": true})
			require.Equal(t, http.StatusOK, status, string(raw))
			status, raw = doReq(t, client, http.MethodGet, base+"/api/admin/users/"+itoa(ada.ID), nil)
			require.Equal(t, http.StatusOK, status, string(raw))
			assert.NotContains(t, string(raw), "pending_approval", "approved: the reason is gone")
			c = person()
			got = ssoRun(t, c, base, betaStart, false)
			assert.Equal(t, http.StatusOK, got.status, got.location)
			assert.Equal(t, "ada@idp.example", signedInAs(t, c, base))
		})
	}
}

// An existing account that is bound to no SSO identity yet is not signed in
// to by an unverified address - unless the operator trusts the provider's
// addresses.
func TestOIDCTenantBoundary_AnUnboundAccountNeedsAVerifiedAddress(t *testing.T) {
	base, client, store, idp, beta := ssoTenantSetup(t, nil)
	ada := tenantAccount(t, store, beta.ID, "ada@idp.example", model.RoleUser)
	idp.SetExtraClaims(map[string]any{"email_verified": false})

	c := person()
	got := ssoRun(t, c, base, betaStart, false)
	assert.Equal(t, "", signedInAs(t, c, base), "an unverified address signed in to an unbound account")
	require.Equal(t, http.StatusFound, got.status, got.body)
	assert.Equal(t, "email_unverified", got.reason(t), got.location)
	again, err := store.GetUser(context.Background(), ada.ID)
	require.NoError(t, err)
	assert.True(t, again.Enabled, "the account is untouched")

	status, body := patchProvider(t, client, base, "corp-sso", map[string]any{"config": map[string]any{"trust_email": true}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	c = person()
	got = ssoRun(t, c, base, betaStart, false)
	assert.Equal(t, http.StatusOK, got.status, "a provider whose addresses are trusted: %s", got.location)
	assert.Equal(t, "ada@idp.example", signedInAs(t, c, base))
}

// A single-tenant install signs in as it always did: its one OIDC, started
// without an instance, no flow cookie; an existing account by its (verified)
// address, a new person with a new account.
func TestOIDCTenantBoundary_ASingleTenantInstallSignsInAsBefore(t *testing.T) {
	srv, admin, store, _, _ := liveServer(t, nil)
	base := srv.URL
	idp := fakeidp.New(t, fakeidp.WithClient("filex", "right-secret"))
	status, body := patchProvider(t, admin, base, "oidc", map[string]any{"enabled": true, "config": map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "right-secret",
		"redirect_url": base + "/api/auth/oidc/callback",
	}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "existing@corp.example", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	idp.SignIn("existing@corp.example", "existing-1")
	c := person()
	got := ssoRun(t, c, base, "/api/auth/oidc/start", false)
	require.Equal(t, http.StatusOK, got.status, got.location)
	assert.Equal(t, "existing@corp.example", signedInAs(t, c, base))
	for _, ck := range c.Jar.Cookies(mustURL(t, base)) {
		assert.NotEqual(t, "filex_oidc_flow", ck.Name, "a single-tenant start keeps no flow")
	}

	idp.SignIn("new@corp.example", "new-1")
	c = person()
	got = ssoRun(t, c, base, "/api/auth/oidc/start", false)
	require.Equal(t, http.StatusOK, got.status, got.location)
	assert.Equal(t, "new@corp.example", signedInAs(t, c, base))

	// The existing account signs in again by its identity.
	idp.SignIn("existing@corp.example", "existing-1")
	c = person()
	got = ssoRun(t, c, base, "/api/auth/oidc/start", false)
	require.Equal(t, http.StatusOK, got.status, got.location)
	assert.Equal(t, "existing@corp.example", signedInAs(t, c, base))
	again, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, again.Enabled)
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	require.NoError(t, err)
	return u
}

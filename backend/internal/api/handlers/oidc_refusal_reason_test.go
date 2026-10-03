package handlers_test

// What the sign-in page is told when an SSO sign-in is refused (0.50,
// docs/SSO.md "When an SSO sign-in is refused").
//
// Until 0.50 every refusal came back as `/admin/login?error=oidc`, and the
// page could only say "SSO sign-in failed": no account is opened at a first
// sign-in, the person is in no allowed group, the identity provider cancelled,
// the account is disabled - the reason was in the server log only. Now the
// redirect carries a reason CODE the page translates, and ONE refusal keeps
// the old answer byte for byte: an e-mail address with an account in another
// tenant (telling it would say that tenant exists and that this address is a
// member of it).
//
// The reason codes are written out as strings here, not taken from package
// auth: they are the wire contract with web/src/lib/ssoRefusal.ts, and the
// tests must compile against the code before them (red proof).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// answer is one HTTP answer as the browser sees it.
type answer struct {
	status   int
	location string
	body     string
}

// reason is the reason code a sign-in page address carries, "" for none.
func (a answer) reason(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(a.location)
	require.NoError(t, err, a.location)
	return u.Query().Get("reason")
}

// stepper is a browser that follows no redirect, so every hop of the SSO round
// trip can be looked at (and the callback address edited, as a cancelled
// sign-in or another tab would leave it).
func stepper() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func hop(t *testing.T, c *http.Client, target string) answer {
	t.Helper()
	resp, err := c.Get(target)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return answer{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(body)}
}

// ssoTrip runs start → identity provider → callback and returns the callback's
// answer. edit, when not nil, rewrites the callback address the identity
// provider sent the browser to.
func ssoTrip(t *testing.T, base, start string, edit func(q url.Values)) answer {
	t.Helper()
	c := stepper()
	toIdP := hop(t, c, base+start)
	require.Equal(t, http.StatusFound, toIdP.status, toIdP.body)
	back := hop(t, c, toIdP.location)
	require.Equal(t, http.StatusFound, back.status, back.body)
	cb, err := url.Parse(back.location)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(cb.Path, "/api/auth/oidc/callback"), back.location)
	if edit != nil {
		q := cb.Query()
		edit(q)
		cb.RawQuery = q.Encode()
	}
	return hop(t, c, cb.String())
}

// ssoTenantSetup is a multi-tenant server with tenant beta, signing in through
// a shared OIDC ("corp-sso") bound to beta with the given first-login rule.
func ssoTenantSetup(t *testing.T, config map[string]any) (string, *http.Client, db.Store, *fakeidp.IdP, *model.Provider) {
	t.Helper()
	base, client, store := multiLiveServer(t)
	idp := newAdaIssuer(t, "corp-secret")
	beta, err := store.CreateProvider(context.Background(), &model.Provider{Slug: "beta", Name: "Beta", Enabled: true})
	require.NoError(t, err)
	cfg := map[string]any{"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "corp-secret",
		"redirect_url": base + "/api/auth/oidc/callback", "role_claim": "groups"}
	for k, v := range config {
		cfg[k] = v
	}
	status, raw := doReq(t, client, http.MethodPost, base+"/api/admin/auth-providers", map[string]any{
		"driver": "oidc", "slug": "corp-sso", "label": "Corp SSO", "enabled": true,
		"tenants": []int64{beta.ID}, "config": cfg,
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	return base, client, store, idp, beta
}

const betaStart = "/api/auth/oidc/start?instance=corp-sso&realm=beta"

// A first sign-in refused by the provider's first-login rule: the page is told
// which rule, and nothing is created.
func TestOIDCRefusal_TheFirstLoginRuleIsNamedOnThePage(t *testing.T) {
	base, client, store, idp, _ := ssoTenantSetup(t, map[string]any{"auto_create": false})
	ctx := context.Background()

	got := ssoTrip(t, base, betaStart, nil)
	require.Equal(t, http.StatusFound, got.status, got.body)
	assert.Contains(t, got.location, "/admin/login?error=oidc")
	assert.Equal(t, "auto_create_off", got.reason(t), got.location)
	_, gerr := store.GetUserByEmail(ctx, "ada@idp.example")
	assert.Error(t, gerr, "no account may be opened")

	status, raw := patchProvider(t, client, base, "corp-sso", map[string]any{
		"config": map[string]any{"auto_create": true, "allowed_groups": "staff"},
	})
	require.Equal(t, http.StatusOK, status, "%v", raw)
	idp.SetExtraClaims(map[string]any{"groups": []any{"interns"}})
	got = ssoTrip(t, base, betaStart, nil)
	assert.Equal(t, "group_not_allowed", got.reason(t), got.location)
	_, gerr = store.GetUserByEmail(ctx, "ada@idp.example")
	assert.Error(t, gerr, "no account may be opened")

	// In the group: signed in (the 200 bounce), so the rule is what refused.
	idp.SetExtraClaims(map[string]any{"groups": []any{"staff"}})
	got = ssoTrip(t, base, betaStart, nil)
	assert.Equal(t, http.StatusOK, got.status, got.location)
}

// What the identity provider answered is turned into a code; its own words
// never reach the page.
func TestOIDCRefusal_TheIdentityProvidersAnswerIsACodeNotItsWords(t *testing.T) {
	base, _, _, idp, _ := ssoTenantSetup(t, nil)
	logs := captureLogs(t)

	// The person cancelled at the identity provider (RFC 6749 4.1.2.1).
	got := ssoTrip(t, base, betaStart, func(q url.Values) {
		q.Del("code")
		q.Set("error", "access_denied")
		q.Set("error_description", "<b>ldap bind as uid=ada failed</b>")
	})
	assert.Equal(t, "idp_denied", got.reason(t), got.location)
	for _, s := range []string{got.location, got.body} {
		assert.NotContains(t, s, "ldap bind")
		assert.NotContains(t, s, "access_denied")
	}
	assert.Contains(t, logs(), "access_denied", "the operator still reads what the IdP said")

	// A code the identity provider never issued: the exchange fails.
	got = ssoTrip(t, base, betaStart, func(q url.Values) { q.Set("code", "never-issued") })
	assert.Equal(t, "idp_error", got.reason(t), got.location)

	// Back with another sign-in's state (another tab, a stale page).
	got = ssoTrip(t, base, betaStart, func(q url.Values) { q.Set("state", "another-tab") })
	assert.Equal(t, "expired", got.reason(t), got.location)

	// No e-mail address in the answer: there is nothing to find an account by.
	idp.SignIn("", "nobody-1")
	got = ssoTrip(t, base, betaStart, nil)
	assert.Equal(t, "no_email", got.reason(t), got.location)
}

// ⚠⚠ The one refusal that stays generic: the address has an account in ANOTHER
// tenant. Its answer is the answer of a sign-in that names no tenant at all -
// same status, same address, same body - so it says nothing about acme. The
// log keeps the reason.
func TestOIDCRefusal_AnAccountInAnotherTenantGetsTheGenericAnswer(t *testing.T) {
	base, _, store, _, _ := ssoTenantSetup(t, nil)
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Enabled: true})
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, "ada@idp.example", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, acme.ID, ""))
	logs := captureLogs(t)

	other := ssoTrip(t, base, betaStart, nil)
	require.Equal(t, http.StatusFound, other.status, other.body)
	assert.True(t, strings.HasSuffix(other.location, "/admin/login?error=oidc"), other.location)
	assert.Empty(t, other.reason(t))
	assert.Contains(t, logs(), "registered to another tenant", "the operator still reads why")

	// The baselines: a realm nobody has, and an SSO the realm does not have.
	for _, start := range []string{
		"/api/auth/oidc/start?instance=corp-sso&realm=nobody-has-this",
		"/api/auth/oidc/start?instance=corp-sso&realm=acme",
	} {
		generic := hop(t, stepper(), base+start)
		assert.Equal(t, generic, other, "%s must answer exactly like an account in another tenant", start)
	}

	// Nothing moved: the account is still acme's, none was made in beta.
	again, err := store.GetUserByEmail(ctx, "ada@idp.example")
	require.NoError(t, err)
	require.NotNil(t, again.ProviderID)
	assert.Equal(t, acme.ID, *again.ProviderID)
}

// An account that may not open a session (handlers.OIDCCallback): the page is
// told which gate said no. It used to land on a page that said nothing.
func TestOIDCRefusal_TheGateThatRefusedTheSessionIsNamed(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	gone, err := store.CreateProvider(ctx, &model.Provider{Slug: "gone", Name: "Gone", Enabled: false})
	require.NoError(t, err)
	live, err := store.CreateProvider(ctx, &model.Provider{Slug: "live", Name: "Live", Enabled: true})
	require.NoError(t, err)

	cases := []struct {
		name        string
		user        *model.User
		multiTenant bool
		want        string
	}{
		{"disabled account", &model.User{ID: 7, Email: "off@x.test", Enabled: false}, true, "/admin/login?error=oidc&reason=account_disabled"},
		{"suspended tenant", &model.User{ID: 8, Email: "s@x.test", Enabled: true, ProviderID: &gone.ID}, true, "/admin/login?maintenance=1&reason=tenant_suspended"},
		{"maintenance mode", &model.User{ID: 9, Email: "m@x.test", Enabled: true, ProviderID: &live.ID}, false, "/admin/login?maintenance=1&reason=maintenance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := handlers.NewAuth(store, nil, &fakeOIDC{user: tc.user, token: "tkn"}, "https://files.test", tc.multiTenant, "")
			assert.Equal(t, "https://files.test"+tc.want, callbackLocation(t, a, "files.test", nil))
		})
	}
}

// failingStart is an OIDC whose start fails for a reason of its own.
type failingStart struct {
	fakeOIDC
	err error
}

func (f *failingStart) StartFlow(http.ResponseWriter, *http.Request) error { return f.err }

// A start that fails goes back to the sign-in page like a failed callback - a
// browser navigation must not dead-end on a JSON body carrying the error's
// words (it used to be a 500 with them).
func TestOIDCRefusal_AFailedStartGoesBackToTheSignInPage(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	a := handlers.NewAuth(store, nil, &failingStart{err: errors.New("oidc: discover provider: dial tcp 10.0.0.9:443: refused")}, "https://files.test", false, "")
	rec := httptest.NewRecorder()
	a.OIDCStart(rec, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil))
	assert.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	assert.Equal(t, "https://files.test/admin/login?error=oidc", rec.Header().Get("Location"))
	assert.NotContains(t, rec.Body.String(), "10.0.0.9")
}

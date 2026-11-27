package handlers_test

// MCP OAuth on a multi-tenant install (task #374): every address is an
// authorization server of its own - the platform's public URL, and each
// address a tenant owns - and a client, a code, a grant and an access token
// are bound to the address they were made at. A sign-in handed to the
// account's tenant (the realm handoff) carries the waiting authorization
// with it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const (
	mtoPlatform = "files.example.test"
	mtoAcme     = "acme.example.test"
	mtoBeta     = "beta.example.test"
	mtoPass     = "TenantPass!1"
)

type mtoFixture struct {
	srv         *httptest.Server
	store       db.Store
	acme, beta  *model.Provider
	acmeUser    *model.User
	betaUser    *model.User
	platformUsr *model.User
}

func mtoServer(t *testing.T) *mtoFixture {
	t.Helper()
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.MultiTenant = true
		c.PublicURL = "https://" + mtoPlatform
	})
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: mtoAcme, AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", Host: mtoBeta, AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	super, err := store.GetSupertenant(ctx)
	require.NoError(t, err)
	mk := func(email string, p *model.Provider) *model.User {
		hash, err := authlocal.HashPassword(mtoPass)
		require.NoError(t, err)
		u, err := store.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		if p != nil {
			require.NoError(t, store.SetUserProvider(ctx, u.ID, p.ID, ""))
		}
		u, err = store.GetUser(ctx, u.ID)
		require.NoError(t, err)
		return u
	}
	return &mtoFixture{srv: srv, store: store, acme: acme, beta: beta,
		acmeUser: mk("ada@acme.local", acme), betaUser: mk("bo@beta.local", beta), platformUsr: mk("pat@local", super)}
}

// mtoBrowser is one browser on one address, every request sent to the test
// server with that address as its Host. It keeps its cookies by name: a
// cookie jar would file them under the test server's 127.0.0.1 and drop the
// session's Domain cookie (a tenant's cookie domain, .example.test).
type mtoBrowser struct {
	t       *testing.T
	f       *mtoFixture
	host    string
	c       *http.Client
	cookies map[string]string
}

func (f *mtoFixture) browser(t *testing.T, host string) *mtoBrowser {
	return &mtoBrowser{t: t, f: f, host: host, cookies: map[string]string{}, c: &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *mtoBrowser) do(method, path, ctype, body string, header ...string) (*http.Response, string) {
	b.t.Helper()
	req, err := http.NewRequest(method, b.f.srv.URL+path, strings.NewReader(body))
	require.NoError(b.t, err)
	req.Host = b.host
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	for name, value := range b.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	res, err := b.c.Do(req)
	require.NoError(b.t, err)
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(raw)
}

func (b *mtoBrowser) json(method, path string, v any) (int, map[string]any) {
	b.t.Helper()
	raw, _ := json.Marshal(v)
	res, body := b.do(method, path, "application/json", string(raw))
	out := map[string]any{}
	_ = json.Unmarshal([]byte(body), &out)
	return res.StatusCode, out
}

func (b *mtoBrowser) signIn(email, realm string) map[string]any {
	b.t.Helper()
	body := map[string]any{"email": email, "password": mtoPass}
	if realm != "" {
		body["realm"] = realm
	}
	status, out := b.json("POST", "/api/auth/login", body)
	require.Equal(b.t, http.StatusOK, status, "%v", out)
	return out
}

func (b *mtoBrowser) register() string {
	b.t.Helper()
	status, out := b.json("POST", "/api/oauth/register", map[string]any{"client_name": "Tenant test client",
		"redirect_uris": []string{oauthCallback}, "token_endpoint_auth_method": "none"})
	require.Equal(b.t, http.StatusCreated, status, "%v", out)
	return out["client_id"].(string)
}

func mtoResource(host string) string { return "https://" + host + "/api/ai/mcp" }

func (b *mtoBrowser) authorize(clientID, issuerHost string) (*http.Response, string) {
	q := oauthAuthorizeQuery(clientID, oauthCallback, "read mcp")
	q.Set("resource", mtoResource(issuerHost))
	return b.do("GET", "/api/oauth/authorize?"+q.Encode(), "", "")
}

var mtoHidden = func(body, name string) string {
	m := regexp.MustCompile(`name="` + name + `" value="([a-f0-9]+)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// approve answers a consent page this browser holds and returns the code
// and the issuer the callback was given.
func (b *mtoBrowser) approve(page string) (string, string) {
	b.t.Helper()
	form := url.Values{"request": {mtoHidden(page, "request")}, "csrf": {mtoHidden(page, "csrf")}, "decision": {"approve"}}
	res, body := b.do("POST", "/api/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	require.Equal(b.t, http.StatusSeeOther, res.StatusCode, body)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(b.t, err)
	require.Equal(b.t, "s", loc.Query().Get("state"))
	return loc.Query().Get("code"), loc.Query().Get("iss")
}

func (b *mtoBrowser) exchange(clientID, code, issuerHost string) (int, map[string]any) {
	b.t.Helper()
	v := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {oauthVerifier},
		"redirect_uri": {oauthCallback}, "resource": {mtoResource(issuerHost)}}
	res, body := b.do("POST", "/api/oauth/token", "application/x-www-form-urlencoded", v.Encode())
	out := map[string]any{}
	_ = json.Unmarshal([]byte(body), &out)
	return res.StatusCode, out
}

// mcp asks this address's MCP endpoint with token: 200 when filex let the
// token in. The test server listens on a loopback address, so this is also
// the MCP SDK's DNS-rebinding guard's case (TestMCPBehindALoopbackProxy).
func (b *mtoBrowser) mcp(token string) int {
	b.t.Helper()
	res, _ := b.do("POST", "/api/ai/mcp", "application/json", toolsList, "Authorization", "Bearer "+token, "Accept", "application/json, text/event-stream")
	return res.StatusCode
}

// grant runs one whole authorization at host, signed in there as email, and
// returns the client and the token answer.
func (f *mtoFixture) grant(t *testing.T, host, email string) (string, map[string]any) {
	t.Helper()
	b := f.browser(t, host)
	b.signIn(email, "")
	id := b.register()
	res, page := b.authorize(id, host)
	require.Equal(t, http.StatusOK, res.StatusCode, page)
	code, iss := b.approve(page)
	require.Equal(t, "https://"+host, iss)
	status, tok := b.exchange(id, code, host)
	require.Equal(t, http.StatusOK, status, "%v", tok)
	return id, tok
}

func TestMCPOAuthMultiTenant_EveryAddressIsItsOwnIssuer(t *testing.T) {
	f := mtoServer(t)
	for _, host := range []string{mtoPlatform, mtoAcme, mtoBeta} {
		b := f.browser(t, host)
		res, body := b.do("GET", "/.well-known/oauth-protected-resource/api/ai/mcp", "", "")
		require.Equal(t, 200, res.StatusCode, body)
		var prm map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &prm))
		require.Equal(t, mtoResource(host), prm["resource"])
		require.Equal(t, []any{"https://" + host}, prm["authorization_servers"])
		res, body = b.do("GET", "/.well-known/oauth-authorization-server", "", "")
		require.Equal(t, 200, res.StatusCode, body)
		var as map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &as))
		require.Equal(t, "https://"+host, as["issuer"])
		require.Equal(t, "https://"+host+"/api/oauth/token", as["token_endpoint"])
		res, _ = b.do("POST", "/api/ai/mcp", "application/json", toolsList)
		require.Equal(t, 401, res.StatusCode)
		require.Contains(t, res.Header.Get("WWW-Authenticate"), `resource_metadata="https://`+host+`/.well-known/oauth-protected-resource/api/ai/mcp"`)
	}
	// An address nobody provisioned is no issuer: a forged Host never
	// becomes one.
	res, _ := f.browser(t, "evil.example.test").do("GET", "/.well-known/oauth-authorization-server", "", "")
	require.Equal(t, 404, res.StatusCode)
}

func TestMCPOAuthMultiTenant_ATokenOpensOnlyTheAddressItWasIssuedAt(t *testing.T) {
	f := mtoServer(t)
	id, tok := f.grant(t, mtoAcme, f.acmeUser.Email)
	access := tok["access_token"].(string)
	require.Equal(t, 200, f.browser(t, mtoAcme).mcp(access))
	require.Equal(t, 401, f.browser(t, mtoBeta).mcp(access), "another tenant's address")
	require.Equal(t, 401, f.browser(t, mtoPlatform).mcp(access), "the platform's address")
	// The grant refreshes at its own address only; elsewhere its client is
	// unknown.
	for _, host := range []string{mtoBeta, mtoPlatform} {
		res, body := f.browser(t, host).do("POST", "/api/oauth/token", "application/x-www-form-urlencoded",
			url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {tok["refresh_token"].(string)}}.Encode())
		require.Equal(t, 401, res.StatusCode, "%s: %s", host, body)
	}
	res, body := f.browser(t, mtoAcme).do("POST", "/api/oauth/token", "application/x-www-form-urlencoded",
		url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {tok["refresh_token"].(string)}}.Encode())
	require.Equal(t, 200, res.StatusCode, body)
}

func TestMCPOAuthMultiTenant_AClientRegisteredAtOneAddressIsUnknownAtAnother(t *testing.T) {
	f := mtoServer(t)
	id := f.browser(t, mtoAcme).register()
	beta := f.browser(t, mtoBeta)
	beta.signIn(f.betaUser.Email, "")
	res, body := beta.authorize(id, mtoBeta)
	require.Equal(t, 400, res.StatusCode, body)
	require.Empty(t, res.Header.Get("Location"))
	// Nor does a code issued at one address redeem at another.
	acme := f.browser(t, mtoAcme)
	acme.signIn(f.acmeUser.Email, "")
	res, page := acme.authorize(id, mtoAcme)
	require.Equal(t, 200, res.StatusCode, page)
	code, _ := acme.approve(page)
	status, out := f.browser(t, mtoBeta).exchange(id, code, mtoAcme)
	require.Equal(t, 401, status, "%v", out)
}

// A session of another tenant's account on a tenant's address (a cookie
// shared over a parent domain, provider cookie_domain) does not authorize
// there: the page names the account's own MCP address.
func TestMCPOAuthMultiTenant_AnotherTenantsAccountIsSentToItsOwnAddress(t *testing.T) {
	f := mtoServer(t)
	beta := f.browser(t, mtoBeta)
	beta.signIn(f.betaUser.Email, "")
	acme := f.browser(t, mtoAcme)
	acme.cookies[authlocal.SessionCookieName] = beta.cookies[authlocal.SessionCookieName]
	require.NotEmpty(t, acme.cookies[authlocal.SessionCookieName])
	id := acme.register()
	res, body := acme.authorize(id, mtoAcme)
	require.Equal(t, http.StatusForbidden, res.StatusCode, body)
	require.Contains(t, body, mtoResource(mtoBeta))
	tokens, err := f.store.ListAPITokensByUser(context.Background(), f.betaUser.ID)
	require.NoError(t, err)
	require.Empty(t, tokens)
}

func TestMCPOAuthMultiTenant_ASuspendedTenantsTokensStop(t *testing.T) {
	f := mtoServer(t)
	_, tok := f.grant(t, mtoAcme, f.acmeUser.Email)
	access := tok["access_token"].(string)
	require.Equal(t, 200, f.browser(t, mtoAcme).mcp(access))
	f.acme.Enabled = false
	require.NoError(t, f.store.UpdateProvider(context.Background(), f.acme))
	require.Equal(t, 401, f.browser(t, mtoAcme).mcp(access))
	res, _ := f.browser(t, mtoAcme).do("POST", "/api/oauth/token", "application/x-www-form-urlencoded",
		url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}}.Encode())
	require.NotEqual(t, 200, res.StatusCode)
}

// An address that passes to another tenant (an own domain moved) stops
// opening the grants made there by the tenant it left.
func TestMCPOAuthMultiTenant_AnAddressThatChangesHandsDropsItsGrants(t *testing.T) {
	f := mtoServer(t)
	_, tok := f.grant(t, mtoAcme, f.acmeUser.Email)
	access := tok["access_token"].(string)
	ctx := context.Background()
	f.acme.Host = "acme-new.example.test"
	require.NoError(t, f.store.UpdateProvider(ctx, f.acme))
	f.beta.Host = mtoAcme
	require.NoError(t, f.store.UpdateProvider(ctx, f.beta))
	require.Equal(t, 401, f.browser(t, mtoAcme).mcp(access))
}

// The realm handoff (docs/MULTI-TENANCY.md, Realms): an MCP client pointed at
// the platform's address, a person who signs in there with their tenant's
// realm and whose session is opened on the tenant's own address. The ticket
// carries the waiting authorization; the person approves it on the tenant's
// address, and the code goes back from the issuer the client asked - the
// platform's - so the token opens the platform's MCP endpoint.
func TestMCPOAuthMultiTenant_TheRealmHandoffCarriesTheAuthorization(t *testing.T) {
	f := mtoServer(t)
	platform := f.browser(t, mtoPlatform)
	id := platform.register()
	res, body := platform.authorize(id, mtoPlatform)
	require.Equal(t, http.StatusSeeOther, res.StatusCode, body)
	require.Contains(t, res.Header.Get("Location"), "/admin/login?redirect=")
	out := platform.signIn(f.acmeUser.Email, "acme")
	handoff, ok := out["handoff"].(map[string]any)
	require.True(t, ok, "the sign-in is handed to acme's address: %v", out)
	require.Equal(t, "https://"+mtoAcme, handoff["origin"])

	acme := f.browser(t, mtoAcme)
	status, redeemed := acme.json("POST", "/api/auth/handoff", map[string]any{"code": handoff["code"]})
	require.Equal(t, http.StatusOK, status, "%v", redeemed)
	resume, _ := redeemed["resume"].(string)
	require.Regexp(t, regexp.MustCompile(`^/api/oauth/authorize\?request=[0-9a-f]{64}$`), resume)
	res, page := acme.do("GET", resume, "", "")
	require.Equal(t, http.StatusOK, res.StatusCode, page)
	require.Contains(t, page, mtoPlatform, "the page names the address the app connects to")
	code, iss := acme.approve(page)
	require.Equal(t, "https://"+mtoPlatform, iss)

	// The code is the platform's: it redeems there and nowhere else.
	status, tok := f.browser(t, mtoAcme).exchange(id, code, mtoPlatform)
	require.Equal(t, 401, status, "the client is the platform's: %v", tok)
	status, tok = platform.exchange(id, code, mtoPlatform)
	require.Equal(t, http.StatusOK, status, "%v", tok)
	require.Equal(t, 200, platform.mcp(tok["access_token"].(string)))
	require.Equal(t, 401, f.browser(t, mtoAcme).mcp(tok["access_token"].(string)))

	// The request moved: the nonce the platform's address knew is spent.
	resumeOld := regexp.MustCompile(`request%3D([0-9a-f]{64})`).FindStringSubmatch(res.Header.Get("Location"))
	if len(resumeOld) == 2 {
		r2, _ := platform.do("GET", "/api/oauth/authorize?request="+resumeOld[1], "", "")
		require.NotEqual(t, http.StatusOK, r2.StatusCode)
	}
}

// A ticket carries only a request its own browser was signing in for: a
// handoff redeemed by a browser that never started one resumes nothing.
func TestMCPOAuthMultiTenant_AHandoffWithoutAnAuthorizationResumesNothing(t *testing.T) {
	f := mtoServer(t)
	platform := f.browser(t, mtoPlatform)
	out := platform.signIn(f.acmeUser.Email, "acme")
	handoff := out["handoff"].(map[string]any)
	status, redeemed := f.browser(t, mtoAcme).json("POST", "/api/auth/handoff", map[string]any{"code": handoff["code"]})
	require.Equal(t, http.StatusOK, status, "%v", redeemed)
	_, has := redeemed["resume"]
	require.False(t, has, "%v", redeemed)
}

package handlers_test

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
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/stretchr/testify/require"
)

const oauthPublic = "https://files.example.test"
const oauthResource = oauthPublic + "/api/ai/mcp"
const oauthCallback = "https://client.example.test/callback"

// RFC 7636's independently specified S256 example.
const oauthVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
const oauthChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

func oauthRequest(t *testing.T, client *http.Client, method, target, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	require.NoError(t, err)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := client.Do(req)
	require.NoError(t, err)
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	return res, string(b)
}

func oauthRegister(t *testing.T, server string) string {
	t.Helper()
	res, err := http.Post(server+"/api/oauth/register", "application/json", strings.NewReader(`{"client_name":"Test MCP client","redirect_uris":["`+oauthCallback+`"],"token_endpoint_auth_method":"none"}`))
	require.NoError(t, err)
	defer res.Body.Close()
	var data map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&data))
	require.Equal(t, http.StatusCreated, res.StatusCode, "%v", data)
	require.Equal(t, "none", data["token_endpoint_auth_method"])
	return data["client_id"].(string)
}

func TestMCPOAuthDiscovery(t *testing.T) {
	srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	res, body := oauthRequest(t, client, "GET", srv.URL+"/.well-known/oauth-protected-resource/api/ai/mcp", "")
	require.Equal(t, 200, res.StatusCode, body)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &metadata))
	require.Equal(t, oauthResource, metadata["resource"])
	require.Equal(t, []any{oauthPublic}, metadata["authorization_servers"])
	res, body = oauthRequest(t, client, "GET", srv.URL+"/.well-known/oauth-authorization-server", "")
	require.Equal(t, 200, res.StatusCode, body)
	require.NoError(t, json.Unmarshal([]byte(body), &metadata))
	require.Equal(t, oauthPublic+"/api/oauth/token", metadata["token_endpoint"])
	require.Equal(t, []any{"S256"}, metadata["code_challenge_methods_supported"])
	res, _ = oauthRequest(t, client, "POST", srv.URL+"/api/ai/mcp", `{}`)
	require.Equal(t, 401, res.StatusCode)
	require.Contains(t, res.Header.Get("WWW-Authenticate"), `resource_metadata="`+oauthPublic+`/.well-known/oauth-protected-resource/api/ai/mcp"`)
}

func TestMCPOAuthRejectsUnregisteredCallbackAndMissingPKCE(t *testing.T) {
	srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	id := oauthRegister(t, srv.URL)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	for _, query := range []url.Values{
		{"client_id": {id}, "redirect_uri": {"https://attacker.example/callback"}, "response_type": {"code"}, "code_challenge": {oauthChallenge}, "code_challenge_method": {"S256"}},
		{"client_id": {id}, "redirect_uri": {oauthCallback}, "response_type": {"code"}},
	} {
		res, body := oauthRequest(t, client, "GET", srv.URL+"/api/oauth/authorize?"+query.Encode(), "")
		require.Equal(t, 400, res.StatusCode, body)
		require.Empty(t, res.Header.Get("Location"))
	}
}

func oauthCode(t *testing.T, server, clientID string, browser *http.Client, scope string) string {
	t.Helper()
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	q := url.Values{"client_id": {clientID}, "redirect_uri": {oauthCallback}, "response_type": {"code"}, "code_challenge": {oauthChallenge}, "code_challenge_method": {"S256"}, "resource": {oauthResource}, "state": {"client-state"}, "scope": {scope}}
	res, body := oauthRequest(t, browser, "GET", server+"/api/oauth/authorize?"+q.Encode(), "")
	require.Equal(t, 200, res.StatusCode, body)
	hidden := func(name string) string {
		m := regexp.MustCompile(`name="` + name + `" value="([a-zA-Z0-9_-]+)"`).FindStringSubmatch(body)
		require.Len(t, m, 2, body)
		return m[1]
	}
	form := url.Values{"request": {hidden("request")}, "csrf": {hidden("csrf")}, "decision": {"approve"}}
	res, body = oauthRequest(t, browser, "POST", server+"/api/oauth/authorize", form.Encode())
	require.Equal(t, 303, res.StatusCode, body)
	target, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "client-state", target.Query().Get("state"))
	require.NotEmpty(t, target.Query().Get("code"))
	require.Equal(t, oauthPublic, target.Query().Get("iss"))
	return target.Query().Get("code")
}

func TestMCPOAuthCodeIsBoundToClientRedirectAndResource(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "admin", "oauth-bindings@example.test")
	id := oauthRegister(t, srv.URL)
	other := oauthRegister(t, srv.URL)
	for _, field := range []string{"client_id", "redirect_uri", "resource"} {
		code := oauthCode(t, srv.URL, id, browser, "read mcp")
		values := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {code}, "code_verifier": {oauthVerifier}, "redirect_uri": {oauthCallback}, "resource": {oauthResource}}
		if field == "client_id" {
			values.Set(field, other)
		} else {
			values.Set(field, "https://other.example.test/")
		}
		res, body := oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", values.Encode())
		require.Equal(t, 400, res.StatusCode, field+": "+body)
	}
	tokens, err := store.ListAPITokensByUser(context.Background(), uid)
	require.NoError(t, err)
	require.Empty(t, tokens)
}

func TestMCPOAuthExpiredCodeAndLegacyAPIKey(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "admin", "oauth-expiry@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	ctx := context.Background()
	key := apitoken.HashToken(code)
	payload, err := store.TakeMCPOAuthRecord(ctx, "code", key, time.Now().Unix())
	require.NoError(t, err)
	require.NoError(t, store.CreateMCPOAuthRecord(ctx, "code", key, payload, time.Now().Add(-time.Minute).Unix()))
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 400, res.StatusCode, "%v", data)
	legacy := testutil.NewAPIToken(t, store, uid, "read,mcp")
	res, body := oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", legacy, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, 200, res.StatusCode, body)
	res, body = oauthBearer(t, "GET", srv.URL+"/api/ai/root", legacy, "")
	require.Equal(t, 200, res.StatusCode, body)
}

func TestMCPOAuthConfidentialClientAuthentication(t *testing.T) {
	for _, method := range []string{"client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
			browser, _, _ := desktopSession(t, srv.URL, store, "admin", "oauth-secret@example.test")
			body, _ := json.Marshal(map[string]any{"client_name": "Confidential client", "redirect_uris": []string{oauthCallback}, "token_endpoint_auth_method": method})
			res, err := http.Post(srv.URL+"/api/oauth/register", "application/json", strings.NewReader(string(body)))
			require.NoError(t, err)
			var registered map[string]any
			require.NoError(t, json.NewDecoder(res.Body).Decode(&registered))
			res.Body.Close()
			require.Equal(t, 201, res.StatusCode)
			id := registered["client_id"].(string)
			secret := registered["client_secret"].(string)
			code := oauthCode(t, srv.URL, id, browser, "read mcp")
			for _, password := range []string{"wrong-secret", secret} {
				values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {oauthVerifier}, "redirect_uri": {oauthCallback}}
				if method == "client_secret_post" {
					values.Set("client_id", id)
					values.Set("client_secret", password)
				}
				req, err := http.NewRequest("POST", srv.URL+"/api/oauth/token", strings.NewReader(values.Encode()))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if method == "client_secret_basic" {
					req.SetBasicAuth(id, password)
				}
				res, err = http.DefaultClient.Do(req)
				require.NoError(t, err)
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				want := 200
				if password == "wrong-secret" {
					want = 401
				}
				require.Equal(t, want, res.StatusCode, string(b))
			}
		})
	}
}

func TestMCPOAuthCodeConcurrentExchangeIsSingleUse(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "admin", "oauth-concurrency@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { res, _ := oauthExchange(t, srv.URL, id, code, oauthVerifier); statuses <- res.StatusCode }()
	}
	a, b := <-statuses, <-statuses
	require.ElementsMatch(t, []int{200, 400}, []int{a, b})
	tokens, err := store.ListAPITokensByUser(context.Background(), uid)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
}

func TestMCPOAuthBrowserConsentChecksActualSession(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	_, uid, _ := desktopSession(t, srv.URL, store, "admin", "oauth-cookie@example.test")
	id := oauthRegister(t, srv.URL)
	u, err := store.GetUser(context.Background(), uid)
	require.NoError(t, err)
	q := url.Values{"client_id": {id}, "redirect_uri": {oauthCallback}, "response_type": {"code"}, "code_challenge": {oauthChallenge}, "code_challenge_method": {"S256"}}
	req := httptest.NewRequest("GET", oauthPublic+"/api/oauth/authorize?"+q.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: "filex_session", Value: "not-a-live-session"})
	req = req.WithContext(auth.WithUser(req.Context(), u))
	rec := httptest.NewRecorder()
	handlers.NewMCPOAuth(store, acl.New(store), oauthPublic, false).Authorize(rec, req)
	require.Equal(t, 303, rec.Code, "An identity plus an arbitrary cookie must never produce consent")
	require.Contains(t, rec.Header().Get("Location"), "/admin/login")
}

func TestMCPOAuthDisabledAccountCannotUseOrRefresh(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "user", "oauth-disabled@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	require.NoError(t, store.SetUserEnabled(context.Background(), uid, false))
	res, body := oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", data["access_token"].(string), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, 403, res.StatusCode, body)
	res, _ = oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {data["refresh_token"].(string)}}.Encode())
	require.Equal(t, 400, res.StatusCode)
}

func oauthExchange(t *testing.T, server, id, code, verifier string) (*http.Response, map[string]any) {
	t.Helper()
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {oauthCallback}, "resource": {oauthResource}}
	res, body := oauthRequest(t, http.DefaultClient, "POST", server+"/api/oauth/token", values.Encode())
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &data), body)
	return res, data
}

func oauthBearer(t *testing.T, method, target, token, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	return res, string(b)
}

func TestMCPOAuthCodeRefreshAndResourceBinding(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "admin", "oauth@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	require.Equal(t, "Bearer", data["token_type"])
	require.Equal(t, float64(3600), data["expires_in"])
	token := data["access_token"].(string)
	refresh := data["refresh_token"].(string)
	res, body := oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, 200, res.StatusCode, body)
	require.Contains(t, body, `"tools"`)
	for _, path := range []string{"/api/ai/root", "/api/auth/me", "/api/admin/users"} {
		res, body = oauthBearer(t, "GET", srv.URL+path, token, "")
		require.GreaterOrEqual(t, res.StatusCode, 400, "OAuth token escaped resource at %s: %s", path, body)
	}
	res, data = oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 400, res.StatusCode, "replayed code: %v", data)
	res, body = oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {refresh}, "resource": {oauthResource}}.Encode())
	require.Equal(t, 200, res.StatusCode, body)
	require.NoError(t, json.Unmarshal([]byte(body), &data))
	require.NotEqual(t, refresh, data["refresh_token"])
	// Revoking the visible filex API token also prevents OAuth refresh.
	tokens, err := store.ListAPITokensByUser(context.Background(), uid)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	require.NoError(t, store.DeleteAPIToken(context.Background(), tokens[0].ID))
	res, _ = oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {data["refresh_token"].(string)}}.Encode())
	require.Equal(t, 400, res.StatusCode, "revoked grant")
}

func TestMCPOAuthRefreshReplayRevokesActiveGrant(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "admin", "oauth-replay@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode)
	old := data["refresh_token"].(string)
	res, body := oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {old}}.Encode())
	require.Equal(t, 200, res.StatusCode, body)
	require.NoError(t, json.Unmarshal([]byte(body), &data))
	active := data["access_token"].(string)
	current := data["refresh_token"].(string)
	res, _ = oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {old}}.Encode())
	require.Equal(t, 400, res.StatusCode)
	res, _ = oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", active, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, 401, res.StatusCode, "refresh reuse must revoke the stolen active authorization")
	res, _ = oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {current}}.Encode())
	require.Equal(t, 400, res.StatusCode)
}

func TestMCPOAuthRefreshRevocation(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "admin", "oauth-revoke@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode)
	token := data["access_token"].(string)
	refresh := data["refresh_token"].(string)
	res, body := oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/revoke", url.Values{"client_id": {id}, "token": {refresh}}.Encode())
	require.Equal(t, 200, res.StatusCode, body)
	res, _ = oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, 401, res.StatusCode)
	res, _ = oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {refresh}}.Encode())
	require.Equal(t, 400, res.StatusCode)
}

func TestMCPOAuthDiscoveryUnderBasePath(t *testing.T) {
	srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic + "/filex"; c.BasePath = "/filex" })
	res, body := oauthRequest(t, client, "GET", srv.URL+"/.well-known/oauth-protected-resource/filex/api/ai/mcp", "")
	require.Equal(t, 200, res.StatusCode, body)
	require.Contains(t, body, oauthPublic+"/filex/api/ai/mcp")
	res, body = oauthRequest(t, client, "GET", srv.URL+"/.well-known/oauth-authorization-server/filex", "")
	require.Equal(t, 200, res.StatusCode, body)
	require.Contains(t, body, oauthPublic+"/filex/api/oauth/authorize")
	res, _ = oauthRequest(t, client, "POST", srv.URL+"/filex/api/ai/mcp", `{}`)
	require.Equal(t, 401, res.StatusCode)
	require.Contains(t, res.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/filex/api/ai/mcp")
}

func TestMCPOAuthConsentRejectsMissingCSRFAndViewerWrites(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "viewer", "oauth-viewer@example.test")
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	id := oauthRegister(t, srv.URL)
	q := url.Values{"client_id": {id}, "redirect_uri": {oauthCallback}, "response_type": {"code"}, "code_challenge": {oauthChallenge}, "code_challenge_method": {"S256"}, "scope": {"write mcp"}}
	res, body := oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+q.Encode(), "")
	require.Equal(t, 403, res.StatusCode, body)
	q.Set("scope", "read mcp")
	res, body = oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+q.Encode(), "")
	require.Equal(t, 200, res.StatusCode, body)
	m := regexp.MustCompile(`name="request" value="([a-f0-9]+)"`).FindStringSubmatch(body)
	require.Len(t, m, 2)
	res, _ = oauthRequest(t, browser, "POST", srv.URL+"/api/oauth/authorize", url.Values{"request": {m[1]}, "decision": {"approve"}}.Encode())
	require.Equal(t, 403, res.StatusCode)
	tokens, err := store.ListAPITokens(context.Background())
	require.NoError(t, err)
	require.Empty(t, tokens)
}

func TestMCPOAuthWrongVerifierDoesNotMintToken(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "user", "oauth-pkce@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, strings.Repeat("a", 43))
	require.Equal(t, 400, res.StatusCode, "%v", data)
	tokens, err := store.ListAPITokensByUser(context.Background(), uid)
	require.NoError(t, err)
	require.Empty(t, tokens)
}

func TestMCPOAuthRejectsInsecureRedirects(t *testing.T) {
	srv, _, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	for _, callback := range []string{"http://attacker.example/callback", "https://user:pass@client.example/callback", "https://client.example/callback#fragment", "javascript:alert(1)"} {
		raw, _ := json.Marshal(map[string]any{"redirect_uris": []string{callback}})
		res, err := http.Post(srv.URL+"/api/oauth/register", "application/json", strings.NewReader(string(raw)))
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, 400, res.StatusCode, callback)
	}
}

func TestMCPOAuthRegistrationIsThrottled(t *testing.T) {
	srv, _, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	for i := 0; i < 20; i++ {
		oauthRegister(t, srv.URL)
	}
	res, err := http.Post(srv.URL+"/api/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+oauthCallback+`"]}`))
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, 429, res.StatusCode)
	require.NotEmpty(t, res.Header.Get("Retry-After"))
}

func TestMCPOAuthMultiTenantIsExplicitlyUnavailable(t *testing.T) {
	srv, _, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic; c.MultiTenant = true })
	res, err := http.Post(srv.URL+"/api/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+oauthCallback+`"]}`))
	require.NoError(t, err)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, 503, res.StatusCode, string(b))
	require.Contains(t, string(b), "single-tenant")
}

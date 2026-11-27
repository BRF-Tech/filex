package handlers_test

// The MCP OAuth server's review (PR #141, task #374): each test names a rule
// the review found the contributor's code breaking, and holds it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func oauthRegisterWith(t *testing.T, server string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := http.Post(server+"/api/oauth/register", "application/json", strings.NewReader(string(raw)))
	require.NoError(t, err)
	defer res.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(res.Body).Decode(&data)
	return res.StatusCode, data
}

func oauthAuthorizeQuery(id, redirect, scope string) url.Values {
	return url.Values{"client_id": {id}, "redirect_uri": {redirect}, "response_type": {"code"}, "code_challenge": {oauthChallenge},
		"code_challenge_method": {"S256"}, "resource": {oauthResource}, "state": {"s"}, "scope": {scope}}
}

func oauthRefresh(t *testing.T, server, id, refresh string) (*http.Response, map[string]any) {
	t.Helper()
	res, body := oauthRequest(t, http.DefaultClient, "POST", server+"/api/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {id}, "refresh_token": {refresh}}.Encode())
	var data map[string]any
	_ = json.Unmarshal([]byte(body), &data)
	return res, data
}

const toolsList = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

// A native client listens on a port it picks when it starts (RFC 8252
// section 7.3, OAuth 2.1): a loopback callback matches whatever its port,
// localhost is one (the MCP specification), and nothing else is loosened -
// not the path, not an https callback's port.
func TestMCPOAuthLoopbackCallbackMayChangeItsPort(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-loopback@example.test")
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	status, data := oauthRegisterWith(t, srv.URL, map[string]any{"client_name": "Native", "token_endpoint_auth_method": "none",
		"redirect_uris": []string{"http://127.0.0.1:4444/callback", "http://localhost:5555/oauth/cb", oauthCallback}})
	require.Equal(t, 201, status, "%v", data)
	id := data["client_id"].(string)
	for redirect, want := range map[string]int{
		"http://127.0.0.1:39999/callback":                  200,
		"http://localhost:61000/oauth/cb":                  200,
		"http://127.0.0.1:4444/other":                      400,
		"http://localhost.evil.example:5555/oauth/cb":      400,
		"https://client.example.test:8443/callback":        400,
		"http://127.0.0.1:39999/callback?extra=1":          400,
		"https://client.example.test/callback/../callback": 400,
	} {
		res, body := oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+oauthAuthorizeQuery(id, redirect, "read mcp").Encode(), "")
		require.Equal(t, want, res.StatusCode, "%s: %s", redirect, body)
	}
}

// The stored form of a key's list joins entries with commas, and a list may
// hold permission levels (comments:rw) besides the verbs: a scope with a
// comma in it would carry an entry the allow-list never saw.
func TestMCPOAuthScopeCannotSmuggleEntries(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-scope@example.test")
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	id := oauthRegister(t, srv.URL)
	for _, scope := range []string{"read,comments:rw mcp", "root:a://x,write mcp", "admin mcp", "root:a://x root:b://y mcp", "comments:rw"} {
		res, body := oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+oauthAuthorizeQuery(id, oauthCallback, scope).Encode(), "")
		require.Equal(t, 400, res.StatusCode, "%s: %s", scope, body)
	}
}

// A code that comes back after it was used revokes what it made (RFC 6749
// section 4.1.2): whoever replays it holds a code that was intercepted.
func TestMCPOAuthReplayedCodeRevokesItsGrant(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "user", "oauth-code-replay@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	res, body := oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", data["access_token"].(string), toolsList)
	require.Equal(t, 200, res.StatusCode, body)
	res, again := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 400, res.StatusCode, "%v", again)
	res, _ = oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", data["access_token"].(string), toolsList)
	require.Equal(t, 401, res.StatusCode, "the replay revoked the access token")
	res, _ = oauthRefresh(t, srv.URL, id, data["refresh_token"].(string))
	require.Equal(t, 400, res.StatusCode, "and the grant")
	tokens, err := store.ListAPITokensByUser(context.Background(), uid)
	require.NoError(t, err)
	require.Empty(t, tokens)
}

// A grant is one record however often it is refreshed. The contributor's
// version kept every spent refresh token for the grant's 30 days: an hourly
// refresh filled the 10000-record table with a dozen connections and then
// every token request failed.
func TestMCPOAuthRefreshesDoNotPileUpRecords(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-pile@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		res, data = oauthRefresh(t, srv.URL, id, data["refresh_token"].(string))
		require.Equal(t, 200, res.StatusCode, "refresh %d: %v", i, data)
	}
	// Fill what is left of the table: the room the grant leaves is all but
	// the handful of records one connection holds (its client, its grant, the
	// spent code, nothing per refresh). Rolled back afterwards.
	room := 0
	full := errors.New("full")
	_ = store.WithTx(ctx, func(ctx context.Context) error {
		for ; room < 10001; room++ {
			if err := store.CreateMCPOAuthRecord(ctx, "consent", fmt.Sprint("fill-", room), "{}", time.Now().Add(time.Hour).Unix()); err != nil {
				return full
			}
		}
		return full
	})
	require.GreaterOrEqual(t, room, 10000-3, "one refreshed grant holds a few records, not one per refresh")
	res, body := oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", data["access_token"].(string), toolsList)
	require.Equal(t, 200, res.StatusCode, body)
}

// A registration nobody uses is forgotten within the hour; one that obtained
// a token is kept for 90 days after its last authorization.
func TestMCPOAuthUnusedRegistrationIsForgotten(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	ctx := context.Background()
	unused := oauthRegister(t, srv.URL)
	_, err := store.GetMCPOAuthRecord(ctx, "client", unused, time.Now().Add(2*time.Hour).Unix())
	require.ErrorIs(t, err, sql.ErrNoRows, "an unused registration outlived its hour")
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-kept@example.test")
	used := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, used, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, used, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	_, err = store.GetMCPOAuthRecord(ctx, "client_used", used, time.Now().Add(60*24*time.Hour).Unix())
	require.NoError(t, err, "a used registration is kept")
	_, err = store.GetMCPOAuthRecord(ctx, "client", used, time.Now().Unix())
	require.ErrorIs(t, err, sql.ErrNoRows, "and no longer counts as an anonymous one")
	res, data = oauthRefresh(t, srv.URL, used, data["refresh_token"].(string))
	require.Equal(t, 200, res.StatusCode, "%v", data)
}

// The consent form posts to filex and is answered with a redirect to the
// client: a browser holds that redirect to the page's form-action, so the
// callback's origin is named beside 'self' (Chromium refused it with
// form-action 'self' alone). The page is never framed.
func TestMCPOAuthConsentPageLetsItsFormReachTheCallback(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-csp@example.test")
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	id := oauthRegister(t, srv.URL)
	res, body := oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+oauthAuthorizeQuery(id, oauthCallback, "read mcp").Encode(), "")
	require.Equal(t, 200, res.StatusCode, body)
	csp := res.Header.Get("Content-Security-Policy")
	require.Contains(t, csp, "form-action 'self' https://client.example.test", csp)
	require.Contains(t, csp, "frame-ancestors 'none'")
	require.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
	require.Contains(t, body, "client.example.test", "the page says where the browser goes back to")
	require.Contains(t, body, "files.example.test", "and which filex the app connects to")
}

// The pages the browser shows are the server's sentences, in the reader's
// language.
func TestMCPOAuthPagesSpeakTheReadersLanguage(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, uid, _ := desktopSession(t, srv.URL, store, "user", "oauth-tr@example.test")
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	require.NoError(t, store.UpdateUserLocale(context.Background(), uid, "tr", "UTC"))
	id := oauthRegister(t, srv.URL)
	res, body := oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+oauthAuthorizeQuery(id, oauthCallback, "read mcp").Encode(), "")
	require.Equal(t, 200, res.StatusCode, body)
	require.Contains(t, body, `lang="tr"`)
	require.Contains(t, body, "Erişime izin ver")
	res, body = oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?client_id="+id, "")
	require.Equal(t, 400, res.StatusCode)
	require.Contains(t, body, "<!doctype html>", "a person reads it: a page, not a JSON body")
}

// A name a client gives itself cannot turn around on the consent page (a
// right-to-left override) or hide behind control characters.
func TestMCPOAuthClientNameLosesFormattingCharacters(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-name@example.test")
	browser.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	rlo := string(rune(0x202e))
	status, data := oauthRegisterWith(t, srv.URL, map[string]any{"client_name": "Good" + rlo + "exe.txt\t\x01 app", "token_endpoint_auth_method": "none", "redirect_uris": []string{oauthCallback}})
	require.Equal(t, 201, status, "%v", data)
	require.Equal(t, "Goodexe.txt app", data["client_name"])
	res, body := oauthRequest(t, browser, "GET", srv.URL+"/api/oauth/authorize?"+oauthAuthorizeQuery(data["client_id"].(string), oauthCallback, "read mcp").Encode(), "")
	require.Equal(t, 200, res.StatusCode, body)
	require.NotContains(t, body, rlo)
}

// An access token can be revoked (RFC 7009), and revoking it ends its
// connection, as removing its key does (section 2.1 lets the server revoke
// the refresh token with it): the key is the connection's handle.
func TestMCPOAuthAccessTokenRevocation(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-revoke-access@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	access := data["access_token"].(string)
	res, body := oauthRequest(t, http.DefaultClient, "POST", srv.URL+"/api/oauth/revoke", url.Values{"client_id": {id}, "token": {access}}.Encode())
	require.Equal(t, 200, res.StatusCode, body)
	res, _ = oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", access, toolsList)
	require.Equal(t, 401, res.StatusCode)
	res, data = oauthRefresh(t, srv.URL, id, data["refresh_token"].(string))
	require.Equal(t, 400, res.StatusCode, "%v", data)
}

// A 401 to a credential that no longer works tells the client so (RFC 6750
// section 3.1), so it refreshes instead of starting over.
func TestMCPOAuthChallengeSaysTheTokenIsInvalid(t *testing.T) {
	srv, _, _ := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	res, _ := oauthBearer(t, "POST", srv.URL+"/api/ai/mcp", "fxo_"+strings.Repeat("ab", 32), toolsList)
	require.Equal(t, 401, res.StatusCode)
	h := res.Header.Get("WWW-Authenticate")
	require.Contains(t, h, `resource_metadata="`+oauthPublic+`/.well-known/oauth-protected-resource/api/ai/mcp"`)
	require.Contains(t, h, `error="invalid_token"`)
}

// The token answer's refresh token is the grant's own handle: refreshing
// with it again after it was rotated is refused and ends the grant.
func TestMCPOAuthRefreshTokenShape(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.PublicURL = oauthPublic })
	browser, _, _ := desktopSession(t, srv.URL, store, "user", "oauth-shape@example.test")
	id := oauthRegister(t, srv.URL)
	code := oauthCode(t, srv.URL, id, browser, "read mcp")
	res, data := oauthExchange(t, srv.URL, id, code, oauthVerifier)
	require.Equal(t, 200, res.StatusCode, "%v", data)
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{128}$`), data["refresh_token"])
	require.Regexp(t, regexp.MustCompile(`^fxo_[0-9a-f]{64}$`), data["access_token"])
	// A made-up secret for a real grant is a stolen family member: the
	// grant ends.
	forged := data["refresh_token"].(string)[:64] + strings.Repeat("0", 64)
	res, _ = oauthRefresh(t, srv.URL, id, forged)
	require.Equal(t, 400, res.StatusCode)
	res, _ = oauthRefresh(t, srv.URL, id, data["refresh_token"].(string))
	require.Equal(t, 400, res.StatusCode, "the grant was revoked")
}

package api_test

// The cross-site request forgery guard (internal/originguard) measured through
// the whole router: a change sent from another origin with the visitor's
// session is refused BEFORE any handler runs, and nothing changes; filex's own
// pages, a key, a script and the public links keep working.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/originguard"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const ogEvil = "https://evil.example.net"

type ogResult struct {
	status int
	body   map[string]any
	header http.Header
}

func ogDo(t *testing.T, client *http.Client, method, url, body string, hdr map[string]string) ogResult {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := ogResult{status: resp.StatusCode, header: resp.Header}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func ogRefused(r ogResult) bool {
	return r.status == http.StatusForbidden && r.body["error"] == originguard.ErrorCode
}

// theme reads back the caller's web prefs document's `theme`.
func ogTheme(t *testing.T, srv *httptest.Server, client *http.Client) any {
	t.Helper()
	r := ogDo(t, client, http.MethodGet, srv.URL+"/api/me/prefs", "", nil)
	require.Equal(t, http.StatusOK, r.status)
	prefs, _ := r.body["prefs"].(map[string]any)
	return prefs["theme"]
}

func TestOriginGuard_Router(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	session := testutil.LoginAs(t, srv, client, email, pass)
	prefs := srv.URL + "/api/me/prefs"
	put := func(theme string, hdr map[string]string) ogResult {
		return ogDo(t, client, http.MethodPut, prefs, `{"prefs":{"theme":"`+theme+`"}}`, hdr)
	}

	t.Run("filex's own page writes", func(t *testing.T) {
		r := put("own", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": srv.URL})
		require.Equal(t, http.StatusOK, r.status, r.body)
		assert.Equal(t, "own", ogTheme(t, srv, client))
	})

	t.Run("another site is refused and nothing changes", func(t *testing.T) {
		for _, hdr := range []map[string]string{
			{"Sec-Fetch-Site": "cross-site", "Origin": ogEvil},
			{"Sec-Fetch-Site": "same-site", "Origin": "https://blog.test.local"},
			{"Origin": ogEvil},
			{"Origin": "null"},
		} {
			r := put("forged", hdr)
			assert.True(t, ogRefused(r), "%v: %d %v", hdr, r.status, r.body)
			assert.NotEmpty(t, r.body["message"])
		}
		assert.Equal(t, "own", ogTheme(t, srv, client), "the forged writes never reached the handler")
	})

	t.Run("a script with the cookie and no browser headers", func(t *testing.T) {
		r := put("script", nil)
		require.Equal(t, http.StatusOK, r.status, r.body)
		assert.Equal(t, "script", ogTheme(t, srv, client))
	})

	t.Run("a bearer from another origin is not the guard's business", func(t *testing.T) {
		bare := &http.Client{}
		r := ogDo(t, bare, http.MethodPut, prefs, `{"prefs":{"theme":"bearer"}}`, map[string]string{
			"Sec-Fetch-Site": "cross-site", "Origin": "app://filex", "Authorization": "Bearer " + session,
		})
		require.Equal(t, http.StatusOK, r.status, r.body)
		assert.Equal(t, "bearer", ogTheme(t, srv, client))
	})

	t.Run("P-3: a storage plugin install from another site", func(t *testing.T) {
		r := ogDo(t, client, http.MethodPost, srv.URL+"/api/admin/plugins", `{}`, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://blog.test.local"})
		assert.True(t, ogRefused(r), "%d %v", r.status, r.body)
		r = ogDo(t, client, http.MethodPost, srv.URL+"/api/admin/plugins", `{}`, map[string]string{"Sec-Fetch-Site": "same-origin"})
		assert.False(t, ogRefused(r), "the panel's own request reaches the handler (%d)", r.status)
	})

	t.Run("sign-in forgery", func(t *testing.T) {
		jar, _ := cookiejar.New(nil)
		visitor := &http.Client{Jar: jar}
		body := `{"email":"` + email + `","password":"` + pass + `"}`
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", ogEvil)
		resp, err := visitor.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Empty(t, resp.Cookies(), "no session handed to the forged sign-in")

		r := ogDo(t, visitor, http.MethodPost, srv.URL+"/api/auth/login", body, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": srv.URL})
		assert.Equal(t, http.StatusOK, r.status, "the sign-in page itself")
	})

	t.Run("public links answer whoever asks", func(t *testing.T) {
		r := ogDo(t, client, http.MethodPost, srv.URL+"/api/public/d/no-such-token/pin", `{"pin":"1"}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": ogEvil})
		assert.False(t, ogRefused(r), "%d %v", r.status, r.body)
		r = ogDo(t, client, http.MethodPost, srv.URL+"/api/auth/desktop/exchange", `{}`, map[string]string{"Origin": "null"})
		assert.False(t, ogRefused(r), "the desktop's PKCE exchange (%d)", r.status)
	})

	t.Run("WebSocket upgrade", func(t *testing.T) {
		ws := map[string]string{"Upgrade": "websocket", "Connection": "Upgrade", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="}
		cross := map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://blog.test.local"}
		for k, v := range ws {
			cross[k] = v
		}
		r := ogDo(t, client, http.MethodGet, srv.URL+"/api/ws", "", cross)
		assert.True(t, ogRefused(r), "%d %v", r.status, r.body)
		r = ogDo(t, client, http.MethodGet, srv.URL+"/api/ws?ticket=bogus", "", cross)
		assert.False(t, ogRefused(r), "a ticket is the embed's credential; the handler judges it (%d)", r.status)
	})

	t.Run("audit names the account", func(t *testing.T) {
		rows, err := store.ListAuditRecent(context.Background(), 100)
		require.NoError(t, err)
		found := false
		for _, row := range rows {
			if row.Action == originguard.AuditAction && row.UserID != nil {
				found = true
				assert.NotEmpty(t, row.Metadata["reason"])
			}
		}
		assert.True(t, found, "a refused request that carried a session is audited")
	})
}

// An origin on FILEX_CORS_ALLOWED_ORIGINS (an embed that calls filex with
// the visitor's session) writes; the list is the only place to name one.
func TestOriginGuard_RouterTrustsTheCORSList(t *testing.T) {
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.CORS.AllowedOrigins = []string{"https://work.example.org"}
	})
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	r := ogDo(t, client, http.MethodPut, srv.URL+"/api/me/prefs", `{"prefs":{"theme":"embed"}}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://work.example.org"})
	require.Equal(t, http.StatusOK, r.status, r.body)
	assert.Equal(t, "https://work.example.org", r.header.Get("Access-Control-Allow-Origin"), "the same list answers CORS")
	r = ogDo(t, client, http.MethodPut, srv.URL+"/api/me/prefs", `{"prefs":{"theme":"x"}}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": ogEvil})
	assert.True(t, ogRefused(r))
}

// Below a base path (FILEX_BASE_PATH) the exempt routes are matched after the
// base comes off, like every other route.
func TestOriginGuard_RouterUnderABasePath(t *testing.T) {
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.BasePath = "/filex"
		c.PublicURL = "http://test.local/filex"
	})
	email, pass := testutil.SeedAdmin(t, store)
	body := `{"email":"` + email + `","password":"` + pass + `"}`
	r := ogDo(t, client, http.MethodPost, srv.URL+"/filex/api/auth/login", body, map[string]string{"Sec-Fetch-Site": "same-origin"})
	require.Equal(t, http.StatusOK, r.status, r.body)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": ogEvil}
	assert.True(t, ogRefused(ogDo(t, client, http.MethodPut, srv.URL+"/filex/api/me/prefs", `{"prefs":{}}`, cross)))
	assert.False(t, ogRefused(ogDo(t, client, http.MethodPost, srv.URL+"/filex/api/public/d/nope/pin", `{"pin":"1"}`, cross)))
}

package handlers_test

// POST /api/auth/logout and the IdP half of signing out.
//
// Signing out used to delete filex's session and nothing else, so in
// SSO-first mode the login page went straight back to an IdP whose session
// was still open and the same account came back without a form. The handler
// now also answers with the IdP's end-session URL (OpenID Connect
// RP-Initiated Logout 1.0) when the session was an OIDC one, and the web app
// navigates there.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// fakeLogoutOIDC is an OIDC driver that can end IdP sessions, and records
// what it was asked.
type fakeLogoutOIDC struct {
	fakeOIDC
	calls    int
	idToken  string
	backTo   string
	endpoint string
}

var _ auth.OIDCLogoutDriver = (*fakeLogoutOIDC)(nil)

func (f *fakeLogoutOIDC) EndSessionURL(_ *http.Request, idToken, postLogoutRedirect string) string {
	f.calls++
	f.idToken, f.backTo = idToken, postLogoutRedirect
	return f.endpoint
}

type logoutResult struct {
	status    int
	body      map[string]any
	cookieCut bool
}

func postLogout(t *testing.T, a *handlers.Auth, host, session, body string) logoutResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", bytes.NewBufferString(body))
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "filex_session", Value: session})
	}
	rec := httptest.NewRecorder()
	a.Logout(rec, req)
	out := logoutResult{status: rec.Code}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out.body))
	for _, c := range rec.Result().Cookies() {
		if c.Name == "filex_session" && c.MaxAge < 0 {
			out.cookieCut = true
		}
	}
	return out
}

// oidcSession seeds a signed-in OIDC session that kept its id_token.
func oidcSession(t *testing.T, store db.Store, token, idToken string) {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "sso-"+token+"@example.test", "", model.RoleUser, "en", "")
	require.NoError(t, err)
	_, err = store.CreateSession(ctx, u.ID, token, time.Now().Add(time.Hour), "", "")
	require.NoError(t, err)
	if idToken != "" {
		require.NoError(t, store.SetSessionIDToken(ctx, token, idToken))
	}
}

func TestLogout_OIDCSessionAlsoEndsTheIdPSession(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	oidcSession(t, store, "tkn-1", "idt-1")
	idp := &fakeLogoutOIDC{endpoint: "https://idp.example.test/logout?id_token_hint=idt-1"}
	a := handlers.NewAuth(store, nil, idp, "https://files.example.test", false, "")

	res := postLogout(t, a, "files.example.test", "tkn-1", `{"return_to":"/drive/login"}`)

	require.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, true, res.body["ok"])
	assert.Equal(t, idp.endpoint, res.body["logout_url"])
	assert.Equal(t, "idt-1", idp.idToken, "the session's own id_token is the hint")
	assert.Equal(t, "https://files.example.test/drive/login?signed_out=1", idp.backTo,
		"back to the front door the person was using, told they are signed out")
	// filex's half is unchanged: the session is gone and the cookie is cut.
	assert.True(t, res.cookieCut)
	_, err := store.GetSessionByToken(context.Background(), "tkn-1")
	assert.Error(t, err)
}

// The post-logout address is built from a fixed list — never from what the
// caller sent — so sign-out cannot be turned into an open redirect.
func TestLogout_ReturnToIsOneOfTheTwoFrontDoors(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"return_to":"/admin/login"}`, "https://files.example.test/admin/login?signed_out=1"},
		{`{"return_to":"/drive/login"}`, "https://files.example.test/drive/login?signed_out=1"},
		{`{"return_to":"https://evil.example/x"}`, "https://files.example.test/admin/login?signed_out=1"},
		{`{"return_to":"//evil.example/drive/login"}`, "https://files.example.test/admin/login?signed_out=1"},
		{`{"return_to":"/drive/login?x=1"}`, "https://files.example.test/admin/login?signed_out=1"},
		{``, "https://files.example.test/admin/login?signed_out=1"},
		{`not json`, "https://files.example.test/admin/login?signed_out=1"},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			_, store := testutil.NewTestDB(t)
			oidcSession(t, store, "tkn", "idt")
			idp := &fakeLogoutOIDC{endpoint: "https://idp.example.test/logout"}
			a := handlers.NewAuth(store, nil, idp, "https://files.example.test", false, "")

			res := postLogout(t, a, "files.example.test", "tkn", c.body)

			require.Equal(t, http.StatusOK, res.status)
			assert.Equal(t, c.want, idp.backTo)
		})
	}
}

// Multi-tenant: back to the TENANT's host, like the OIDC callback.
func TestLogout_MultiTenantReturnsToTheTenantHost(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	seedProvider(t, store, &model.Provider{Slug: "tenant-a", Host: "files.tenant-a.test", AuthType: model.AuthTypeOIDC})
	oidcSession(t, store, "tkn", "idt")
	idp := &fakeLogoutOIDC{endpoint: "https://idp.tenant-a.test/logout"}
	a := handlers.NewAuth(store, nil, idp, "https://operator.test", true, "")

	postLogout(t, a, "files.tenant-a.test", "tkn", `{"return_to":"/drive/login"}`)

	assert.Equal(t, "https://files.tenant-a.test/drive/login?signed_out=1", idp.backTo)
}

func TestLogout_StaysLocalWhenThereIsNothingToEnd(t *testing.T) {
	t.Run("password session — no id_token kept", func(t *testing.T) {
		_, store := testutil.NewTestDB(t)
		oidcSession(t, store, "tkn", "")
		idp := &fakeLogoutOIDC{endpoint: "https://idp.example.test/logout"}
		a := handlers.NewAuth(store, nil, idp, "https://files.example.test", false, "")

		res := postLogout(t, a, "files.example.test", "tkn", "")

		assert.NotContains(t, res.body, "logout_url")
		assert.Zero(t, idp.calls)
		assert.True(t, res.cookieCut)
	})

	t.Run("no session cookie", func(t *testing.T) {
		_, store := testutil.NewTestDB(t)
		idp := &fakeLogoutOIDC{endpoint: "https://idp.example.test/logout"}
		a := handlers.NewAuth(store, nil, idp, "https://files.example.test", false, "")

		res := postLogout(t, a, "files.example.test", "", "")

		assert.Equal(t, true, res.body["ok"])
		assert.NotContains(t, res.body, "logout_url")
		assert.Zero(t, idp.calls)
	})

	t.Run("driver cannot end IdP sessions", func(t *testing.T) {
		_, store := testutil.NewTestDB(t)
		oidcSession(t, store, "tkn", "idt")
		a := handlers.NewAuth(store, nil, &fakeOIDC{}, "https://files.example.test", false, "")

		res := postLogout(t, a, "files.example.test", "tkn", "")

		assert.NotContains(t, res.body, "logout_url")
	})

	t.Run("IdP offers no end-session URL", func(t *testing.T) {
		_, store := testutil.NewTestDB(t)
		oidcSession(t, store, "tkn", "idt")
		a := handlers.NewAuth(store, nil, &fakeLogoutOIDC{endpoint: ""}, "https://files.example.test", false, "")

		res := postLogout(t, a, "files.example.test", "tkn", "")

		assert.NotContains(t, res.body, "logout_url")
	})
}

// The whole path through the real router: SSO sign-in against an IdP (start →
// callback), then sign-out. Also proves FILEX_OIDC_LOGOUT reaches the handler.
func TestLogout_EndToEndThroughTheRouter(t *testing.T) {
	signInAndOut := func(t *testing.T, logoutMode string) (*fakeidp.IdP, map[string]any, db.Store) {
		t.Helper()
		idp := fakeidp.New(t)
		srv, client, store := testutil.NewTestServerWith(t,
			func(c *config.Config) { c.Auth.OIDC.Logout = logoutMode },
			func(d *api.Deps) {
				drv := authoidc.New(d.Store)
				require.NoError(t, drv.Init(context.Background(), map[string]any{
					"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
					"redirect_url": "http://test.local/api/auth/oidc/callback",
				}))
				d.OIDCAuth = drv
			})
		// Stop at the IdP: this test is the browser, and plays the IdP's part
		// by carrying the state back itself.
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

		resp, err := client.Get(srv.URL + "/api/auth/oidc/start")
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusFound, resp.StatusCode)
		base, _ := url.Parse(srv.URL)
		state := ""
		for _, c := range client.Jar.Cookies(base) {
			if c.Name == "filex_oidc_state" {
				state = c.Value
			}
		}
		require.NotEmpty(t, state)

		resp, err = client.Get(srv.URL + "/api/auth/oidc/callback?code=any&state=" + url.QueryEscape(state))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "signed in")

		resp, err = client.Post(srv.URL+"/api/auth/logout", "application/json",
			bytes.NewBufferString(`{"return_to":"/drive/login"}`))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		return idp, body, store
	}

	t.Run("default: the IdP session ends too", func(t *testing.T) {
		idp, body, _ := signInAndOut(t, "")

		raw, _ := body["logout_url"].(string)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		require.Equal(t, idp.EndSessionEndpoint(), u.Scheme+"://"+u.Host+u.Path)
		q := u.Query()
		assert.Equal(t, idp.LastIDToken(), q.Get("id_token_hint"))
		assert.Equal(t, "filex", q.Get("client_id"))
		assert.Equal(t, "http://test.local/drive/login?signed_out=1", q.Get("post_logout_redirect_uri"))
	})

	t.Run("FILEX_OIDC_LOGOUT=local: filex only", func(t *testing.T) {
		_, body, _ := signInAndOut(t, "local")

		assert.Equal(t, true, body["ok"])
		assert.NotContains(t, body, "logout_url")
	})
}

// FILEX_OIDC_LOGOUT=local — the operator keeps sign-out inside filex.
func TestLogout_LocalModeLeavesTheIdPSessionAlone(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	oidcSession(t, store, "tkn", "idt")
	idp := &fakeLogoutOIDC{endpoint: "https://idp.example.test/logout"}
	a := handlers.NewAuth(store, nil, idp, "https://files.example.test", false, "")
	a.OIDCLocalLogout = true

	res := postLogout(t, a, "files.example.test", "tkn", "")

	assert.NotContains(t, res.body, "logout_url")
	assert.Zero(t, idp.calls)
	assert.True(t, res.cookieCut)
}

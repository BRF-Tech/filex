package oidc_test

// Sign-out that ends the IdP's session too (OpenID Connect RP-Initiated
// Logout 1.0).
//
// Before this, filex dropped its own session and nothing else. The IdP's
// session stayed open, so in SSO-first mode (FILEX_OIDC_AUTO_REDIRECT) the
// login page went straight back to the IdP, the IdP issued a new code without
// a form, and the same account was signed in again half a second later.
// Ending the IdP session needs the id_token as id_token_hint, and the
// callback used to verify it and throw it away.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

const redirectURL = "https://files.example.test/api/auth/oidc/callback"

func newDriver(t *testing.T, idp *fakeidp.IdP) (*oidc.Driver, db.Store) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"issuer":        idp.Issuer(),
		"client_id":     "filex",
		"client_secret": "s3cret",
		"redirect_url":  redirectURL,
	}))
	return drv, store
}

// signIn runs the browser's half of the code flow: start (which sets the
// state cookie), then the callback carrying that cookie back.
func signIn(t *testing.T, drv *oidc.Driver) string {
	t.Helper()
	start := httptest.NewRecorder()
	require.NoError(t, drv.StartFlow(start, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil)))
	var state *http.Cookie
	for _, c := range start.Result().Cookies() {
		if c.Name == "filex_oidc_state" {
			state = c
		}
	}
	require.NotNil(t, state, "StartFlow sets the state cookie")

	cb := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=any&state="+url.QueryEscape(state.Value), nil)
	cb.AddCookie(state)
	_, session, err := drv.HandleCallback(httptest.NewRecorder(), cb)
	require.NoError(t, err)
	require.NotEmpty(t, session)
	return session
}

func TestCallbackKeepsTheIDTokenForSignOut(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := newDriver(t, idp)

	session := signIn(t, drv)

	got, err := store.GetSessionIDToken(context.Background(), session)
	require.NoError(t, err)
	require.NotEmpty(t, idp.LastIDToken())
	require.Equal(t, idp.LastIDToken(), got)
}

// Nothing to hint at an IdP that cannot end sessions: the token is personal
// data filex would be keeping for no use.
func TestCallbackKeepsNoIDTokenWhenTheIdPCannotEndSessions(t *testing.T) {
	idp := fakeidp.New(t, fakeidp.WithoutEndSession())
	drv, store := newDriver(t, idp)

	session := signIn(t, drv)

	got, err := store.GetSessionIDToken(context.Background(), session)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestEndSessionURL(t *testing.T) {
	idp := fakeidp.New(t)
	drv, _ := newDriver(t, idp)
	signIn(t, drv)
	idToken := idp.LastIDToken()
	back := "https://files.example.test/drive/login?signed_out=1"

	got := drv.EndSessionURL(httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil), idToken, back)

	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, idp.EndSessionEndpoint(), u.Scheme+"://"+u.Host+u.Path)
	q := u.Query()
	require.Equal(t, idToken, q.Get("id_token_hint"), "the hint is what lets the IdP skip its confirmation page")
	require.Equal(t, "filex", q.Get("client_id"))
	require.Equal(t, back, q.Get("post_logout_redirect_uri"))
}

func TestEndSessionURLIsEmptyWhenTheIdPCannotBeAsked(t *testing.T) {
	back := "https://files.example.test/admin/login?signed_out=1"
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)

	t.Run("no end_session_endpoint in discovery", func(t *testing.T) {
		idp := fakeidp.New(t, fakeidp.WithoutEndSession())
		drv, _ := newDriver(t, idp)
		signIn(t, drv)
		require.Empty(t, drv.EndSessionURL(req, idp.LastIDToken(), back))
	})

	t.Run("no id_token kept for the session", func(t *testing.T) {
		idp := fakeidp.New(t)
		drv, _ := newDriver(t, idp)
		require.Empty(t, drv.EndSessionURL(req, "", back))
	})

	// Never hand one IdP a token another one issued — a provider whose issuer
	// was changed between sign-in and sign-out, say.
	t.Run("id_token issued by another issuer", func(t *testing.T) {
		idp := fakeidp.New(t)
		drv, _ := newDriver(t, idp)
		foreign := idp.IDToken(map[string]any{
			"iss": "https://elsewhere.example.test/realms/other", "aud": "filex", "sub": "x",
			"exp": time.Now().Add(time.Minute).Unix(),
		})
		require.Empty(t, drv.EndSessionURL(req, foreign, back))
	})
}

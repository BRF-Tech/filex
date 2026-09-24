package handlers_test

// RP-initiated logout (PR #40, Berk Başarır) against the identity-providers
// page v0.43.0 made real (authsetup.Live): the IdP half of sign-out follows the
// provider the page runs NOW — configured without a restart, and changed
// without one.
//
// ⚠ Why this exists. The handlers hold authsetup.Live's OIDC proxy, not a
// driver, and PR #40 asks the driver it holds for EndSessionURL with a type
// assertion. The proxy had no such method, so on a real server the assertion
// failed and sign-out stayed local — SSO-first mode signed the same account
// straight back in, the very report PR #40 fixed — while every test of PR #40
// passed, because each one wired a driver in by hand. Only a sign-in through
// the page's own provider, over HTTP, sees the wiring.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// signOut posts the web app's sign-out and returns the IdP URL it answered
// with ("" = sign-out stayed local).
func signOut(t *testing.T, c *http.Client, base, returnTo string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"return_to": returnTo})
	resp, err := c.Post(base+"/api/auth/logout", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		OK        bool   `json:"ok"`
		LogoutURL string `json:"logout_url"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.True(t, out.OK)
	return out.LogoutURL
}

func TestIdentityProviders_SignOutEndsTheSessionAtTheProviderThePageRuns(t *testing.T) {
	first := newAdaIssuer(t, "right-secret")
	srv, admin, _, _, _ := liveServer(t, nil)
	configure := func(idp *fakeidp.IdP) {
		t.Helper()
		status, body := patchProvider(t, admin, srv.URL, "oidc", map[string]any{"enabled": true, "config": map[string]any{
			"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "right-secret",
			"redirect_url": srv.URL + "/api/auth/oidc/callback",
		}})
		require.Equal(t, http.StatusOK, status, "%v", body)
	}
	configure(first)

	// Signed in through the page's provider, signed out: the IdP's session
	// ends too, with the session's own id_token and this front door to return to.
	browser, ok, who := oidcBrowser(t, srv.URL)
	require.True(t, ok)
	require.Equal(t, "ada@idp.example", who)
	got := signOut(t, browser, srv.URL, "/drive/login")
	require.NotEmpty(t, got, "sign-out stayed local: the running provider was never asked to end its session")
	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, first.EndSessionEndpoint(), u.Scheme+"://"+u.Host+u.Path)
	require.Equal(t, "filex", u.Query().Get("client_id"))
	require.NotEmpty(t, u.Query().Get("id_token_hint"))
	require.True(t, strings.HasSuffix(u.Query().Get("post_logout_redirect_uri"), "/drive/login?signed_out=1"),
		"back to the front door the person used: %s", u.Query().Get("post_logout_redirect_uri"))

	// A session from the provider that WAS running, signed out after the page
	// switched to another one: its token is the old issuer's, and no IdP is
	// handed another's token — sign-out stays local for it.
	stale, ok, _ := oidcBrowser(t, srv.URL)
	require.True(t, ok)
	second := newAdaIssuer(t, "right-secret")
	configure(second)
	require.Empty(t, signOut(t, stale, srv.URL, "/admin/login"),
		"a token the old provider issued must not be sent to the new one")

	// …and a session through the provider the page runs now ends at THAT one.
	fresh, ok, _ := oidcBrowser(t, srv.URL)
	require.True(t, ok)
	got = signOut(t, fresh, srv.URL, "/admin/login")
	require.True(t, strings.HasPrefix(got, second.EndSessionEndpoint()+"?"), "%s", got)

	// Switched off on the page: nothing to end at an IdP, sign-out is local
	// and the password sign-in the lockout protection keeps is still there.
	again, ok, _ := oidcBrowser(t, srv.URL)
	require.True(t, ok)
	status, body := patchProvider(t, admin, srv.URL, "oidc", map[string]any{"enabled": false})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Empty(t, signOut(t, again, srv.URL, "/admin/login"))
	resp, err := admin.Get(srv.URL + "/api/auth/me")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the administrator's own session is untouched")
}

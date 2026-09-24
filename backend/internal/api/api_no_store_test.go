package api_test

// Every /api response is uncacheable unless its handler says otherwise.
//
// Field report (2026-09-24, a multi-tenant install behind Cloudflare): the
// tenant's zone carried a "cache everything" rule written for its WordPress
// site, with no host condition. GET /api/auth/me carried no Cache-Control, so
// the edge kept one administrator's answer for two hours and handed it to
// everyone who asked — an anonymous curl included (email, role: admin). After
// signing out and in as another account the web app still showed the
// administrator: the identity request never reached filex.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func get(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := c.Get(url)
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

func TestAPIResponsesAreNotStoredByDefault(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)

	// Anonymous — the refusal is not stored either.
	resp := get(t, http.DefaultClient, srv.URL+"/api/auth/me")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, []string{"no-store"}, resp.Header.Values("Cache-Control"))

	// Signed in — the answer that leaked.
	testutil.LoginAs(t, srv, client, email, pw)
	resp = get(t, client, srv.URL+"/api/auth/me")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"no-store"}, resp.Header.Values("Cache-Control"))
}

// A handler with a caching policy of its own keeps it — one value, not the
// default appended to it (no-store beside max-age would cancel the max-age).
func TestAPIHandlersKeepTheirOwnCachePolicy(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)

	resp := get(t, http.DefaultClient, srv.URL+"/api/branding")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"public, max-age=60"}, resp.Header.Values("Cache-Control"))
}

func TestNonAPIRoutesAreNotTouched(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)

	resp := get(t, http.DefaultClient, srv.URL+"/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Values("Cache-Control"))
}

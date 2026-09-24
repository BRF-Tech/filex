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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
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
//
// v0.43.0: branding is no longer `public, max-age=60` but `public, no-cache`
// with a strong ETag (handlers/public_cache.go) — kept, and asked about on
// every load. ⚠ The 304 of that revalidation carries the same policy: a 304
// that said `no-store` would tell the browser to drop the copy it just
// revalidated, and every page load would fetch the whole body again.
func TestAPIHandlersKeepTheirOwnCachePolicy(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)

	resp := get(t, http.DefaultClient, srv.URL+"/api/branding")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"public, no-cache"}, resp.Header.Values("Cache-Control"))
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/branding", nil)
	require.NoError(t, err)
	req.Header.Set("If-None-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.Equal(t, []string{"public, no-cache"}, resp.Header.Values("Cache-Control"))
}

func TestNonAPIRoutesAreNotTouched(t *testing.T) {
	srv, _, _ := testutil.NewTestServer(t)

	resp := get(t, http.DefaultClient, srv.URL+"/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Values("Cache-Control"))
}

// The default is installed above CORS and the demo guard, so an answer one of
// them writes itself carries it too: "every /api answer" has no exceptions but
// the ones a handler names.
func TestAPINoStore_CoversAnswersTheMiddlewareWrites(t *testing.T) {
	srv, _, _ := testutil.NewTestServerWith(t, func(c *config.Config) { c.Demo.Mode = true }, nil)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/users", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "the demo guard answers before any route")
	assert.Equal(t, []string{"no-store"}, resp.Header.Values("Cache-Control"))
}

package api_test

// The browser-facing headers (internal/secheaders), measured through the whole
// router the way a browser meets it: filex's pages say who may frame them,
// every answer is nosniff with a referrer that stops at the origin, and a
// script or a JSON answer is not given a framing policy it has no use for.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func newHeadersServer(t *testing.T, ancestors []string) *httptest.Server {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.FrameAncestors = ancestors
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
	})
	testutil.SeedAdmin(t, store)
	return srv
}

func headersOf(t *testing.T, url string) http.Header {
	t.Helper()
	res, err := http.Get(url)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	require.Less(t, res.StatusCode, 500, url)
	return res.Header
}

func TestSecurityHeaders_TheAppsPagesAreNotFramedByOtherSites(t *testing.T) {
	srv := newHeadersServer(t, nil)
	for _, page := range []string{"/admin/", "/admin/explore", "/drive/explore"} {
		h := headersOf(t, srv.URL+page)
		assert.Contains(t, h.Get("Content-Type"), "text/html", page)
		assert.Equal(t, "frame-ancestors 'self'", h.Get("Content-Security-Policy"), page)
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), page)
		assert.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"), page)
	}
}

func TestSecurityHeaders_ScriptsAndAnswersAreNosniffWithoutAFramingPolicy(t *testing.T) {
	srv := newHeadersServer(t, nil)
	for _, path := range []string{"/admin/assets/index-AbC123.js", "/embed.js", "/api/capabilities", "/healthz"} {
		h := headersOf(t, srv.URL+path)
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), path)
		assert.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"), path)
		assert.Empty(t, h.Get("Content-Security-Policy"), path)
	}
}

func TestSecurityHeaders_TheOperatorNamesTheDashboardsThatMayFrameIt(t *testing.T) {
	srv := newHeadersServer(t, []string{"https://home.example.com", "https://*.example.org"})
	h := headersOf(t, srv.URL+"/admin/")
	assert.Equal(t, "frame-ancestors 'self' https://home.example.com https://*.example.org", h.Get("Content-Security-Policy"))
}

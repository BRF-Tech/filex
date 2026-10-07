package api_test

// The editor's frame (task #92) through the whole router: with an origin of
// its own for app interfaces (FILEX_APP_UI_ORIGIN) filex serves the page
// ONLYOFFICE's api.js runs in THERE - on that host only, never on its own -
// with the policy onlyoffice.FramePage builds, never cached, no cookie.
// Before #92 there was no such page: every assertion on a 200 below is red on
// the old code.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const frameUIHost = "apps.usercontent.example"

func newFrameServer(t *testing.T, base string, uiOrigin bool, documentServer string) *httptest.Server {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.BasePath = base
		if uiOrigin {
			c.AppUIOrigin = "https://" + frameUIHost
		}
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
		if documentServer != "" {
			d.OnlyOffice = onlyoffice.New(d.Store, nil, documentServer, "s3cret", "http://test.local", 0)
		}
	})
	testutil.SeedAdmin(t, store)
	return srv
}

// frameGet asks srv for path as the browser would on host.
func frameGet(t *testing.T, srv *httptest.Server, method, host, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	require.NoError(t, err)
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestOnlyOfficeFrame_ServedOnTheInterfaceOriginOnly(t *testing.T) {
	for _, base := range []string{"", "/filex"} {
		srv := newFrameServer(t, base, true, "https://docs.example.com")
		path := base + onlyoffice.FramePath

		res, body := frameGet(t, srv, http.MethodGet, frameUIHost, path)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s on the interface host", path)
		assert.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
		assert.Equal(t, "no-store", res.Header.Get("Cache-Control"), "the policy names the document server in force")
		assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
		assert.Empty(t, res.Header.Values("Set-Cookie"), "the interface origin never gets a cookie")
		csp := res.Header.Get("Content-Security-Policy")
		assert.Contains(t, csp, "script-src 'sha256-")
		assert.Contains(t, csp, "frame-src https://docs.example.com/")
		assert.Contains(t, csp, "frame-ancestors *")
		assert.Equal(t, 1, strings.Count(csp, "frame-ancestors"), "the router's own frame-ancestors is not added to the page's")
		assert.NotContains(t, csp, "frame-ancestors 'self'")
		assert.Contains(t, body, `data-api="https://docs.example.com/web-apps/apps/api/documents/api.js"`)
		assert.Contains(t, body, `id="filex-oo-editor"`)

		res, body = frameGet(t, srv, http.MethodHead, frameUIHost, path)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Empty(t, body)

		// filex's own host: a frame there would put api.js back on filex's
		// origin, which is the whole point of not doing it.
		res, body = frameGet(t, srv, http.MethodGet, strings.TrimPrefix(srv.URL, "http://"), path)
		assert.Equal(t, http.StatusNotFound, res.StatusCode, "%s on filex's own host", path)
		assert.NotContains(t, body, "filex-oo-editor")
	}
}

func TestOnlyOfficeFrame_NothingWithoutADocumentServer(t *testing.T) {
	srv := newFrameServer(t, "", true, "")
	res, body := frameGet(t, srv, http.MethodGet, frameUIHost, onlyoffice.FramePath)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.NotContains(t, body, "filex-oo-editor")

	// A document server whose address cannot be named in a policy: no page
	// either (onlyoffice.FrameSource).
	srv = newFrameServer(t, "", true, "https://docs.example.com/a b")
	res, _ = frameGet(t, srv, http.MethodGet, frameUIHost, onlyoffice.FramePath)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

// Without FILEX_APP_UI_ORIGIN the frame does not exist anywhere: api.js runs
// in filex's page as it always has, and a frame of filex's own origin would
// isolate nothing.
func TestOnlyOfficeFrame_NotServedWithoutAnInterfaceOrigin(t *testing.T) {
	srv := newFrameServer(t, "", false, "https://docs.example.com")
	for _, host := range []string{frameUIHost, strings.TrimPrefix(srv.URL, "http://")} {
		res, body := frameGet(t, srv, http.MethodGet, host, onlyoffice.FramePath)
		assert.NotContains(t, body, "filex-oo-editor", host)
		assert.NotContains(t, res.Header.Get("Content-Security-Policy"), "sha256-", host)
	}
}

// The interface host answers nothing of filex's but the interface route: the
// frame's neighbours there are not reachable through it.
func TestOnlyOfficeFrame_TheInterfaceHostStillAnswersNothingElse(t *testing.T) {
	srv := newFrameServer(t, "", true, "https://docs.example.com")
	for _, path := range []string{"/api/files/onlyoffice/config", "/admin/", "/api/auth/whoami"} {
		res, _ := frameGet(t, srv, http.MethodGet, frameUIHost, path)
		assert.Equal(t, http.StatusNotFound, res.StatusCode, path)
	}
}

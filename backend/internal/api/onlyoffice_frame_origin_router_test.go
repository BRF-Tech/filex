package api_test

// The ONLYOFFICE editor's frame on its own origin (task #92, the maintainers'
// ruling of 2026-10-06: no new domain, the frame lives on the document server's own
// origin - docs.example.com beside files.example.com).
//
// FILEX_ONLYOFFICE_FRAME_ORIGIN names that origin; the document server's
// reverse proxy sends /filex-frame/* to filex. Through the whole router:
//
//   - on that host filex answers /filex-frame/editor and NOTHING else - not
//     the SPA, the API, a share, the interface route - at the host's root,
//     whatever filex's base path;
//   - on every other host /filex-frame/ is refused, so the page never runs on
//     filex's own origin;
//   - the editor config names the frame there, before the interface origin.
//
// Red before the frame origin existed (the field, the dispatch and the path
// are new): every 200 below, and the frame address in the config.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const docsHost = "docs.example.com"

func newFrameOriginServer(t *testing.T, base, frameOrigin, documentServer string) *httptest.Server {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.BasePath = base
		c.ExternalServices.OnlyOffice.FrameOrigin = frameOrigin
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
		if documentServer != "" {
			d.OnlyOffice = onlyoffice.New(d.Store, nil, documentServer, "s3cret", "http://test.local", 0)
		}
	})
	testutil.SeedAdmin(t, store)
	return srv
}

func TestOnlyOfficeFrameOrigin_TheOnePathOnTheDocumentServersHost(t *testing.T) {
	for _, base := range []string{"", "/filex"} {
		srv := newFrameOriginServer(t, base, "https://"+docsHost, "https://"+docsHost)

		res, body := frameGet(t, srv, http.MethodGet, docsHost, onlyoffice.FrameHostPath)
		require.Equal(t, http.StatusOK, res.StatusCode, "base %q: the frame at the host's root", base)
		assert.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
		assert.Equal(t, "no-store", res.Header.Get("Cache-Control"))
		assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
		assert.Empty(t, res.Header.Values("Set-Cookie"))
		csp := res.Header.Get("Content-Security-Policy")
		assert.Contains(t, csp, "script-src 'sha256-")
		assert.Contains(t, csp, "frame-src https://docs.example.com/")
		assert.Equal(t, 1, strings.Count(csp, "frame-ancestors"))
		assert.Contains(t, body, `data-api="https://docs.example.com/web-apps/apps/api/documents/api.js"`)

		res, body = frameGet(t, srv, http.MethodHead, docsHost, onlyoffice.FrameHostPath)
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Empty(t, body)

		// Nothing else of filex's on the document server's host - its proxy
		// sends only /filex-frame/*, and filex would not answer more if it
		// sent more.
		for _, path := range []string{
			"/", "/admin/", "/api/auth/whoami", "/api/files/onlyoffice/config", "/s/sharetoken",
			"/filex-frame/", "/filex-frame/other", "/filex-frame/editor/x",
			"/_appui/_onlyoffice/editor", "/web-apps/apps/api/documents/api.js",
			base + "/filex-frame/editor", base + "/admin/",
		} {
			if path == onlyoffice.FrameHostPath {
				continue
			}
			res, body := frameGet(t, srv, http.MethodGet, docsHost, path)
			assert.Equal(t, http.StatusNotFound, res.StatusCode, "base %q: %s on the document server's host", base, path)
			assert.NotContains(t, body, "filex-oo-editor", path)
		}

		// filex's own host never serves the frame, under the base or not: a
		// frame on filex's origin would run the script with filex's session.
		own := strings.TrimPrefix(srv.URL, "http://")
		for _, path := range []string{onlyoffice.FrameHostPath, base + onlyoffice.FrameHostPath} {
			res, body := frameGet(t, srv, http.MethodGet, own, path)
			assert.Equal(t, http.StatusNotFound, res.StatusCode, "base %q: %s on filex's own host", base, path)
			assert.NotContains(t, body, "filex-oo-editor", path)
		}
	}
}

// The browser sends filex's session cookie to the document server's host too
// (same site, Domain=.example.com on a multi-tenant install): the frame does
// not read it, and answers the same page.
func TestOnlyOfficeFrameOrigin_TheSessionCookieChangesNothing(t *testing.T) {
	srv := newFrameOriginServer(t, "", "https://"+docsHost, "https://"+docsHost)
	_, plain := frameGet(t, srv, http.MethodGet, docsHost, onlyoffice.FrameHostPath)

	req, err := http.NewRequest(http.MethodGet, srv.URL+onlyoffice.FrameHostPath, nil)
	require.NoError(t, err)
	req.Host = docsHost
	req.AddCookie(&http.Cookie{Name: authlocal.SessionCookieName, Value: "a-session-token"})
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	buf := new(strings.Builder)
	_, _ = io.Copy(buf, res.Body)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, plain, buf.String())
	assert.Empty(t, res.Header.Values("Set-Cookie"))
}

func TestOnlyOfficeFrameOrigin_NothingWithoutADocumentServer(t *testing.T) {
	srv := newFrameOriginServer(t, "", "https://"+docsHost, "")
	res, body := frameGet(t, srv, http.MethodGet, docsHost, onlyoffice.FrameHostPath)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	assert.NotContains(t, body, "filex-oo-editor")
}

// No frame origin: /filex-frame/ answers nothing on any host - a proxy rule
// sending it to filex before the setting is made shows no filex page there.
func TestOnlyOfficeFrameOrigin_UnsetServesNothingUnderThePrefix(t *testing.T) {
	srv := newFrameOriginServer(t, "", "", "https://"+docsHost)
	for _, host := range []string{docsHost, strings.TrimPrefix(srv.URL, "http://")} {
		res, body := frameGet(t, srv, http.MethodGet, host, onlyoffice.FrameHostPath)
		assert.Equal(t, http.StatusNotFound, res.StatusCode, host)
		assert.NotContains(t, body, "filex-oo-editor", host)
	}
}

// filex's pages may frame the frame origin (frame-src), as they frame the
// document server.
func TestOnlyOfficeFrameOrigin_PagesMayFrameIt(t *testing.T) {
	srv := newFrameOriginServer(t, "", "https://frames.example.com", "")
	h := headersOf(t, srv.URL+"/admin/")
	assert.Contains(t, h.Get("Content-Security-Policy"), " https://frames.example.com")
}

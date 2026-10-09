package api_test

// The browser-facing headers (internal/secheaders), measured through the whole
// router the way a browser meets it: filex's pages say who may frame them,
// every answer is nosniff with a referrer that stops at the origin, and a
// script or a JSON answer is not given a framing policy it has no use for.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/printframe"
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

// ownFrames is what a page served at srv may frame of filex itself (security
// review UI-11): the app interfaces, the download frame and the print page
// (`ui.print`, #189), by path.
func ownFrames(srv *httptest.Server, interfaces bool) string {
	host := strings.TrimPrefix(srv.URL, "http://")
	if !interfaces {
		return host + "/z/ " + host + "/_print/"
	}
	return host + "/_appui/ " + host + "/z/ " + host + "/_print/"
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

// The print page (`ui.print`, #189) keeps its own policy - frames of the
// blob: PDF it made, nothing of filex - and says who may frame it: filex,
// the pages the operator allows to frame filex (an embed's site) and the
// desktop app, once, never any site (security review sec055 S9). Red before:
// the page carried no frame-ancestors, so every site could frame it.
func TestSecurityHeaders_ThePrintPageFramesOnlyItsOwnBlob(t *testing.T) {
	srv := newHeadersServer(t, []string{"https://home.example.com"})
	h := headersOf(t, srv.URL+"/_print/")
	csp := h.Get("Content-Security-Policy")
	assert.Contains(t, h.Get("Content-Type"), "text/html")
	assert.Equal(t, printframe.CSP+"; frame-ancestors 'self' https://home.example.com app://filex", csp)
	assert.Equal(t, 1, strings.Count(csp, "frame-ancestors"), "the middleware adds no second one")
	assert.NotContains(t, csp, "/_appui/")
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"))

	// With no list: filex and the desktop app only.
	srv = newHeadersServer(t, nil)
	assert.Equal(t, printframe.CSP+"; frame-ancestors 'self' app://filex", headersOf(t, srv.URL+"/_print/").Get("Content-Security-Policy"))
}

func TestSecurityHeaders_TheAppsPagesAreNotFramedByOtherSites(t *testing.T) {
	srv := newHeadersServer(t, nil)
	for _, page := range []string{"/admin/", "/admin/explore", "/drive/explore"} {
		h := headersOf(t, srv.URL+page)
		assert.Contains(t, h.Get("Content-Type"), "text/html", page)
		assert.Equal(t, "frame-ancestors 'self'; frame-src "+ownFrames(srv, true), h.Get("Content-Security-Policy"), page)
		assert.NotContains(t, h.Get("Content-Security-Policy"), "frame-src 'self'", page)
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
	assert.Equal(t, "frame-ancestors 'self' https://home.example.com https://*.example.org; frame-src "+ownFrames(srv, true), h.Get("Content-Security-Policy"))
}

// The editors the operator configured are frames of filex's pages, read from
// the live external_services rows (no restart after an edit in the panel); a
// service that is switched off is not.
func TestSecurityHeaders_PagesMayFrameTheConfiguredEditorsOnly(t *testing.T) {
	srv, _, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Embed = fakeBuild()
	})
	testutil.SeedAdmin(t, store)
	ctx := context.Background()
	require.NoError(t, store.UpsertExternalService(ctx, "drawio", true, "https://draw.example.com/drawio/", "", "", time.Time{}, ""))
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", false, "https://docs.example.com", "", "", time.Time{}, ""))
	// A row nothing reads any more (the iframe converter, removed in 0.48)
	// is never framed, enabled or not.
	require.NoError(t, store.UpsertExternalService(ctx, "convert", true, "https://convert.example.com", "", "", time.Time{}, ""))
	time.Sleep(1100 * time.Millisecond) // the resolver caches for a second
	h := headersOf(t, srv.URL+"/admin/")
	assert.Equal(t, "frame-ancestors 'self'; frame-src "+ownFrames(srv, true)+" https://draw.example.com", h.Get("Content-Security-Policy"))
}

// Behind a proxy that does not pass the Host on, the page's own host is not
// the one the browser used: the public URL's host is named too, so the
// download frame and the interfaces are not refused.
func TestSecurityHeaders_OwnFramesNameThePublicHostToo(t *testing.T) {
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.PublicURL = "https://files.example.com"
		c.PublicURLSet = true
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
	})
	testutil.SeedAdmin(t, store)
	h := headersOf(t, srv.URL+"/admin/")
	assert.Equal(t, "frame-ancestors 'self'; frame-src "+ownFrames(srv, true)+" files.example.com/_appui/ files.example.com/z/ files.example.com/_print/", h.Get("Content-Security-Policy"))
}

// FILEX_APP_UI_ORIGIN: filex's pages may frame the interface origin, and
// filex's own host no longer answers the interface route.
func TestSecurityHeaders_AnInterfaceOriginOfTheirOwnIsFramedFromThere(t *testing.T) {
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.AppUIOrigin = "https://apps.usercontent.example"
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
	})
	testutil.SeedAdmin(t, store)
	h := headersOf(t, srv.URL+"/admin/explore")
	assert.Equal(t, "frame-ancestors 'self'; frame-src "+ownFrames(srv, false)+" https://apps.usercontent.example", h.Get("Content-Security-Policy"),
		"the interfaces come from their own origin: filex's own /_appui/ is not framed")
	res, err := http.Get(srv.URL + "/_appui/probe/0123456789abcdef/index.html")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode, "filex's own host refuses the interface route")
}

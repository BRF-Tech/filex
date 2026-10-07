package api_test

// The files a browser fetches to install filex and to run its service worker,
// served the way it needs them (task #190: a phone was never offered to
// install filex as an app). A phone's install offer starts on the server: the
// manifest has to arrive as a manifest, the worker as JavaScript, and both have
// to be revalidated on every load - a worker script held in a cache for a year
// is an app that never updates, and a missing one answered with the SPA's HTML
// is a worker that fails to parse while every status code says fine.
//
// ⚠ The worker lives at /admin/sw.js and its scope is /admin/ - the directory
// it is served from, which is the widest scope a worker may have without a
// `Service-Worker-Allowed` header. None is sent, and none must be: the scope
// is narrow on purpose (web/vite.config.ts), and the page finds the worker by
// that scope wherever it stands (web/src/lib/browserNotify.ts).

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// pwaBuild is the fake build with the worker's neighbours added: the
// notification handlers it imports, workbox's runtime and an icon.
func pwaBuild() fstest.MapFS {
	b := fakeBuild()
	b["admin/notify-sw.js"] = &fstest.MapFile{Data: []byte(`self.addEventListener("push",function(){})`)}
	b["admin/workbox-2fbc6a65.js"] = &fstest.MapFile{Data: []byte(`define([],function(){})`)}
	b["admin/icons/icon-192.png"] = &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n")}
	return b
}

func TestPWA_TheInstallFilesAreServedAsABrowserNeedsThem(t *testing.T) {
	srv, _, _ := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.PublicURL = "http://test.local"
	}, func(d *api.Deps) {
		d.Embed = pwaBuild()
	})
	c := bpNoFollow(t)

	for _, tc := range []struct{ path, contentType string }{
		{"/admin/manifest.webmanifest", "application/manifest+json"},
		{"/admin/sw.js", "application/javascript; charset=utf-8"},
		{"/admin/notify-sw.js", "application/javascript; charset=utf-8"},
		{"/admin/workbox-2fbc6a65.js", "application/javascript; charset=utf-8"},
		{"/admin/icons/icon-192.png", "image/png"},
	} {
		resp, _ := bpGet(t, c, srv.URL+tc.path)
		require.Equal(t, http.StatusOK, resp.StatusCode, tc.path)
		assert.Equal(t, tc.contentType, resp.Header.Get("Content-Type"), tc.path)
		// Revalidated on every load: never the year-long `immutable` the
		// hashed assets/ files get, which would pin a worker and a manifest.
		assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"), tc.path)
	}

	resp, _ := bpGet(t, c, srv.URL+"/admin/sw.js")
	assert.Empty(t, resp.Header.Get("Service-Worker-Allowed"), "the worker's scope stays the directory it is served from")

	// A missing worker or manifest is a 404, never the SPA's index.html with
	// a 200: the browser would try to run HTML as the worker.
	for _, p := range []string{"/admin/sw-missing.js", "/admin/missing.webmanifest"} {
		resp, body := bpGet(t, c, srv.URL+p)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, p)
		assert.NotContains(t, body, "<!DOCTYPE html>", p)
	}

	// The non-admin's front door is the same document, so it links the same
	// manifest and is installable too (GitHub #14).
	resp, body := bpGet(t, c, srv.URL+"/drive/explore")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `<link rel="manifest" href="/admin/manifest.webmanifest">`)
}

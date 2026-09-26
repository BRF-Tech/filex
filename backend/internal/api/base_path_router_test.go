package api_test

// A sub-path deployment (FILEX_BASE_PATH, internal/basepath) measured through
// the whole router, the way a browser behind a proxy that passes the full
// path meets it: every route answers under the base and nowhere else, every
// redirect and cookie carries it, and the two documents that spell out their
// own address (index.html, the PWA manifest) are served rewritten for it —
// while the root deployment is served byte for byte as built.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// fakeIndex is shaped like the real build's index.html (web/dist): absolute
// /admin/ asset URLs, a manifest link, no base anywhere.
const fakeIndex = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/admin/favicon.svg" />
    <title>filex</title>
    <script type="module" crossorigin src="/admin/assets/index-AbC123.js"></script>
    <link rel="modulepreload" crossorigin href="/admin/assets/vue-vendor-XyZ.js">
    <link rel="stylesheet" crossorigin href="/admin/assets/index-Q1w2.css">
  <link rel="manifest" href="/admin/manifest.webmanifest"></head>
  <body><div id="app"></div></body>
</html>
`

// fakeManifest is the real build's manifest, as vite-plugin-pwa writes it.
const fakeManifest = `{"name":"filex — File Manager","short_name":"filex","start_url":"/admin/","display":"standalone","background_color":"#0a0a0a","lang":"en","scope":"/","id":"/admin/","icons":[{"src":"icons/icon.svg","sizes":"any","type":"image/svg+xml","purpose":"any"}]}`

const fakeJS = `import("./Lazy-1.js");export const x=new URL("../assets/logo.svg",import.meta.url).href;`

func fakeBuild() fstest.MapFS {
	return fstest.MapFS{
		"admin/index.html":             {Data: []byte(fakeIndex)},
		"admin/manifest.webmanifest":   {Data: []byte(fakeManifest)},
		"admin/assets/index-AbC123.js": {Data: []byte(fakeJS)},
		"admin/sw.js":                  {Data: []byte(`self.x=1`)},
		"web/filex.js":                 {Data: []byte(`export {}`)},
		"web/Chunk-Ab12.js":            {Data: []byte(`export const c=1`)},
	}
}

func newBaseServer(t *testing.T, base, publicURL string) *httptest.Server {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.BasePath = base
		c.PublicURL = publicURL
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
	})
	testutil.SeedAdmin(t, store)
	return srv
}

// bpNoFollow is a client that shows redirects instead of following them.
func bpNoFollow(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func bpGet(t *testing.T, c *http.Client, url string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func bpLogin(t *testing.T, c *http.Client, url string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": "admin@test.local", "password": "TestAdminPass!1"})
	resp, err := c.Post(url, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "login at %s", url)
	for _, ck := range resp.Cookies() {
		if ck.Name == authlocal.SessionCookieName {
			return ck
		}
	}
	t.Fatalf("no session cookie from %s", url)
	return nil
}

func TestBasePath_TheRouterAnswersUnderTheBaseAndNowhereElse(t *testing.T) {
	srv := newBaseServer(t, "/filex", "http://test.local/filex")
	c := bpNoFollow(t)

	// Signing in happens under the base, and the session is scoped to it.
	ck := bpLogin(t, c, srv.URL+"/filex/api/auth/login")
	assert.Equal(t, "/filex", ck.Path, "the session cookie belongs to the base, not to the whole host")

	resp, _ := bpGet(t, c, srv.URL+"/filex/api/auth/me")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"), "APINoStore still sees an /api path under the base")

	// Nothing outside the base reaches a handler — signed in or not.
	for _, p := range []string{"/api/auth/me", "/api/files/capabilities", "/admin/", "/drive/", "/", "/embed.js", "/dav/", "/s3/", "/metrics", "/filexx/api/auth/me"} {
		resp, _ := bpGet(t, c, srv.URL+p)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "%s is outside the base", p)
	}
	resp, _ = bpGet(t, c, srv.URL+"/filex%2Fapi/auth/me")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "an escaped slash cannot end the prefix")

	// /healthz answers at both addresses (container health checks).
	for _, p := range []string{"/healthz", "/filex/healthz"} {
		resp, body := bpGet(t, c, srv.URL+p)
		assert.Equal(t, http.StatusOK, resp.StatusCode, p)
		assert.Contains(t, body, `"ok"`, p)
	}

	// Every redirect the server sends stays under the base.
	for from, to := range map[string]string{
		"/filex":        "/filex/",
		"/filex/":       "/filex/admin/",
		"/filex/admin":  "/filex/admin/",
		"/filex/drive":  "/filex/drive/",
		"/filex/p/tok1": "/filex/s/tok1",
	} {
		resp, _ := bpGet(t, c, srv.URL+from)
		assert.Contains(t, []int{http.StatusFound, http.StatusMovedPermanently}, resp.StatusCode, from)
		assert.Equal(t, to, resp.Header.Get("Location"), from)
	}
	resp, _ = bpGet(t, c, srv.URL+"/filex/api/p/tok1/view")
	assert.Equal(t, "/filex/api/public/s/tok1", resp.Header.Get("Location"))
}

func TestBasePath_TheWebAppIsServedForTheBase(t *testing.T) {
	srv := newBaseServer(t, "/filex", "http://test.local/filex")
	c := bpNoFollow(t)

	for _, p := range []string{"/filex/admin/", "/filex/admin/storages/3", "/filex/drive/", "/filex/drive/explore", "/filex/files/edit?path=a"} {
		resp, body := bpGet(t, c, srv.URL+p)
		require.Equal(t, http.StatusOK, resp.StatusCode, p)
		assert.Contains(t, body, `<meta name="filex-base" content="/filex" />`, "%s: the app is told its base", p)
		assert.Contains(t, body, `src="/filex/admin/assets/index-AbC123.js"`, p)
		assert.Contains(t, body, `href="/filex/admin/assets/index-Q1w2.css"`, p)
		assert.Contains(t, body, `href="/filex/admin/manifest.webmanifest"`, p)
		assert.Contains(t, body, `href="/filex/admin/favicon.svg"`, p)
		assert.NotContains(t, body, `="/admin/`, "%s: no URL is left pointing at the host's root", p)
	}

	// The public link shell (a JavaScript browser opening /s/<token>) is the
	// same document.
	for _, p := range []string{"/filex/s/some-token", "/filex/d/some-token"} {
		resp, body := bpGet(t, c, srv.URL+p, "Accept", "text/html")
		require.Equal(t, http.StatusOK, resp.StatusCode, p)
		assert.Contains(t, body, `<div id="app">`, p)
		assert.Contains(t, body, `<meta name="filex-base" content="/filex" />`, p)
		assert.NotContains(t, body, `="/admin/`, p)
	}

	// The manifest moves under the base: identity, start and scope.
	resp, body := bpGet(t, c, srv.URL+"/filex/admin/manifest.webmanifest")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/manifest+json", resp.Header.Get("Content-Type"))
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m))
	assert.Equal(t, "/filex/admin/", m["id"])
	assert.Equal(t, "/filex/admin/", m["start_url"])
	assert.Equal(t, "/filex/", m["scope"])
	assert.Equal(t, "filex — File Manager", m["name"], "the rest travels unchanged")

	// Everything else is served byte for byte: one build, any base.
	resp, body = bpGet(t, c, srv.URL+"/filex/admin/assets/index-AbC123.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, fakeJS, body)
	resp, _ = bpGet(t, c, srv.URL+"/filex/embed.js")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = bpGet(t, c, srv.URL+"/filex/Chunk-Ab12.js")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the web component's chunks resolve next to /filex/embed.js")
}

// The root deployment — no base — is what it was before the setting existed.
func TestBasePath_TheRootDeploymentIsUnchanged(t *testing.T) {
	srv := newBaseServer(t, "", "http://test.local")
	c := bpNoFollow(t)

	ck := bpLogin(t, c, srv.URL+"/api/auth/login")
	assert.Equal(t, "/", ck.Path)

	resp, body := bpGet(t, c, srv.URL+"/admin/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, fakeIndex, body, "index.html is served exactly as built")
	resp, body = bpGet(t, c, srv.URL+"/admin/manifest.webmanifest")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, fakeManifest, body, "the manifest — the installed app's identity — is served exactly as built")

	for from, to := range map[string]string{"/": "/admin/", "/admin": "/admin/", "/drive": "/drive/", "/p/tok1": "/s/tok1"} {
		resp, _ := bpGet(t, c, srv.URL+from)
		assert.Equal(t, to, resp.Header.Get("Location"), from)
	}
	resp, _ = bpGet(t, c, srv.URL+"/filex/api/auth/me")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "no base, no prefix to strip")
}

// A dedicated S3 host (FILEX_S3_DOMAIN) is served at ITS root, never under the
// app's base: every S3 client talks to `/` and `/bucket/key` of its endpoint.
func TestBasePath_AnS3HostIsServedAtItsOwnRoot(t *testing.T) {
	srv, _, _ := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.BasePath = "/filex"
		c.PublicURL = "http://test.local/filex"
		c.S3.Enabled = true
		c.S3.Domain = "s3.test.local"
	}, nil)
	c := bpNoFollow(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Host = "s3.test.local"
	resp, err := c.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.NotEqual(t, http.StatusNotFound, resp.StatusCode, "the S3 host is not outside the app's base")
	assert.Contains(t, string(body), "<Error>", "an unsigned ListBuckets is the S3 endpoint's own refusal")

	// …and the app's host still keeps everything under the base.
	resp, _ = bpGet(t, c, srv.URL+"/api/files/capabilities")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

package basepath_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/basepath"
)

func TestNormalize(t *testing.T) {
	good := map[string]string{
		"":              "",
		"/":             "",
		"  ":            "",
		"/filex":        "/filex",
		" /filex ":      "/filex",
		"/apps/filex":   "/apps/filex",
		"/a-b.c_d~e":    "/a-b.c_d~e",
		"/Files2":       "/Files2",
		"/apps/v1.2/fx": "/apps/v1.2/fx",
	}
	for in, want := range good {
		got, err := basepath.Normalize(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	bad := []string{
		"filex",        // relative
		"/filex/",      // trailing slash
		"//filex",      // empty segment
		"/a//b",        // empty segment
		"/../filex",    // climbs out
		"/filex/..",    // climbs out
		"/./filex",     // dot segment
		"/fi lex",      // needs escaping
		"/fil%65x",     // already escaped
		"/filex?x=1",   // a query is not a path
		"/filex#frag",  // nor is a fragment
		"/filéx",       // non-ASCII
		`/a\b`,         // backslash
		"/filex;param", // matrix parameter
	}
	for _, in := range bad {
		_, err := basepath.Normalize(in)
		assert.Error(t, err, "%q must be refused", in)
	}
}

func TestFromURL(t *testing.T) {
	good := map[string]string{
		"":                               "",
		"http://localhost:5212":          "",
		"https://example.com/":           "",
		"https://example.com/filex":      "/filex",
		"https://example.com/filex/":     "/filex",
		"https://example.com/apps/filex": "/apps/filex",
		"not a url":                      "",
		"/filex":                         "", // not absolute: no host, nothing to derive
	}
	for in, want := range good {
		got, err := basepath.FromURL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"https://example.com/fi%20lex", "https://example.com/a//b", "https://example.com/../x"} {
		_, err := basepath.FromURL(in)
		assert.Error(t, err, "%q must be refused", in)
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", basepath.From(ctx))
	assert.Equal(t, "/", basepath.CookiePath(ctx))
	assert.Equal(t, "/admin/", basepath.Path(ctx, "/admin/"))

	ctx = basepath.With(ctx, "/filex")
	assert.Equal(t, "/filex", basepath.From(ctx))
	assert.Equal(t, "/filex", basepath.CookiePath(ctx))
	assert.Equal(t, "/filex/admin/", basepath.Path(ctx, "/admin/"))

	// With("") is the root: nothing on the context.
	assert.Equal(t, "", basepath.From(basepath.With(context.Background(), "")))
}

func TestStrip(t *testing.T) {
	cases := []struct {
		base, in, want string
		ok             bool
	}{
		{"", "/dav/x", "/dav/x", true},
		{"/filex", "/filex/dav/x", "/dav/x", true},
		{"/filex", "/filex", "/", true},
		{"/filex", "/dav/x", "", false},
		{"/filex", "/filexdav/x", "", false},
		{"/filex", "/other/filex/dav", "", false},
	}
	for _, c := range cases {
		got, ok := basepath.Strip(c.base, c.in)
		assert.Equal(t, c.ok, ok, "%s %s", c.base, c.in)
		assert.Equal(t, c.want, got, "%s %s", c.base, c.in)
	}
}

// seen records what the wrapped handler received.
type seen struct {
	called  bool
	path    string
	rawPath string
	base    string
}

func serve(t *testing.T, base, target string) (*httptest.ResponseRecorder, *seen) {
	t.Helper()
	s := &seen{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.called = true
		s.path = r.URL.Path
		s.rawPath = r.URL.RawPath
		s.base = basepath.From(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	basepath.Middleware(base)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec, s
}

func TestMiddlewareAtRootIsTheRouterItself(t *testing.T) {
	for _, target := range []string{"/", "/api/files/manager", "/filex/api/x", "/healthz"} {
		rec, s := serve(t, "", target)
		require.True(t, s.called, target)
		assert.Equal(t, http.StatusTeapot, rec.Code, target)
		assert.Equal(t, "", s.base, target)
	}
	_, s := serve(t, "", "/api/files/manager")
	assert.Equal(t, "/api/files/manager", s.path)
}

func TestMiddlewareUnderABase(t *testing.T) {
	// Inside the base: the router sees the path it always read, and the base
	// is on the context.
	rec, s := serve(t, "/filex", "/filex/api/files/manager?action=index")
	require.True(t, s.called)
	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, "/api/files/manager", s.path)
	assert.Equal(t, "/filex", s.base)

	_, s = serve(t, "/filex", "/filex/")
	assert.Equal(t, "/", s.path)

	_, s = serve(t, "/apps/filex", "/apps/filex/admin/x")
	assert.Equal(t, "/admin/x", s.path)
	assert.Equal(t, "/apps/filex", s.base)

	// The escaped path keeps its escapes, minus the base.
	_, s = serve(t, "/filex", "/filex/dav/a%2Fb")
	require.True(t, s.called)
	assert.Equal(t, "/dav/a/b", s.path)
	assert.Equal(t, "/dav/a%2Fb", s.rawPath)

	// The bare base is a directory.
	rec, s = serve(t, "/filex", "/filex")
	assert.False(t, s.called)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/filex/", rec.Header().Get("Location"))
	rec, _ = serve(t, "/filex", "/filex?x=1")
	assert.Equal(t, "/filex/?x=1", rec.Header().Get("Location"))

	// /healthz answers at the root too, and only /healthz.
	rec, s = serve(t, "/filex", "/healthz")
	require.True(t, s.called)
	assert.Equal(t, http.StatusTeapot, rec.Code)
	assert.Equal(t, "/healthz", s.path)
	assert.Equal(t, "", s.base)
	_, s = serve(t, "/filex", "/filex/healthz")
	assert.Equal(t, "/healthz", s.path)
	assert.Equal(t, "/filex", s.base)
}

func TestMiddlewareRefusesEverythingOutsideTheBase(t *testing.T) {
	for _, target := range []string{
		"/",
		"/api/files/manager",
		"/api/auth/login",
		"/admin/",
		"/dav/",
		"/s3/",
		"/embed.js",
		"/metrics",
		"/healthz/x",
		"/filexx/api/files/manager", // shares the prefix's letters, not the prefix
		"/other/filex/api/x",
		"/fil%65x/api/files/manager", // the decoded path matches, the escaped one does not
		"/filex%2Fapi/files/manager", // an escaped slash where the prefix ends
		"/FILEX/api/files/manager",   // paths are case-sensitive
	} {
		rec, s := serve(t, "/filex", target)
		assert.False(t, s.called, "%s must not reach the router", target)
		assert.Equal(t, http.StatusNotFound, rec.Code, target)
	}
}

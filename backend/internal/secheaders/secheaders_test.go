package secheaders

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serve(t *testing.T, allowed []string, h http.HandlerFunc) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	Middleware(allowed)(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	res := rec.Result()
	_, _ = io.Copy(io.Discard, res.Body)
	return res
}

func TestEveryAnswerIsNotSniffedAndKeepsItsPathToItself(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"json": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		},
		"pdf": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.7"))
		},
		"no body": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	} {
		res := serve(t, nil, h)
		assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"), name)
		assert.Equal(t, "strict-origin-when-cross-origin", res.Header.Get("Referrer-Policy"), name)
	}
}

func TestPagesSayWhoMayFrameThem(t *testing.T) {
	page := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>filex</title>"))
	}
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self'", serve(t, nil, page).Header.Get("Content-Security-Policy"))
	assert.Equal(t, "frame-ancestors 'self' https://home.example.com https://*.example.org; frame-src 'self'",
		serve(t, []string{"https://home.example.com", "https://*.example.org"}, page).Header.Get("Content-Security-Policy"))
}

func TestAPageWithoutAContentTypeIsStillAPage(t *testing.T) {
	res := serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>wait</body></html>"))
	})
	assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self'", res.Header.Get("Content-Security-Policy"))
}

// A file's own bytes are framed by design: a PDF in the explorer embedded on
// another site's page, a download through a hidden frame.
func TestAFileBodyIsNotAPage(t *testing.T) {
	for _, ct := range []string{"application/pdf", "image/png", "application/octet-stream", "text/plain; charset=utf-8", "image/svg+xml"} {
		res := serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(http.StatusOK)
		})
		assert.Empty(t, res.Header.Get("Content-Security-Policy"), ct)
	}
}

func TestAHandlersOwnPolicyIsKept(t *testing.T) {
	res := serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.WriteHeader(http.StatusOK)
	})
	assert.Equal(t, "sandbox; default-src 'none'; frame-ancestors 'self'", res.Header.Get("Content-Security-Policy"))

	res = serve(t, []string{"https://home.example.com"}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.WriteHeader(http.StatusOK)
	})
	assert.Equal(t, "frame-ancestors 'none'", res.Header.Get("Content-Security-Policy"), "a handler that decided is not overruled")

	res = serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusOK)
	})
	assert.Equal(t, "no-referrer", res.Header.Get("Referrer-Policy"))
}

func TestAFlushDecidesBeforeTheHeadersGo(t *testing.T) {
	res := serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		require.NoError(t, http.NewResponseController(w).Flush())
		_, _ = w.Write([]byte("<p>streamed</p>"))
	})
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self'", res.Header.Get("Content-Security-Policy"))
}

// The frames a filex page may hold (M0 of the app-interface work, 2026-09-27):
// filex itself, the external editors the operator configured and the origin
// app interfaces are served from. Never data: or blob: -- measured, a sandboxed
// app frame that navigates ITSELF to a data:/blob: document gets a fresh realm
// without the bootstrap that takes WebRTC away, and in Firefox the host's
// frame-src is the only thing that stops that navigation.
func TestPagesSayWhatTheyMayFrame(t *testing.T) {
	page := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>filex</title>"))
	}
	frames := func(*http.Request) []string {
		return []string{"https://draw.example.com/drawio/", "https://docs.example.com", "https://draw.example.com",
			"data:", "blob:", "*", "'unsafe-inline'", "https://evil.example;script-src *", "/relative/drawio", ""}
	}
	rec := httptest.NewRecorder()
	Middleware(nil, WithFrameSources(frames))(http.HandlerFunc(page)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self' https://draw.example.com https://docs.example.com",
		rec.Result().Header.Get("Content-Security-Policy"))
}

// Security review UI-11: filex's own frames are named by PATH — the app
// interfaces (/_appui/) and the download frame (/z/) — never 'self', which
// would let an app's frame navigate itself to any page of filex. Entries not
// of that plain host/path/ form are left out; with none left, 'self'.
func TestPagesFrameOnlyTheirOwnPaths(t *testing.T) {
	page := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>filex</title>"))
	}
	serveWith := func(own []string) string {
		rec := httptest.NewRecorder()
		Middleware(nil,
			WithOwnFrames(func(*http.Request) []string { return own }),
			WithFrameSources(func(*http.Request) []string { return []string{"https://draw.example.com"} }),
		)(http.HandlerFunc(page)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Result().Header.Get("Content-Security-Policy")
	}
	assert.Equal(t, "frame-ancestors 'self'; frame-src files.example.com/_appui/ files.example.com:8443/filex/z/ [::1]:8080/z/ https://draw.example.com",
		serveWith([]string{"files.example.com/_appui/", "files.example.com:8443/filex/z/", "[::1]:8080/z/", "files.example.com/_appui/",
			"'self'", "*/_appui/", "files.example.com/nopath", "https://files.example.com/_appui/", "files.example.com/a b/", "files.example.com/_appui/;script-src *", "files.example.com/../"}))
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self' https://draw.example.com", serveWith(nil))
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self' https://draw.example.com", serveWith([]string{"*", "'self'"}))
}

// A handler that wrote its own policy decides its own frames: appending a
// frame-src there could only WIDEN it (a default-src 'none' page would gain
// 'self' frames).
func TestAHandlersOwnPolicyGetsNoFrameSources(t *testing.T) {
	rec := httptest.NewRecorder()
	Middleware(nil, WithFrameSources(func(*http.Request) []string { return []string{"https://draw.example.com"} }))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, "default-src 'none'; frame-ancestors 'self'", rec.Result().Header.Get("Content-Security-Policy"))
}

func TestTheConnectionUnderneathIsReachable(t *testing.T) {
	var reached bool
	Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, reached = w.(interface{ Unwrap() http.ResponseWriter })
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.True(t, reached, "http.ResponseController (and the WebSocket upgrade) must be able to unwrap the writer")
}

func TestNormalize(t *testing.T) {
	got, err := Normalize([]string{" https://Home.Example.com ", "https://home.example.com/", "http://10.0.0.5:7575", "https://*.example.org", ""})
	require.NoError(t, err)
	assert.Equal(t, []string{"https://home.example.com", "http://10.0.0.5:7575", "https://*.example.org"}, got)

	got, err = Normalize([]string{"*"})
	require.NoError(t, err)
	assert.Equal(t, []string{"*"}, got)

	for _, bad := range []string{
		"home.example.com",              // no scheme
		"'self'",                        // implied, and a keyword is not an origin
		"'none'",                        // would lock filex out of its own frames
		"ftp://home.example.com",        // not a page
		"https://home.example.com/dash", // a path
		"https://home.example.com;x",    // would break out of the directive
		"https://a*.example.com",        // not a whole leading label
		"https://user@home.example.com", // a user
	} {
		_, err := Normalize([]string{bad})
		assert.Error(t, err, bad)
	}
}

func TestSplit(t *testing.T) {
	assert.Equal(t, []string{"https://a.example", "https://b.example", "https://c.example"},
		Split(" https://a.example, https://b.example  https://c.example,"))
}

// An app interface's page names no frame-ancestors on purpose: the interface
// (a sandboxed, opaque origin) frames pages of its own package, and no source
// expression matches an opaque origin - Chromium refused such a page under
// `frame-ancestors *`. OpenFraming keeps the handler's policy as it wrote
// it; without it, or on a page with no policy of its own, the middleware
// still adds filex's own.
func TestOpenFraming_KeepsAPagesPolicyWithoutFrameAncestors(t *testing.T) {
	const own = "default-src 'none'; frame-src https://files.example.com/_appui/app/0123456789abcdef/; sandbox allow-scripts"
	page := func(open bool, policy string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if policy != "" {
				w.Header().Set("Content-Security-Policy", policy)
			}
			if open {
				OpenFraming(r)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><p>app"))
		}
	}
	assert.Equal(t, own, serve(t, nil, page(true, own)).Header.Get("Content-Security-Policy"), "opened: no frame-ancestors added")
	assert.Equal(t, own+"; frame-ancestors 'self'", serve(t, nil, page(false, own)).Header.Get("Content-Security-Policy"), "not opened: filex's own is added")
	assert.Equal(t, "frame-ancestors 'self'; frame-src 'self'", serve(t, nil, page(true, "")).Header.Get("Content-Security-Policy"),
		"a page without a policy of its own is filex's page: it gets filex's policy whatever it says")

	// Outside the middleware it is a no-op, never a panic.
	rec := httptest.NewRecorder()
	page(true, own).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, own, rec.Result().Header.Get("Content-Security-Policy"))
}

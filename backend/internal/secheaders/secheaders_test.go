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
	assert.Equal(t, "frame-ancestors 'self'", serve(t, nil, page).Header.Get("Content-Security-Policy"))
	assert.Equal(t, "frame-ancestors 'self' https://home.example.com https://*.example.org",
		serve(t, []string{"https://home.example.com", "https://*.example.org"}, page).Header.Get("Content-Security-Policy"))
}

func TestAPageWithoutAContentTypeIsStillAPage(t *testing.T) {
	res := serve(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>wait</body></html>"))
	})
	assert.Contains(t, res.Header.Get("Content-Type"), "text/html")
	assert.Equal(t, "frame-ancestors 'self'", res.Header.Get("Content-Security-Policy"))
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
	assert.Equal(t, "frame-ancestors 'self'", res.Header.Get("Content-Security-Policy"))
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

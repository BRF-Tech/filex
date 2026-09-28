package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The origin an interface's policy names as its own package: the Host filex
// answered on, the scheme the browser used (a TLS proxy says so), and the base
// path. A Host that could end a policy directive is not served.
func TestAppUIOrigin(t *testing.T) {
	o := appUIOrigin("/filex")
	r := httptest.NewRequest("GET", "http://Files.Example.com:8443/_appui/x/0123456789abcdef/index.html", nil)
	got, ok := o(r)
	assert.True(t, ok)
	assert.Equal(t, "http://files.example.com:8443/filex", got)

	r.Header.Set("X-Forwarded-Proto", "https")
	got, _ = o(r)
	assert.Equal(t, "https://files.example.com:8443/filex", got)

	r = httptest.NewRequest("GET", "https://files.example.com/_appui/x/0123456789abcdef/index.html", nil)
	r.TLS = &tls.ConnectionState{}
	got, _ = appUIOrigin("")(r)
	assert.Equal(t, "https://files.example.com", got)

	for _, host := range []string{"evil.example;script-src *", "a b", "x'y", "", "evil.example/path"} {
		r.Host = host
		_, ok := o(r)
		assert.False(t, ok, "%q", host)
	}
}

// FILEX_APP_UI_ORIGIN: the interface host answers the interface route (and a
// health probe) and nothing else; every other host refuses the interface
// route. Under a base path too.
func TestAppUIHostSplit(t *testing.T) {
	for _, base := range []string{"", "/filex"} {
		reached := ""
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r.Host + r.URL.Path })
		h := appUIHostSplit("https://apps.usercontent.example", base)(next)
		for _, c := range []struct {
			host, path string
			ok         bool
		}{
			{"apps.usercontent.example", base + "/_appui/probe/0123456789abcdef/index.html", true},
			{"APPS.usercontent.example", base + "/_appui/probe/0123456789abcdef/index.html", true},
			{"apps.usercontent.example", base + "/healthz", true},
			{"apps.usercontent.example", base + "/admin/", false},
			{"apps.usercontent.example", base + "/api/auth/whoami", false},
			{"apps.usercontent.example", base + "/s/sharetoken", false},
			{"apps.usercontent.example", "/", false},
			{"files.example.com", base + "/_appui/probe/0123456789abcdef/index.html", false},
			{"files.example.com", base + "/admin/", true},
			{"files.example.com", base + "/api/auth/whoami", true},
		} {
			reached = ""
			rec := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "http://"+c.host+c.path, nil)
			h.ServeHTTP(rec, r)
			if c.ok {
				assert.Equal(t, c.host+c.path, reached, "%s%s", c.host, c.path)
			} else {
				assert.Equal(t, http.StatusNotFound, rec.Code, "%s%s", c.host, c.path)
				assert.Empty(t, reached, "%s%s", c.host, c.path)
			}
		}
	}
}

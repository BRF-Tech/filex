package api_test

// The document server's origin is usually the SAME SITE as filex
// (docs.example.com beside files.example.com), and with the editor's frame
// living there (task #92) code filex did not write runs on it. SameSite=Lax
// does not keep the session cookie off a same-site request, so two things must
// hold whatever FILEX_CORS_ALLOWED_ORIGINS says:
//
//   - a state-changing request from that origin is refused (the origin guard:
//     Sec-Fetch-Site same-site is not trust, and the frame origin is
//     Untrusted even when a CORS wildcard covers it);
//   - no answer is readable from there (no CORS headers for it, preflight
//     included).
//
// The app-interface origin is held to the same. Red before #92: the wildcard
// cases (a CORS entry `https://*.example.com` trusted the document server's
// host for writes and reflected it for reads).

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
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func newSameSiteServer(t *testing.T, cors []string) *httptest.Server {
	t.Helper()
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.PublicURL = "https://files.example.com"
		c.PublicURLSet = true
		c.CORS.AllowedOrigins = cors
		c.ExternalServices.OnlyOffice.FrameOrigin = "https://docs.example.com"
		c.AppUIOrigin = "https://apps.usercontent.example"
	}, func(d *api.Deps) {
		d.Embed = fakeBuild()
	})
	testutil.SeedAdmin(t, store)
	return srv
}

// sameSiteWrite sends a state-changing request the way a script on origin would,
// with filex's session cookie riding along (same site).
func sameSiteWrite(t *testing.T, srv *httptest.Server, origin, site string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/files/copy", strings.NewReader(`{"sources":["main://a"],"destination":"main://b"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Sec-Fetch-Site", site)
	req.AddCookie(&http.Cookie{Name: authlocal.SessionCookieName, Value: "a-session-token"})
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	buf := new(strings.Builder)
	_, _ = io.Copy(buf, res.Body)
	return res.StatusCode, buf.String()
}

func TestSameSite_TheFrameOriginCannotWriteWithTheSession(t *testing.T) {
	for _, cors := range [][]string{{"*"}, {"https://*.example.com"}, {"https://docs.example.com", "https://apps.usercontent.example"}} {
		srv := newSameSiteServer(t, cors)
		for _, c := range []struct{ origin, site string }{
			{"https://docs.example.com", "same-site"},
			{"https://docs.example.com", ""},
			{"https://apps.usercontent.example", "cross-site"},
		} {
			code, body := sameSiteWrite(t, srv, c.origin, c.site)
			assert.Equal(t, http.StatusForbidden, code, "CORS %v: %s (%q)", cors, c.origin, c.site)
			assert.Contains(t, body, "cross_origin_refused", "CORS %v: %s (%q)", cors, c.origin, c.site)
		}
	}
}

// The control: another origin the operator's wildcard covers is still trusted
// (the guard lets it through, and the missing session answers instead).
func TestSameSite_AnOperatorsOtherOriginsStayTrusted(t *testing.T) {
	srv := newSameSiteServer(t, []string{"https://*.example.com"})
	code, body := sameSiteWrite(t, srv, "https://dash.example.com", "same-site")
	assert.NotContains(t, body, "cross_origin_refused")
	assert.NotEqual(t, http.StatusOK, code, "no real session: whatever the route says, not a success")
}

func corsGet(t *testing.T, srv *httptest.Server, method, origin string) http.Header {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+"/api/capabilities", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	return res.Header
}

func TestSameSite_NoAnswerIsReadableFromTheFrameOrigin(t *testing.T) {
	for _, cors := range [][]string{{"*"}, {"https://*.example.com"}, {"https://docs.example.com"}} {
		srv := newSameSiteServer(t, cors)
		for _, origin := range []string{"https://docs.example.com", "https://DOCS.example.com:443", "https://apps.usercontent.example"} {
			for _, method := range []string{http.MethodGet, http.MethodOptions} {
				h := corsGet(t, srv, method, origin)
				assert.Empty(t, h.Get("Access-Control-Allow-Origin"), "CORS %v: %s %s", cors, method, origin)
				assert.Empty(t, h.Get("Access-Control-Allow-Credentials"), "CORS %v: %s %s", cors, method, origin)
			}
		}
	}
	// The control: the operator's wildcard still answers its other origins.
	srv := newSameSiteServer(t, []string{"https://*.example.com"})
	assert.Equal(t, "https://dash.example.com", corsGet(t, srv, http.MethodGet, "https://dash.example.com").Get("Access-Control-Allow-Origin"))
}

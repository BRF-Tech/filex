package originguard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/cors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

const host = "files.example.com"

// req builds a request to filex at `host`. Every case carries the session
// cookie unless it says otherwise: that is the request the guard is for.
func req(method, path string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader("{}"))
	r.Host = host
	r.AddCookie(&http.Cookie{Name: "filex_session", Value: "sess-1"})
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
			continue
		}
		r.Header.Set(k, v)
	}
	return r
}

func guard(trusted ...string) *Guard {
	return New(Config{
		Trusted:       trusted,
		Self:          "https://platform.example.com/filex",
		Exempt:        []string{"/api/public", "/s", "/d", "/u", "/api/files/onlyoffice/callback", "/api/auth/desktop/exchange", "/s3"},
		SessionCookie: "filex_session",
	})
}

func TestJudge(t *testing.T) {
	type tc struct {
		name    string
		method  string
		path    string
		hdr     map[string]string
		trusted []string
		reason  string // "" = passes
	}
	evil := "https://evil.example.net"
	sibling := "https://blog.example.com"
	cases := []tc{
		// Fetch Metadata first.
		{name: "same-origin page", hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://" + host}},
		{name: "same-origin even when Origin says null", hdr: map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "null"}},
		{name: "user-initiated (none)", hdr: map[string]string{"Sec-Fetch-Site": "none"}},
		{name: "cross-site page", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "sibling host is same-site, still refused", hdr: map[string]string{"Sec-Fetch-Site": "same-site", "Origin": sibling}, reason: ReasonSameSite},
		{name: "an unknown Sec-Fetch-Site value is not same-origin", hdr: map[string]string{"Sec-Fetch-Site": "sideways", "Origin": evil}, reason: ReasonCrossSite},
		{name: "same-site, Origin missing, Referer names it", hdr: map[string]string{"Sec-Fetch-Site": "same-site", "Referer": sibling + "/post/1"}, reason: ReasonSameSite},
		{name: "cross-site, no origin at all", hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}, reason: ReasonCrossSite},
		// The CORS list is the one place that names other origins.
		{name: "embed on the CORS list", trusted: []string{"https://work.example.org"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://work.example.org"}},
		{name: "CORS entry matches case-insensitively", trusted: []string{"HTTPS://Work.Example.org"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://work.example.org"}},
		{name: "CORS entry with a default port", trusted: []string{"https://work.example.org:443"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://work.example.org"}},
		{name: "CORS wildcard pattern", trusted: []string{"https://*.example.com"}, hdr: map[string]string{"Sec-Fetch-Site": "same-site", "Origin": sibling}},
		{name: "CORS wildcard does not cover another domain", trusted: []string{"https://*.example.com"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "CORS `*` trusts nobody for writes", trusted: []string{"*"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "CORS `*` among others still trusts only the others", trusted: []string{"*", "https://work.example.org"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://work.example.org"}},
		{name: "http twin of a trusted https origin", trusted: []string{"https://work.example.org"}, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://work.example.org"}, reason: ReasonCrossSite},
		{name: "filex's own public address (platform page to a tenant host)", hdr: map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://platform.example.com"}},
		// Older browsers: Origin, then Referer.
		{name: "legacy: Origin is this host", hdr: map[string]string{"Origin": "https://" + host}},
		{name: "legacy: Origin is this host, default port spelled out", hdr: map[string]string{"Origin": "https://" + host + ":443"}},
		{name: "legacy: Origin on another port", hdr: map[string]string{"Origin": "https://" + host + ":8443"}, reason: ReasonForeignOrigin},
		{name: "legacy: Origin elsewhere", hdr: map[string]string{"Origin": evil}, reason: ReasonForeignOrigin},
		{name: "legacy: Origin null (sandboxed frame, file://, privacy redirect)", hdr: map[string]string{"Origin": "null"}, reason: ReasonNullOrigin},
		{name: "legacy: Referer from this host", hdr: map[string]string{"Referer": "https://" + host + "/drive/explore"}},
		{name: "legacy: Referer elsewhere", hdr: map[string]string{"Referer": evil + "/x"}, reason: ReasonForeignOrigin},
		{name: "legacy: trusted origin", trusted: []string{"https://work.example.org"}, hdr: map[string]string{"Origin": "https://work.example.org"}},
		// Neither header: not a browser.
		{name: "no Origin, no Referer, no Sec-Fetch-Site (curl, scripts)", hdr: nil},
		// A credential a page must attach on purpose.
		{name: "Bearer from another origin", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil, "Authorization": "Bearer abc"}},
		{name: "desktop app (app:// origin, Bearer)", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "app://filex", "Authorization": "Bearer abc"}},
		{name: "API key header", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil, "X-Filex-Token": "fx_abc"}},
		{name: "lower-case bearer is not what the drivers read", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil, "Authorization": "bearer abc"}, reason: ReasonCrossSite},
		{name: "empty Bearer is no credential", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil, "Authorization": "Bearer  "}, reason: ReasonCrossSite},
		{name: "Basic is not exempt (a browser may replay it)", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil, "Authorization": "Basic YTpi"}, reason: ReasonCrossSite},
		{name: "app:// without a Bearer", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "app://filex"}, reason: ReasonCrossSite},
		{name: "file:// page", hdr: map[string]string{"Origin": "file://"}, reason: ReasonForeignOrigin},
		// Methods.
		{name: "GET is never judged", method: http.MethodGet, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "HEAD is never judged", method: http.MethodHead, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "OPTIONS (preflight) is never judged", method: http.MethodOptions, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "PUT", method: http.MethodPut, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "PATCH", method: http.MethodPatch, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "DELETE", method: http.MethodDelete, hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "WebDAV PROPPATCH", method: "PROPPATCH", path: "/dav/a.txt", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "WebDAV client (no browser headers)", method: "PUT", path: "/dav/a.txt", hdr: nil},
		// WebSocket upgrades are GETs that act with the session.
		{name: "WebSocket from another site", method: http.MethodGet, path: "/api/ws", hdr: map[string]string{"Upgrade": "websocket", "Sec-Fetch-Site": "same-site", "Origin": sibling}, reason: ReasonSameSite},
		{name: "WebSocket, legacy Origin elsewhere", method: http.MethodGet, path: "/api/ws", hdr: map[string]string{"Upgrade": "WebSocket", "Origin": evil}, reason: ReasonForeignOrigin},
		{name: "WebSocket from filex's own page", method: http.MethodGet, path: "/api/ws", hdr: map[string]string{"Upgrade": "websocket", "Sec-Fetch-Site": "same-origin", "Origin": "https://" + host}},
		{name: "WebSocket with a ticket (embed)", method: http.MethodGet, path: "/api/ws?ticket=t1", hdr: map[string]string{"Upgrade": "websocket", "Sec-Fetch-Site": "cross-site", "Origin": "https://work.example.org"}},
		// Routes whose credential is the request itself.
		{name: "public share PIN", path: "/api/public/s/tok/pin", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "public drop upload", path: "/api/public/d/tok/upload", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "share PIN form", path: "/s/tok", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "drop form", path: "/d/tok", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "upload ticket", method: http.MethodPut, path: "/u/tick", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "presigned S3 upload", method: http.MethodPut, path: "/s3/bucket/key", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}},
		{name: "document server callback", path: "/api/files/onlyoffice/callback", hdr: map[string]string{"Origin": "null"}},
		{name: "desktop PKCE exchange", path: "/api/auth/desktop/exchange", hdr: map[string]string{"Origin": "null"}},
		{name: "exempt prefixes match whole segments only", path: "/s3x/a", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "/dav is not the /d drop link", method: http.MethodPut, path: "/dav/x", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		{name: "/api/publicity is not /api/public", path: "/api/publicity", hdr: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, reason: ReasonCrossSite},
		// P-3: installing a storage plugin with the administrator's session.
		{name: "plugin install from another site", path: "/api/admin/plugins", hdr: map[string]string{"Sec-Fetch-Site": "same-site", "Origin": sibling}, reason: ReasonSameSite},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			method, path := c.method, c.path
			if method == "" {
				method = http.MethodPost
			}
			if path == "" {
				path = "/api/files/manager?action=delete"
			}
			v := guard(c.trusted...).Judge(req(method, path, c.hdr))
			if c.reason == "" {
				assert.False(t, v.Refused, "should pass: %+v", v)
				return
			}
			assert.True(t, v.Refused, "should be refused")
			assert.Equal(t, c.reason, v.Reason)
		})
	}
}

// Sign-in forgery: the sign-in form is judged although it carries no session.
// Login decodes its body as JSON whatever the Content-Type says, so a
// text/plain form post from any page could sign the visitor in to an account
// the page chose; after that, what they upload lands in it.
func TestJudge_SignInForgery(t *testing.T) {
	g := guard()
	mk := func(hdr map[string]string) *http.Request {
		r := req(http.MethodPost, "/api/auth/login", hdr)
		r.Header.Del("Cookie")
		return r
	}
	assert.True(t, g.Judge(mk(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.net", "Content-Type": "text/plain"})).Refused)
	assert.True(t, g.Judge(mk(map[string]string{"Origin": "https://evil.example.net"})).Refused, "older browser")
	assert.False(t, g.Judge(mk(map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://" + host})).Refused, "filex's own sign-in page")
	assert.False(t, g.Judge(mk(nil)).Refused, "a script signing in (no browser headers)")
	assert.True(t, g.Judge(func() *http.Request {
		r := req(http.MethodPost, "/api/auth/handoff", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.net"})
		r.Header.Del("Cookie")
		return r
	}()).Refused, "the handoff redeem is a sign-in too")
}

// The CORS layer and the guard read ONE list. This pins that they read it the
// same way: for every entry and origin below, the guard trusts the origin
// exactly when go-chi/cors would answer it with its own Access-Control-Allow-
// Origin (a bare `*` aside, which the guard never treats as trust).
func TestTrusted_MatchesTheCORSLayer(t *testing.T) {
	lists := [][]string{
		{"https://work.example.org"},
		{"https://WORK.example.org"},
		{"https://*.example.com"},
		{"http://localhost:*"},
		{"https://a.example.com", "https://b.example.com"},
	}
	origins := []string{
		"https://work.example.org", "http://work.example.org", "https://work.example.org.evil.net",
		"https://blog.example.com", "https://example.com", "https://deep.sub.example.com",
		"http://localhost:5173", "http://localhost", "https://a.example.com", "https://c.example.com",
	}
	for _, list := range lists {
		h := cors.Handler(cors.Options{AllowedOrigins: list, AllowCredentials: true})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		g := New(Config{Trusted: list})
		for _, o := range origins {
			r := httptest.NewRequest(http.MethodPost, "https://"+host+"/x", nil)
			r.Header.Set("Origin", o)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			corsSays := w.Header().Get("Access-Control-Allow-Origin") == o
			assert.Equal(t, corsSays, g.trusted(strings.ToLower(o)), "list %v, origin %s", list, o)
		}
	}
}

type fakeStore struct {
	mu       sync.Mutex
	rows     []*model.AuditEntry
	sessions map[string]int64
}

func (f *fakeStore) InsertAuditEntry(_ context.Context, e *model.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, e)
	return nil
}

func (f *fakeStore) GetSessionByToken(_ context.Context, token string) (*model.Session, error) {
	if id, ok := f.sessions[token]; ok {
		return &model.Session{UserID: id, Token: token}, nil
	}
	return nil, errors.New("no such session")
}

func TestMiddleware_RefusesWith403AndTheMessage(t *testing.T) {
	g := guard()
	reached := false
	h := g.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	w := httptest.NewRecorder()
	r := req(http.MethodPost, "/api/files/manager?action=delete", map[string]string{
		"Sec-Fetch-Site": "same-site", "Origin": "https://blog.example.com", "Accept-Language": "tr-TR,tr;q=0.9",
	})
	h.ServeHTTP(w, r)
	assert.False(t, reached, "the handler must not run")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, ErrorCode, body["error"])
	assert.Contains(t, body["message"], "başka bir siteden", "in the visitor's language")

	w = httptest.NewRecorder()
	h.ServeHTTP(w, req(http.MethodPost, "/api/files/manager?action=delete", map[string]string{"Sec-Fetch-Site": "same-origin"}))
	assert.True(t, reached)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecord_AuditsTheAccountOncePerMinute(t *testing.T) {
	store := &fakeStore{sessions: map[string]int64{"sess-1": 42}}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	g := New(Config{SessionCookie: "filex_session", Store: store, Now: func() time.Time { return now }})
	h := g.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	hit := func(cookie string) {
		r := req(http.MethodDelete, "/api/files/manager", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.net"})
		r.Header.Del("Cookie")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "filex_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, http.StatusForbidden, w.Code)
	}

	hit("sess-1")
	require.Len(t, store.rows, 1)
	row := store.rows[0]
	assert.Equal(t, AuditAction, row.Action)
	require.NotNil(t, row.UserID)
	assert.Equal(t, int64(42), *row.UserID)
	assert.Equal(t, "https://evil.example.net", row.Metadata["origin"])
	assert.Equal(t, ReasonCrossSite, row.Metadata["reason"])
	assert.Equal(t, "/api/files/manager", row.Metadata["path"])

	hit("sess-1")
	hit("sess-1")
	assert.Len(t, store.rows, 1, "a looping page writes one row a minute")

	now = now.Add(61 * time.Second)
	hit("sess-1")
	assert.Len(t, store.rows, 2)

	hit("")
	hit("forged-cookie")
	assert.Len(t, store.rows, 2, "no live session, no account to audit: no row")
}

// Task #92: the ONLYOFFICE editor's frame origin (the document server's own,
// usually a sibling host) and the app-interface origin are Untrusted - never
// trusted, whatever the CORS list says, a wildcard covering them included. Red
// before #92: the field did not exist, and a wildcard trusted the sibling.
func TestJudge_UntrustedBeatsTheCORSList(t *testing.T) {
	docs := "https://docs.example.com"
	for _, trusted := range [][]string{{"https://*.example.com"}, {docs}, {"HTTPS://Docs.Example.com:443"}} {
		g := New(Config{
			Trusted:       trusted,
			Untrusted:     []string{"HTTPS://DOCS.example.com/", "https://apps.usercontent.example"},
			Self:          "https://platform.example.com",
			SessionCookie: "filex_session",
		})
		for _, site := range []string{"same-site", "cross-site", ""} {
			v := g.Judge(req(http.MethodPost, "/api/files/copy", map[string]string{"Sec-Fetch-Site": site, "Origin": docs}))
			assert.True(t, v.Refused, "trusted %v, Sec-Fetch-Site %q", trusted, site)
		}
		// Referer when the Origin is missing: the same origin, the same answer.
		v := g.Judge(req(http.MethodPost, "/api/files/copy", map[string]string{"Sec-Fetch-Site": "same-site", "Referer": docs + "/filex-frame/editor"}))
		assert.True(t, v.Refused, "trusted %v, by Referer", trusted)
		// The wildcard still covers the operator's other sibling hosts.
		if trusted[0] == "https://*.example.com" {
			v = g.Judge(req(http.MethodPost, "/api/files/copy", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://dash.example.com"}))
			assert.False(t, v.Refused)
		}
	}
}

func TestCanonical(t *testing.T) {
	c, ok := Canonical("HTTPS://Docs.Example.com:443/")
	assert.True(t, ok)
	assert.Equal(t, "https://docs.example.com", c)
	_, ok = Canonical("null")
	assert.False(t, ok)
}

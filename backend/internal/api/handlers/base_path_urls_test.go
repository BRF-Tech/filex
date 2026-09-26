package handlers_test

// Every browser-facing address a handler writes, under a base path
// (FILEX_BASE_PATH, internal/basepath): the no-JS share and file-request
// pages, the OIDC bounce and its error redirects, the unlock and session
// cookies, the realtime socket address — and, pinned the other way, the
// relative URLs inside JSON answers (`/z/<ticket>`), which stay relative to
// the server root because their reader joins them with its own API base.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// underBase returns r as it arrives after the router took the base off.
func underBase(r *http.Request, base string) *http.Request {
	return r.WithContext(basepath.With(r.Context(), base))
}

func shareReq(method, target, token string, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestBasePath_NoJSFolderPageLinksUnderTheBase(t *testing.T) {
	h, _, sh := newBrowseFixture(t, map[string]string{"a.pdf": "x", "b.txt": "y", "sub/c.txt": "z"})

	rec := httptest.NewRecorder()
	h.HandleDownload(rec, underBase(shareReq(http.MethodGet, "/s/"+sh.Token, sh.Token, ""), "/filex"))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `href="/filex/s/`+sh.Token+`/f/a.pdf"`)
	assert.Contains(t, body, `/filex/s/`+sh.Token+`?zip=1`)
	assert.Contains(t, body, `/filex/s/`+sh.Token+`?dir=sub`)
	assert.NotContains(t, body, `href="/s/`, "no link on the page points at the host's root")

	// At the root the page is what it was.
	rec = httptest.NewRecorder()
	h.HandleDownload(rec, shareReq(http.MethodGet, "/s/"+sh.Token, sh.Token, ""))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `href="/s/`+sh.Token+`/f/a.pdf"`)
}

func TestBasePath_NoJSPINGateAndUnlockCookie(t *testing.T) {
	h, _, folder := newBrowseFixture(t, map[string]string{"a.pdf": "x"})
	sh, err := h.Service.Create(context.Background(), share.CreateOpts{NodeID: folder.NodeID, PIN: "482913"})
	require.NoError(t, err)

	// The gate's form posts back under the base.
	rec := httptest.NewRecorder()
	h.HandleDownload(rec, underBase(shareReq(http.MethodGet, "/s/"+sh.Token, sh.Token, ""), "/filex"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `action="/filex/s/`+sh.Token+`"`)

	// The right PIN: the unlock cookie is scoped to the base, and the
	// confirmed download posts under it too.
	rec = httptest.NewRecorder()
	h.HandleDownload(rec, underBase(shareReq(http.MethodPost, "/s/"+sh.Token, sh.Token, "pin=482913"), "/filex"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `/filex/s/`+sh.Token+`?confirmed=1`)
	var unlock *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == share.CookieName(sh.Token) {
			unlock = c
		}
	}
	require.NotNil(t, unlock, "a right PIN mints the unlock cookie")
	assert.Equal(t, "/filex", unlock.Path)
}

func TestBasePath_DropPagesPostUnderTheBase(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	pinTok, pin := mintDrop(t, r, store, st, root, "gizli", true)
	openTok, _ := mintDrop(t, r, store, st, root, "gelen", false)

	// Served the way the router serves it: the base comes off in front.
	outer := basepath.Middleware("/filex")(r)

	rec := getPage(t, outer, "/filex/d/"+pinTok, "en")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `action="/filex/d/`+pinTok+`"`, "the PIN gate posts under the base")

	rec = getPage(t, outer, "/filex/d/"+openTok, "en")
	require.Equal(t, http.StatusOK, rec.Code)
	// The uploader's script config: where its XHR posts the files.
	assert.Contains(t, rec.Body.String(), `"action":"/filex/d/`+openTok+`"`, "the uploader POSTs under the base")

	req := httptest.NewRequest(http.MethodPost, "/filex/d/"+pinTok, strings.NewReader(url.Values{"pin": {pin}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	outer.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var unlock *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == share.CookieName(pinTok) {
			unlock = c
		}
	}
	require.NotNil(t, unlock)
	assert.Equal(t, "/filex", unlock.Path)
}

func TestBasePath_OIDCCallbackStaysUnderTheBase(t *testing.T) {
	_, store := testutil.NewTestDB(t)

	// Single tenant: the public URL carries the base (config.Load puts it
	// there), so the error redirects do.
	a := handlers.NewAuth(store, nil, &fakeOIDC{err: errors.New("boom")}, "https://example.com/filex", false, "")
	req := underBase(httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=x&state=y", nil), "/filex")
	rec := httptest.NewRecorder()
	a.OIDCCallback(rec, req)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://example.com/filex/admin/login?error=oidc", rec.Header().Get("Location"))

	// Success: the bounce lands on the panel under the base, and the session
	// cookie is scoped to it.
	a = handlers.NewAuth(store, nil, &fakeOIDC{user: &model.User{ID: 1, Email: "u@example.com", Enabled: true}, token: "tkn"}, "https://example.com/filex", false, "")
	rec = httptest.NewRecorder()
	a.OIDCCallback(rec, underBase(httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=x&state=y", nil), "/filex"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `location.replace("\/filex\/admin\/")`)
	assert.Contains(t, rec.Body.String(), `url=/filex/admin/`)
	assert.Contains(t, sessionSetCookie(t, rec.Result()), "Path=/filex")

	// Multi-tenant: a tenant's own host, under the same base.
	seedProvider(t, store, &model.Provider{Slug: "tenant-a", Host: "files.tenant-a.test", AuthType: model.AuthTypeOIDC})
	a = handlers.NewAuth(store, nil, &fakeOIDC{err: errors.New("boom")}, "https://operator.test/filex", true, "")
	req = underBase(httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=x&state=y", nil), "/filex")
	req.Host = "files.tenant-a.test"
	rec = httptest.NewRecorder()
	a.OIDCCallback(rec, req)
	assert.Equal(t, "https://files.tenant-a.test/filex/admin/login?error=oidc", rec.Header().Get("Location"))
}

func TestBasePath_RealtimeSocketUnderTheBase(t *testing.T) {
	_, store := testutil.NewTestDB(t)

	// No public URL configured: the address comes from the request, and the
	// base with it.
	wsh := handlers.NewWS(store, nil, realtime.NewHub(), realtime.NewTicketStore(), config.DefaultPublicURL+"/filex")
	wsh.AttachPublicURLConfigured(false)
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsh.Ticket(w, underBase(r, "/filex"))
	})
	assert.Equal(t, "ws://127.0.0.1:5299/filex/api/ws", wsTicketURL(t, &h, "127.0.0.1:5299", false))
	assert.Equal(t, "wss://example.com/filex/api/ws", wsTicketURL(t, &h, "example.com", true))

	// Configured: the public URL already carries it.
	wsh = handlers.NewWS(store, nil, realtime.NewHub(), realtime.NewTicketStore(), "https://example.com/filex")
	wsh.AttachPublicURLConfigured(true)
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsh.Ticket(w, underBase(r, "/filex"))
	})
	assert.Equal(t, "wss://example.com/filex/api/ws", wsTicketURL(t, &h, "example.com", false))
}

// The selection archive's ticket URL is RELATIVE TO THE SERVER ROOT, under a
// base as at the root: the explorer joins it with its API base
// (packages/core absoluteTicketUrl), which already carries the prefix — and
// for an embed whose host proxies filex under a path of its own, only that
// join is right. The bytes are then fetched at <base>/z/<ticket>.
func TestBasePath_ArchiveTicketIsRelativeToTheServerRoot(t *testing.T) {
	srv, client, _, tok, _ := aiFixtureConfigured(t, false, func(c *config.Config) {
		c.BasePath = "/filex"
		c.PublicURL = "http://test.local/filex"
	})
	root := srv.URL + "/filex"
	seedFiles(t, client, root, tok, map[string]string{"main://docs/one.txt": "ONE", "main://docs/two.txt": "TWO"})

	code, info := mintArchive(t, client, root, tok, []string{"main://docs/one.txt", "main://docs/two.txt"})
	require.Equal(t, http.StatusOK, code, "mint failed: %v", info)
	ticketURL, _ := info["url"].(string)
	require.True(t, strings.HasPrefix(ticketURL, "/z/"), "url = %q: relative to the server root, not to the host", ticketURL)

	resp, raw := fetchArchive(t, root, ticketURL)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got := openZip(t, raw)
	assert.Equal(t, "ONE", got["one.txt"])

	// …and the host's root is not filex's.
	resp, _ = fetchArchive(t, srv.URL, "/z/whatever")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// An app link's no-JS page lists the copies the app exposed as plain links —
// under the base, because the browser resolves them against the host.
func TestBasePath_NoJSAppPageLinksUnderTheBase(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	client, _, _ := f.loginRegular(t, "sender", "editor")
	token := f.openSigningLinkAs(t, client, id, "docs/terms.txt")

	// The same router behind the base: the request reaches the handler the
	// way it does in a sub-path deployment.
	under := httptest.NewServer(basepath.Middleware("/filex")(f.srv.Config.Handler))
	defer under.Close()
	status, raw := doReq(t, freshClient(t), http.MethodGet, under.URL+"/filex/s/"+token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	body := string(raw)
	assert.Contains(t, body, `href="/filex/api/public/s/`+token+`/file/`, "an exposed copy is linked under the base")
	assert.NotContains(t, body, `href="/api/`, "no link on the page points at the host's root")
}

package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// ── An app's public route, end to end through the router ───────────────────
//
// The echo module, widened into a link shortener (testdata/echo/routes.go),
// set up through the admin doors, then visited the way a stranger does.

func shortenerManifest(m map[string]any) {
	perms, _ := m["permissions"].([]any)
	m["permissions"] = append(perms, "data", "public_routes", "link_shortener")
	ups, _ := m["user_permissions"].([]any)
	m["user_permissions"] = append(ups, map[string]any{"id": "use", "label": map[string]any{"en": "Shorten links", "tr": "Bağlantı kısalt"}, "default": "user"})
	m["routes"] = []any{map[string]any{"id": "go", "label": map[string]any{"en": "Short links", "tr": "Kısa bağlantılar"}, "default_prefix": "l"}}
	m["setup"] = map[string]any{"view": "setup", "reset": map[string]any{"warning": map[string]any{"en": "Every short link goes.", "tr": "Bütün kısa bağlantılar gider."}}}
	m["link_shortener"] = map[string]any{"route": "go", "requires": "use"}
	views, _ := m["views"].([]any)
	m["views"] = append(views, map[string]any{"id": "setup", "placement": "setup", "label": map[string]any{"en": "Set up", "tr": "Kur"}})
}

// setUpShortener installs and sets the app up through the HTTP doors, with
// filex's own address under /l and l.example.test of its own.
func setUpShortener(t *testing.T, f *appFixture) int64 {
	t.Helper()
	id := f.installEchoWith(t, shortenerManifest)
	base := f.srv.URL + fmt.Sprintf("/api/admin/app-plugins/%d", id)
	status, raw := doReq(t, f.admin, http.MethodGet, base+"/setup", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var a struct {
		State   string `json:"state"`
		AppStep struct {
			View string `json:"view"`
		} `json:"app_step"`
		OwnHosts []string `json:"own_hosts"`
	}
	require.NoError(t, json.Unmarshal(raw, &a))
	assert.Equal(t, "setup", a.State)
	assert.Equal(t, "setup", a.AppStep.View)
	assert.Equal(t, []string{"test.local"}, a.OwnHosts, "the host the DNS of an address of its own points at")

	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/complete", nil)
	assert.Equal(t, http.StatusConflict, status, string(raw))
	assert.Contains(t, string(raw), "setup_app_step")

	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/view", map[string]any{"event": "action", "action_id": "finish"})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"app_done":true`)

	// filex's own paths are refused as prefixes, in the reader's language.
	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/routes/go/bases", map[string]any{"prefix": "api"})
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "short_link_prefix_reserved")
	assert.Contains(t, string(raw), `"field":"prefix"`)
	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/routes/go/bases", map[string]any{"host": "test.local"})
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "route_host_is_filex")

	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/routes/go/bases", map[string]any{"prefix": "l", "default": true})
	require.Equal(t, http.StatusCreated, status, string(raw))
	assert.Contains(t, string(raw), `"url":"http://test.local/l"`)
	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/routes/go/bases", map[string]any{"host": "l.example.test"})
	require.Equal(t, http.StatusCreated, status, string(raw))

	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/complete", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"state":"running"`)
	// The router picks the addresses up on its own goroutine.
	require.Eventually(t, func() bool {
		resp := visit(t, f, http.MethodGet, "", "/l/hit-probe")
		return resp.StatusCode == http.StatusFound
	}, 5*time.Second, 20*time.Millisecond)
	return id
}

type visitResp struct {
	StatusCode int
	Header     http.Header
	Body       string
}

// visit asks like a browser that has no session: no cookie jar.
func visit(t *testing.T, f *appFixture, method, host, path string) visitResp {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, nil)
	require.NoError(t, err)
	if host != "" {
		req.Host = host
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return visitResp{StatusCode: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

func TestRouteDoor_AVisitAndItsInvariants(t *testing.T) {
	f := newAppFixture(t, nil)
	setUpShortener(t, f)

	hit := visit(t, f, http.MethodGet, "", "/l/hit-a")
	require.Equal(t, http.StatusFound, hit.StatusCode, hit.Body)
	assert.Equal(t, "https://example.com/a", hit.Header.Get("Location"))
	assert.Equal(t, "no-store", hit.Header.Get("Cache-Control"))
	assert.Equal(t, "no-referrer", hit.Header.Get("Referrer-Policy"))
	assert.Contains(t, hit.Header.Get("X-Robots-Tag"), "noindex")
	assert.Empty(t, hit.Header.Values("Set-Cookie"), "a visit sets no cookie")

	page := visit(t, f, http.MethodGet, "", "/l/page")
	require.Equal(t, http.StatusOK, page.StatusCode)
	assert.Contains(t, page.Body, "A page")
	assert.Contains(t, page.Body, `href="https://example.com/next"`)
	assert.Contains(t, page.Header.Get("Content-Security-Policy"), "default-src 'none'")
	assert.NotContains(t, page.Body, "<script")

	// A redirect the app should not make is not made.
	bad := visit(t, f, http.MethodGet, "", "/l/bad")
	assert.Equal(t, http.StatusServiceUnavailable, bad.StatusCode)
	assert.Empty(t, bad.Header.Get("Location"))

	// Every miss is the same page, whatever the reason.
	m1 := visit(t, f, http.MethodGet, "", "/l/never-was")
	m2 := visit(t, f, http.MethodGet, "", "/l/s-c999")
	assert.Equal(t, http.StatusNotFound, m1.StatusCode)
	assert.Equal(t, m1.StatusCode, m2.StatusCode)
	assert.Equal(t, m1.Body, m2.Body, "a miss tells nothing about why")

	// The route's prefix never takes over filex: a POST is the router's, and
	// another path is filex's own.
	assert.NotEqual(t, http.StatusFound, visit(t, f, http.MethodPost, "", "/l/hit-a").StatusCode)
}

// A host of its own answers its addresses and /healthz, nothing else.
func TestRouteDoor_AHostOfItsOwnOpensNothingElse(t *testing.T) {
	f := newAppFixture(t, nil)
	setUpShortener(t, f)
	hit := visit(t, f, http.MethodGet, "l.example.test", "/hit-b")
	require.Equal(t, http.StatusFound, hit.StatusCode, hit.Body)
	assert.Equal(t, "https://example.com/b", hit.Header.Get("Location"))
	for _, p := range []string{"/", "/api/auth/me", "/api/files/capabilities", "/admin/", "/s/abc", "/drive/"} {
		r := visit(t, f, http.MethodGet, "l.example.test", p)
		assert.Equal(t, http.StatusNotFound, r.StatusCode, p)
	}
	assert.Equal(t, http.StatusOK, visit(t, f, http.MethodGet, "l.example.test", "/healthz").StatusCode)
}

// The door's own pages are rendered once per language and served again:
// each visitor still gets the page in their language, and the same page.
func TestRouteDoor_OwnPagesInTheVisitorsLanguage(t *testing.T) {
	f := newAppFixture(t, nil)
	setUpShortener(t, f)
	missIn := func(lang, path string) string {
		req, err := http.NewRequest(http.MethodGet, f.srv.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Language", lang)
		resp, err := http.DefaultTransport.RoundTrip(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	tr1 := missIn("tr", "/l/yok-1")
	en1 := missIn("en", "/l/none-1")
	tr2 := missIn("tr", "/l/yok-2")
	assert.Contains(t, tr1, "Bu bağlantı kullanılamıyor")
	assert.Contains(t, en1, "This link is not available")
	assert.NotContains(t, en1, "kullanılamıyor")
	assert.Equal(t, tr1, tr2, "the same page for every miss")
}

// Guessing is slow: past the burst of misses a client is held back on every
// address, a link that exists included.
func TestRouteDoor_GuessingLocksTheClientOut(t *testing.T) {
	f := newAppFixture(t, nil)
	setUpShortener(t, f)
	for i := 0; i < 20; i++ {
		r := visit(t, f, http.MethodGet, "", fmt.Sprintf("/l/guess-%d", i))
		require.Equal(t, http.StatusNotFound, r.StatusCode, "miss %d", i+1)
	}
	// The 20th miss locked: the 21st guess is not asked of the app.
	assert.Equal(t, http.StatusTooManyRequests, visit(t, f, http.MethodGet, "", "/l/guess-20").StatusCode)
	held := visit(t, f, http.MethodGet, "", "/l/hit-a")
	assert.Equal(t, http.StatusTooManyRequests, held.StatusCode, "the lockout covers a link that exists")
	assert.Empty(t, held.Header.Get("Location"))
}

// "Get short link": the shortener app answers, the address is on its base,
// the same again for the same link, and a mailed link carries it.
func TestShortLinks_GetShortLinkAndTheMail(t *testing.T) {
	f := newAppFixture(t, func(c *config.Config) {})
	setUpShortener(t, f)
	f.writeFile(t, "doc.txt", "hello")
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/share", map[string]any{"path": "main://doc.txt"})
	require.Equal(t, http.StatusOK, status, string(raw))
	var created struct {
		ID    int64 `json:"id"`
		Share struct {
			Token string `json:"token"`
			UUID  string `json:"uuid"`
			URL   string `json:"url"`
		} `json:"share"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	token := created.Share.Token
	if token == "" {
		token = created.Share.UUID
	}
	require.NotEmpty(t, token)

	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/capabilities", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), `"short_links":{"available":true}`)

	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/short-links?share="+token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"url":""`, "nothing is made by asking")

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/short-links", map[string]any{"share": token})
	require.Equal(t, http.StatusOK, status, string(raw))
	var short struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(raw, &short))
	require.True(t, strings.HasPrefix(short.URL, "http://test.local/l/"), short.URL)

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/short-links", map[string]any{"share": token})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), short.URL, "the same address for the same link")

	// The short address leads to the share.
	r := visit(t, f, http.MethodGet, "", strings.TrimPrefix(short.URL, "http://test.local"))
	require.Equal(t, http.StatusFound, r.StatusCode, r.Body)
	assert.Contains(t, r.Header.Get("Location"), "/s/"+token)

	// The words a mail (and the OS share sheet) would carry name the short
	// address, not the long one.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/permissions/share-message?share="+token, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), short.URL)
	assert.NotContains(t, string(raw), "/s/"+token)

	// Revoked, the short address is a miss like any other.
	status, _ = doReq(t, f.admin, http.MethodDelete, f.srv.URL+fmt.Sprintf("/api/files/share/%d", created.ID), nil)
	require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, status)
	// Within link_live's few seconds, the short address is a miss.
	require.Eventually(t, func() bool {
		return visit(t, f, http.MethodGet, "", strings.TrimPrefix(short.URL, "http://test.local")).StatusCode == http.StatusNotFound
	}, 8*time.Second, 100*time.Millisecond)
}

// Reconfigure and removal ask for the app's name, then erase.
func TestAppSetup_ReconfigureAndRemoveAskForTheName(t *testing.T) {
	f := newAppFixture(t, nil)
	id := setUpShortener(t, f)
	base := f.srv.URL + fmt.Sprintf("/api/admin/app-plugins/%d", id)
	f.writeFile(t, "doc.txt", "hello")
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/share", map[string]any{"path": "main://doc.txt"})
	require.Equal(t, http.StatusOK, status, string(raw))
	var created struct {
		Share struct{ Token, UUID string } `json:"share"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	token := created.Share.Token
	if token == "" {
		token = created.Share.UUID
	}
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/short-links", map[string]any{"share": token})
	require.Equal(t, http.StatusOK, status, string(raw))

	status, raw = doReq(t, f.admin, http.MethodGet, base+"/erase", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"confirm":"echo"`)
	assert.Contains(t, string(raw), "Every short link goes.")
	assert.NotContains(t, string(raw), `"data_keys":0`)

	status, raw = doReq(t, f.admin, http.MethodPost, base+"/reconfigure", map[string]any{"confirm": "ECHO"})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "confirm_mismatch")
	status, raw = doReq(t, f.admin, http.MethodPost, base+"/reconfigure", map[string]any{"confirm": "echo"})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"state":"setup"`)
	require.Eventually(t, func() bool {
		return visit(t, f, http.MethodGet, "", "/l/hit-a").StatusCode == http.StatusNotFound
	}, 5*time.Second, 20*time.Millisecond, "nothing is served while it is set up again")

	// Removing: the setup's own state is data too (the app kept its config
	// again? no - erased); with nothing kept no name is asked.
	status, raw = doReq(t, f.admin, http.MethodPost, base+"/setup/view", map[string]any{"event": "action", "action_id": "finish"})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doReq(t, f.admin, http.MethodDelete, base, nil)
	require.Equal(t, http.StatusConflict, status, string(raw))
	assert.Contains(t, string(raw), "confirm_required")
	status, raw = doReq(t, f.admin, http.MethodDelete, base+"?confirm=echo", nil)
	require.Equal(t, http.StatusNoContent, status, string(raw))
}

// An uploader that asks for a short link answers each upload with it; one
// that does not keeps the link itself.
func TestShortLinks_AnUploaderAnswersShort(t *testing.T) {
	f := newAppFixture(t, nil)
	setUpShortener(t, f)
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/auth/uploaders", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"short_links_available":true`)

	upload := func(short bool) string {
		status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/auth/uploaders", map[string]any{
			"name": fmt.Sprintf("shots-%v", short), "storage": "main", "folder": "up", "visibility": "public", "short_link": short,
		})
		require.Equal(t, http.StatusCreated, status, string(raw))
		var made struct {
			Key      string `json:"key"`
			Uploader struct {
				ShortLink bool `json:"short_link"`
			} `json:"uploader"`
		}
		require.NoError(t, json.Unmarshal(raw, &made))
		assert.Equal(t, short, made.Uploader.ShortLink)
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("file", fmt.Sprintf("note-%v.txt", short))
		require.NoError(t, err)
		_, _ = part.Write([]byte("captured"))
		require.NoError(t, mw.Close())
		req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/uploads", &body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+made.Key)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
		var ans struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.Unmarshal(b, &ans))
		return ans.URL
	}
	assert.True(t, strings.HasPrefix(upload(true), "http://test.local/l/"), "the short address")
	assert.Contains(t, upload(false), "/s/", "the link itself")
}

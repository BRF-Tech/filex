package onlyoffice

// FILEX_ONLYOFFICE_INTERNAL_URL (GitHub #143): the document server's URL is
// the browser's - api.js loads from it - and filex's own requests go to the
// internal address when one is set. Behind a reverse proxy that ends TLS the
// browser opens https://office.example.com, while the filex container reaches
// the document server at http://onlyoffice; one URL cannot be both.
//
// Run against the code before the field (LiveInternalURL and the Converter's
// BrowserURL do not exist there) these do not build; with the field ignored
// they fail on their assertions: the thumbnail is asked at the browser's
// address, which does not resolve here, and a saved document named on the
// browser's origin is refused as foreign or fetched from the proxy.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// browserDS is a document server's browser-facing address that nothing in a
// test can reach: a request filex sends there fails, so a green test proves
// the request went to the internal address instead.
const browserDS = "https://office.invalid"

// liveService is a Service on the live configuration, as the server wires it:
// the row's URL (the browser's) and secret, and the internal address.
func liveService(browser, internal string) *Service {
	s := &Service{PublicURL: "http://filex.test"}
	s.Live = func(context.Context) (string, string) { return browser, fakeSecret }
	s.LiveInternalURL = func(context.Context) string { return internal }
	return s
}

func TestInternalURL_AThumbnailIsAskedAtTheInternalAddress(t *testing.T) {
	ds := newFakeDS(t, func(int, map[string]any) (int, any) {
		// The document server names its result on the browser's origin, the
		// one the proxy gives it (X-Forwarded-Host/-Proto).
		return http.StatusOK, map[string]any{"endConvert": true, "fileType": "png",
			"fileUrl": browserDS + "/cache/files/result.png?md5=abc&expires=1"}
	})
	svc := liveService(browserDS, ds.srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	png, err := svc.DrawThumbnail(ctx, ThumbRequest{NodeID: 9, StorageID: 1, Name: "plan.docx", Attempt: 1})
	require.NoError(t, err, "the thumbnail was asked at the internal address and its result fetched there")
	assert.Equal(t, tinyPNG(), png)
	require.Len(t, ds.seen(), 1, "the conversion went to the internal address")

	conv, err := svc.Converter(ctx)
	require.NoError(t, err)
	assert.Equal(t, ds.srv.URL, conv.DocumentServerURL)
	assert.Equal(t, browserDS, conv.BrowserURL)
}

func TestInternalURL_TheEditorStillLoadsFromTheBrowsersAddress(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	h.svc.DocumentServerURL = browserDS
	h.svc.LiveInternalURL = func(context.Context) string { return h.ds.URL }
	ada := h.user(t, "ada@example.com", model.RoleUser)
	u, err := h.store.GetUser(context.Background(), ada)
	require.NoError(t, err)

	cfg, err := h.svc.BuildConfigForNode(context.Background(), h.node, u, "en", "edit")
	require.NoError(t, err)
	assert.Equal(t, browserDS, cfg.DocumentServerURL, "api.js loads in the browser, from the browser's address")
}

// A save callback names the saved document on the origin the editor's
// session came in on - the browser's. With an internal address set it is
// downloaded from there, path and query kept; one named on the internal
// address is downloaded as it is; anywhere else is still refused.
func TestInternalURL_TheSavedDocumentIsDownloadedFromTheInternalAddress(t *testing.T) {
	for _, tc := range []struct {
		name  string
		saved func(h *csvHarness) string
	}{
		{"named on the browser's origin", func(*csvHarness) string { return browserDS + "/saved" }},
		{"named on the internal address", func(h *csvHarness) string { return h.ds.URL + "/saved" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDocHarness(t, "rapor.docx", docxMime, "V1")
			h.svc.DocumentServerURL = browserDS
			h.svc.LiveInternalURL = func(context.Context) string { return h.ds.URL }
			ada := h.user(t, "ada@example.com", model.RoleUser)
			h.open(t, ada)
			h.serve(docxBytes)

			resp, err := h.postCallback(t, h.node.ID, signedBody(t, dsCallback(h.sessionKey, StatusReadyForSaving, tc.saved(h), idStrings(ada)...)), "")
			require.NoError(t, err)
			assert.Equal(t, 0, resp["error"], "%v", resp)
			assert.Equal(t, docxBytes, h.disk(t))
			h.sink.wait(t)
		})
	}

	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	h.svc.DocumentServerURL = browserDS
	h.svc.LiveInternalURL = func(context.Context) string { return h.ds.URL }
	ada := h.user(t, "ada@example.com", model.RoleUser)
	h.open(t, ada)
	var asked atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_, _ = w.Write([]byte("FOREIGN BYTES"))
	}))
	t.Cleanup(foreign.Close)
	resp, err := h.postCallback(t, h.node.ID, signedBody(t, dsCallback(h.sessionKey, StatusReadyForSaving, foreign.URL+"/saved", idStrings(ada)...)), "")
	assert.True(t, refusedCallback(resp, err), "%v %v", resp, err)
	assert.Zero(t, asked.Load(), "an address on neither of the document server's origins was fetched")
	assert.Equal(t, "V1", h.disk(t))
}

// The Test's questions (the route back to filex, JWT) are filex's own
// requests too.
func TestInternalURL_TheTestAsksAtTheInternalAddress(t *testing.T) {
	h := newDocHarness(t, "rapor.docx", docxMime, "V1")
	h.svc.DocumentServerURL = browserDS
	h.svc.LiveInternalURL = func(context.Context) string { return h.ds.URL }

	tgt := h.svc.LiveTarget(context.Background())
	assert.Equal(t, browserDS, tgt.DocumentServerURL)
	assert.Equal(t, h.ds.URL, tgt.InternalURL)

	res := h.svc.CheckReversePath(context.Background(), nil)
	h.mu.Lock()
	asked := len(h.convReqs)
	h.mu.Unlock()
	assert.NotZero(t, asked, "the reverse-path question reached the document server at its internal address: %+v", res)
	assert.NotContains(t, res.Detail, "did not answer the conversion request", "%+v", res)
}

// No internal address: everything as before, the browser's URL for both.
func TestInternalURL_NoneIsTheURL(t *testing.T) {
	s := liveService("http://ds:8080", "")
	base, browser := s.serverBase(context.Background())
	assert.Equal(t, "http://ds:8080", base)
	assert.Empty(t, browser)

	s = liveService("http://ds:8080", "http://ds:8080/")
	base, browser = s.serverBase(context.Background())
	assert.Equal(t, "http://ds:8080", base, "the same address twice is one address")
	assert.Empty(t, browser)

	// Not configured is not configured, whatever the variable says.
	s = liveService("", "http://onlyoffice")
	base, _ = s.serverBase(context.Background())
	assert.Empty(t, base)
}

func TestOnDocumentServer(t *testing.T) {
	const in = "http://onlyoffice:8080"
	for _, tc := range []struct {
		name, raw, browser, want string
		ok                       bool
	}{
		{"on the internal address", in + "/cache/x.png?md5=1", browserDS, in + "/cache/x.png?md5=1", true},
		{"on the browser's, moved", browserDS + "/cache/x.png?md5=1&e=2", browserDS, in + "/cache/x.png?md5=1&e=2", true},
		{"on the browser's under its path, moved without it", "https://www.example.com/office/cache/x%20y.png", "https://www.example.com/office", in + "/cache/x%20y.png", true},
		{"elsewhere on the browser's origin, refused", "https://www.example.com/admin/x", "https://www.example.com/office", "", false},
		{"a path that only starts the same, refused", "https://www.example.com/officeX/cache", "https://www.example.com/office", "", false},
		{"another host, refused", "http://169.254.169.254/latest", browserDS, "", false},
		{"the browser's host on http: another origin, refused", "http://office.invalid/cache/x", browserDS, "", false},
		{"no browser address: only the internal one", browserDS + "/cache/x", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := onDocumentServer(tc.raw, in, tc.browser)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

package onlyoffice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// filexUnder starts a stand-in for filex that serves nothing but the probe
// endpoint, the way the real handler does.
func filexUnder(t *testing.T, svc *Service) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProbePath {
			http.NotFound(w, r)
			return
		}
		body, ok := svc.ServeProbe(r.URL.Query().Get("t"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// docServer stands in for the document server's conversion endpoint. fetch
// decides whether it actually downloads the URL it is handed; answer is the
// JSON it replies with.
func docServer(t *testing.T, fetch bool, answer map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if fetch {
			url, _ := req["url"].(string)
			resp, err := http.Get(url) //nolint:noctx // test stand-in
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestReversePath_ArrivalIsTheVerdict is the whole point of the probe: what
// counts is whether the document server's request REACHED filex.
func TestReversePath_ArrivalIsTheVerdict(t *testing.T) {
	svc := &Service{FetchTTL: 0}
	filex := filexUnder(t, svc)
	svc.PublicURL = filex.URL

	t.Run("it fetched", func(t *testing.T) {
		ds := docServer(t, true, map[string]any{"endConvert": true, "fileUrl": "http://example/out.docx"})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.True(t, res.Checked)
		require.True(t, res.OK, res.Detail)
		require.Contains(t, res.URL, ProbePath)
	})

	t.Run("it could not download", func(t *testing.T) {
		// error -4 is OnlyOffice for "error while downloading the document
		// file" — a document server that is up and cannot reach filex, which
		// is exactly the shape issue #17 reported as "Download failed".
		ds := docServer(t, false, map[string]any{"error": -4})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.True(t, res.Checked)
		require.False(t, res.OK)
		require.Equal(t, -4, res.Code)
		require.Contains(t, res.Detail, "could not download")
	})

	t.Run("a rejected signature is not a broken route", func(t *testing.T) {
		// -8 means the document server refused the request before downloading
		// anything. Reporting that as "the route back is broken" would send an
		// operator to fix the wrong address.
		ds := docServer(t, false, map[string]any{"error": -8})
		svc.DocumentServerURL, svc.JWTSecret = ds.URL, "s3cret"

		res := svc.VerifyReversePath(context.Background())
		require.False(t, res.Checked, "an unanswerable question must not read as a failure")
		require.Contains(t, res.Detail, "signature")
	})
}

// TestReversePath_UsesTheCallbackAddress proves the probe is sent to the
// address the document server is told to use, not to the browser-facing one.
func TestReversePath_UsesTheCallbackAddress(t *testing.T) {
	svc := &Service{PublicURL: "https://files.example.com"}
	filex := filexUnder(t, svc)
	svc.LiveCallbackURL = func(context.Context) string { return filex.URL }

	var handed string
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		handed, _ = req["url"].(string)
		resp, err := http.Get(handed) //nolint:noctx // test stand-in
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"endConvert": true})
	}))
	defer ds.Close()
	svc.DocumentServerURL = ds.URL

	res := svc.VerifyReversePath(context.Background())
	require.True(t, res.OK, res.Detail)
	require.True(t, strings.HasPrefix(handed, filex.URL),
		"the document server must be sent to the callback address, got %q", handed)
	require.NotContains(t, handed, "files.example.com")
}

// TestProbeToken_IsOneShotAndUnguessable keeps the endpoint from becoming a
// thing a stranger can poke.
func TestProbeToken_IsOneShotAndUnguessable(t *testing.T) {
	svc := &Service{}

	_, ok := svc.ServeProbe("")
	require.False(t, ok, "an empty token must not be served")
	_, ok = svc.ServeProbe("0123456789abcdef0123456789abcdef")
	require.False(t, ok, "a token filex never issued must not be served")

	tok := svc.probes().issue()
	require.Len(t, tok, 32)
	body, ok := svc.ServeProbe(tok)
	require.True(t, ok)
	require.NotEmpty(t, body)
	require.False(t, strings.Contains(body, tok), "the answer must not echo the token")
}

// TestEditorConfig_PointsAtTheCallbackAddress: the two URLs the document
// server is handed must be built from the callback address. They used to be
// built from the public URL with no way to separate them, which is the gap
// issue #17 ended on.
func TestEditorConfig_PointsAtTheCallbackAddress(t *testing.T) {
	mtime := time.Unix(1700000000, 0)
	node := &model.Node{ID: 42, Name: "rapor.docx", PathHash: "abc", Size: 10, BackendMtime: &mtime}

	svc := &Service{
		DocumentServerURL: "https://office.example.com",
		JWTSecret:         "s3cret",
		PublicURL:         "https://files.example.com",
		LiveCallbackURL:   func(context.Context) string { return "http://filex:5212" },
	}
	cfg, err := svc.BuildConfigForNode(context.Background(), node, nil, "en", "edit")
	require.NoError(t, err)

	doc := cfg.Config["document"].(map[string]any)
	editor := cfg.Config["editorConfig"].(map[string]any)
	require.True(t, strings.HasPrefix(doc["url"].(string), "http://filex:5212/"),
		"the document fetch URL must use the callback address, got %v", doc["url"])
	require.True(t, strings.HasPrefix(editor["callbackUrl"].(string), "http://filex:5212/"),
		"the save callback must use the callback address, got %v", editor["callbackUrl"])

	// With no callback address configured, both fall back to the public URL —
	// which is every install where one address serves both.
	svc.LiveCallbackURL = nil
	cfg, err = svc.BuildConfigForNode(context.Background(), node, nil, "en", "edit")
	require.NoError(t, err)
	doc = cfg.Config["document"].(map[string]any)
	require.True(t, strings.HasPrefix(doc["url"].(string), "https://files.example.com/"))
}

package handlers_test

// Issue #80: the Test's third leg proved less than it looked like.
//
// The reverse-path probe had a door of its own, /api/files/onlyoffice/probe,
// that asked for no signature and did not look at the configuration in force.
// A green "the document server reached filex" therefore stood next to a fetch
// that every real document failed. It also never asked whether the document
// server enforces JWT, which is the first thing to know about a -4 (by
// default a document server skips its private-address filter for a signed
// request) and the reason saves come back unsigned.
//
// These tests drive the real router: the probe must be refused for a bad
// signature exactly as a document would be, and a document server that takes
// an unsigned request must be named on the card.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

type convertStub struct {
	// enforceJWT: an unsigned conversion request is answered -8, the way a
	// document server with JWT on answers it, and nothing is fetched.
	enforceJWT bool
	// forgeSig rewrites the `sig` of the URL before fetching it.
	forgeSig bool
}

// convertingStub is a document server whose conversion endpoint downloads the
// URL it is handed (the signed request, and the unsigned one unless it
// enforces JWT).
func convertingStub(t *testing.T, o convertStub) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ConvertService.ashx") {
			w.WriteHeader(http.StatusOK) // /healthcheck
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		_, hasToken := req["token"]
		hasToken = hasToken || r.Header.Get("Authorization") != ""
		w.Header().Set("Content-Type", "application/json")
		if o.enforceJWT && !hasToken {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": -8})
			return
		}
		target, _ := req["url"].(string)
		if o.forgeSig {
			if u, err := url.Parse(target); err == nil {
				q := u.Query()
				q.Set("sig", "forged")
				u.RawQuery = q.Encode()
				target = u.String()
			}
		}
		if resp, err := http.Get(target); err == nil { //nolint:noctx // test stand-in
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"endConvert": true})
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func publishedAt(c *config.Config) {
	c.PublicURL = "https://files.example.com"
	c.PublicURLSet = true
}

// The probe is refused for a bad signature, the way a document's fetch is.
// Before #80 it went to a door that never looked at one, so this was green.
func TestExternalAdmin_TestProbesThroughTheSignedDocumentFetch(t *testing.T) {
	h, _ := liveExternalServer(t, publishedAt)
	ds := convertingStub(t, convertStub{enforceJWT: true, forgeSig: true})
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"enabled": true, "url": ds, "secret": "s3cr3t", "callback_url": h.srv.URL,
	}).StatusCode)

	out := h.Post(t, "/api/admin/external/onlyoffice/test")
	leg, _ := out["service_to_filex"].(map[string]any)
	require.NotNil(t, leg)
	require.Equal(t, true, leg["checked"], "the request reached filex, so the question was put")
	require.Equal(t, false, leg["ok"],
		"filex refused the forged signature, as it refuses it for a document: the third leg is not green")
	require.Equal(t, "filex_refused", leg["advice"])
	require.Contains(t, leg["url"], "/api/files/onlyoffice/fetch", "the probe walks the document's door")

	// And with the signature intact, the same route is green.
	ds = convertingStub(t, convertStub{enforceJWT: true})
	require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
		"url": ds,
	}).StatusCode)
	out = h.Post(t, "/api/admin/external/onlyoffice/test")
	leg, _ = out["service_to_filex"].(map[string]any)
	require.Equal(t, true, leg["ok"], "%v", leg["detail"])
}

// A document server that takes a request with no token does not enforce JWT,
// and the card says so. One that answers -8 is left alone.
func TestExternalAdmin_TestWarnsWhenTheDocumentServerDoesNotEnforceJWT(t *testing.T) {
	t.Run("JWT off", func(t *testing.T) {
		h, _ := liveExternalServer(t, publishedAt)
		ds := convertingStub(t, convertStub{})
		require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
			"enabled": true, "url": ds, "secret": "s3cr3t", "callback_url": h.srv.URL,
		}).StatusCode)

		out := h.Post(t, "/api/admin/external/onlyoffice/test")
		leg, _ := out["service_to_filex"].(map[string]any)
		require.Equal(t, false, leg["jwt_enforced"])
		require.Equal(t, "warning", advisoryCodes(out["advisories"])["jwt_not_enforced"],
			"saves from such a document server arrive unsigned and are refused: a warning")
	})

	t.Run("JWT on", func(t *testing.T) {
		h, _ := liveExternalServer(t, publishedAt)
		ds := convertingStub(t, convertStub{enforceJWT: true})
		require.Equal(t, http.StatusOK, h.Patch(t, "/api/admin/external/onlyoffice", map[string]any{
			"enabled": true, "url": ds, "secret": "s3cr3t", "callback_url": h.srv.URL,
		}).StatusCode)

		out := h.Post(t, "/api/admin/external/onlyoffice/test")
		leg, _ := out["service_to_filex"].(map[string]any)
		require.Equal(t, true, leg["jwt_enforced"])
		_, warned := advisoryCodes(out["advisories"])["jwt_not_enforced"]
		require.False(t, warned, "a document server that enforces JWT must not be warned about")
	})
}

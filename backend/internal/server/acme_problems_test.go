package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// An authorization the authority could not validate, as Pebble and Let's
// Encrypt answer it (RFC 8555, 7.1.4).
const invalidAuthz = `{
  "status": "invalid",
  "identifier": {"type": "dns", "value": "beta.tenants.files.example"},
  "challenges": [
    {"type": "tls-alpn-01", "url": "https://ca.example/chal/1", "token": "t", "status": "invalid",
     "error": {"type": "urn:ietf:params:acme:error:dns", "detail": "DNS problem: NXDOMAIN looking up A for beta.tenants.files.example", "status": 400}},
    {"type": "http-01", "url": "https://ca.example/chal/2", "token": "t", "status": "pending"}
  ]
}`

func acmeServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func records(h *recordingHandler, msg string) []map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]string
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
		out = append(out, attrs)
	}
	return out
}

const acmeProblemMsg = "tls: the ACME authority could not validate an address"

// TestACMEProblems_LogsTheAuthoritysReason: the authority's reason for an
// invalid authorization reaches the log, with the address and the challenge,
// once, and the client still reads the body it asked for. autocert alone says
// only "no viable challenge type found" (measured against Pebble).
func TestACMEProblems_LogsTheAuthoritysReason(t *testing.T) {
	h := captureLogs(t)
	srv := acmeServer(t, invalidAuthz)
	c := &http.Client{Transport: newACMEProblems(nil, nil)}

	for i := 0; i < 2; i++ {
		res, err := c.Post(srv.URL+"/authz/1", "application/jose+json", strings.NewReader("{}"))
		require.NoError(t, err)
		got, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		assert.Equal(t, invalidAuthz, string(got), "the ACME client reads the body untouched")
	}

	logs := records(h, acmeProblemMsg)
	require.Len(t, logs, 1, "one line per authorization, however often it is polled")
	assert.Equal(t, "beta.tenants.files.example", logs[0]["domain"])
	assert.Equal(t, "tls-alpn-01", logs[0]["challenge"])
	assert.Equal(t, "urn:ietf:params:acme:error:dns", logs[0]["problem"])
	assert.Contains(t, logs[0]["detail"], "NXDOMAIN")
}

// TestACMEProblems_QuietOnAnythingElse: a valid or pending authorization, a
// challenge, a directory, a non-JSON answer: nothing is logged.
func TestACMEProblems_QuietOnAnythingElse(t *testing.T) {
	h := captureLogs(t)
	c := &http.Client{Transport: newACMEProblems(nil, nil)}
	for _, body := range []string{
		strings.Replace(invalidAuthz, `"status": "invalid",`+"\n"+`  "identifier"`, `"status": "valid",`+"\n"+`  "identifier"`, 1),
		`{"type": "tls-alpn-01", "status": "invalid", "error": {"detail": "a challenge, not an authorization"}}`,
		`{"newNonce": "https://ca.example/nonce", "newOrder": "https://ca.example/order"}`,
		`not json`,
	} {
		srv := acmeServer(t, body)
		res, err := c.Get(srv.URL)
		require.NoError(t, err)
		got, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		assert.Equal(t, body, string(got))
	}
	assert.Empty(t, records(h, acmeProblemMsg))
}

// TestACMEManager_ClientLogsProblems: filex's own TLS asks the configured
// directory (Let's Encrypt when none is set) through the transport that logs
// the authority's reasons.
func TestACMEManager_ClientLogsProblems(t *testing.T) {
	s := &Server{cfg: config.Config{}}
	m := s.acmeManager(t.TempDir(), nil)
	require.NotNil(t, m.Client)
	assert.Equal(t, "https://acme-v02.api.letsencrypt.org/directory", m.Client.DirectoryURL)
	require.NotNil(t, m.Client.HTTPClient)
	_, ok := m.Client.HTTPClient.Transport.(*acmeProblems)
	assert.True(t, ok, "the ACME client's transport is acmeProblems")

	s.cfg.TLS.ACMEDirectory = " https://pebble:14000/dir "
	assert.Equal(t, "https://pebble:14000/dir", s.acmeManager(t.TempDir(), nil).Client.DirectoryURL)
}

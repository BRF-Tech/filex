package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestACMEHostPolicy_HTTP01HostCarriesAPort: autocert's HTTP-01 handler asks
// the host policy with the request's Host header, which carries the port when
// the authority dials another one than 80. Measured against Pebble (HTTP-01
// on :5002, e2e/realenv): the challenge request was answered 403
// "acme: h1.filex.test:5002 is not an address this platform serves", and no
// certificate could be proved by HTTP-01. Let's Encrypt dials port 80 and
// sends the bare name, so it was not affected.
func TestACMEHostPolicy_HTTP01HostCarriesAPort(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	s := &Server{cfg: config.Config{PublicURL: "https://files.example"}, store: store}
	m := s.acmeManager(t.TempDir(), nil)
	ctx := context.Background()

	assert.NoError(t, m.HostPolicy(ctx, "files.example"), "the SNI name")
	assert.NoError(t, m.HostPolicy(ctx, "files.example:5002"), "the HTTP-01 request's Host header")
	assert.ErrorIs(t, m.HostPolicy(ctx, "stranger.example:5002"), errNotServed)
	assert.ErrorIs(t, m.HostPolicy(ctx, "stranger.example"), errNotServed)

	// Through autocert's own HTTP-01 handler: a served name with a port is
	// looked up (no such token here: 404), not refused (403).
	h := m.HTTPHandler(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://files.example:5002/.well-known/acme-challenge/no-such-token", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://stranger.example:5002/.well-known/acme-challenge/no-such-token", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

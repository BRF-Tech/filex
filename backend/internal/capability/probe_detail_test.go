package capability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// Issue #17: the Test button said "unreachable" while `curl` from the same
// container loaded the document server's welcome page. /healthcheck was a 502
// from ONLYOFFICE's own nginx — docservice behind it was stopped. The probe
// now says what it saw.

func TestProbeHTTPDetail_StatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	ok, detail := probeHTTPDetail(srv.URL + "/healthcheck")
	assert.False(t, ok)
	assert.Equal(t, "GET "+srv.URL+"/healthcheck returned HTTP 502", detail)
}

func TestProbeHTTPDetail_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the probe timeout")
	}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	ok, detail := probeHTTPDetail(srv.URL)
	assert.False(t, ok)
	assert.Equal(t, "GET "+srv.URL+": no answer within 3s", detail)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestProbeHTTPDetail_ConnectionRefusedNamesTheError(t *testing.T) {
	ok, detail := probeHTTPDetail("http://127.0.0.1:1/healthcheck")
	assert.False(t, ok)
	assert.True(t, strings.HasPrefix(detail, "GET http://127.0.0.1:1/healthcheck: "), detail)
	assert.NotContains(t, detail, `Get "`, "the URL is not repeated by net/http's own prefix")
	assert.Contains(t, strings.ToLower(detail), "refused")
}

func TestProbeHTTPDetail_HealthyHasNoDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("true"))
	}))
	defer srv.Close()

	ok, detail := probeHTTPDetail(srv.URL)
	assert.True(t, ok)
	assert.Empty(t, detail)
}

func TestExternalProbeHint_OnlyOfficeGatewayErrorsPointAtDocservice(t *testing.T) {
	d := externalProbeHint("onlyoffice", "GET https://docs.test/healthcheck returned HTTP 502")
	assert.Contains(t, d, "docservice")
	assert.Contains(t, d, "supervisorctl status")

	assert.Equal(t, "GET https://docs.test/healthcheck returned HTTP 404",
		externalProbeHint("onlyoffice", "GET https://docs.test/healthcheck returned HTTP 404"),
		"only a gateway error means the process behind nginx is down")
	assert.Equal(t, "GET https://c.test/healthz returned HTTP 502",
		externalProbeHint("convert", "GET https://c.test/healthz returned HTTP 502"))
}

func TestProbeExternal_CarriesTheDetail(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, srv.URL, "x", "", time.Time{}, ""))

	st, err := New(store).ProbeExternal(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "unreachable", st.State)
	assert.Contains(t, st.Detail, "/healthcheck returned HTTP 502")
	assert.Contains(t, st.Detail, "docservice")
}

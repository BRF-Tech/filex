package capability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	assert.Equal(t, "GET "+srv.URL+"/healthcheck returned HTTP 502 (2 attempts)", detail)
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

	oldTimeout := probeTimeout
	probeTimeout = 50 * time.Millisecond
	defer func() { probeTimeout = oldTimeout }()

	start := time.Now()
	ok, detail := probeHTTPDetail(srv.URL)
	assert.False(t, ok)
	assert.Equal(t, "GET "+srv.URL+": no answer within 50ms", detail)
	assert.Less(t, time.Since(start), time.Second)
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

	d = externalProbeHint("onlyoffice", "GET https://docs.test/healthcheck returned HTTP 503 (2 attempts)")
	assert.Contains(t, d, "docservice", "the attempts after the status do not hide the hint")
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

func TestProbeHTTPDetail_RetriesOnceBeforeFailing(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("true"))
	}))
	defer srv.Close()

	ok, detail := probeHTTPDetail(srv.URL)
	assert.True(t, ok)
	assert.Empty(t, detail)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "one failed attempt must be retried")
}

// shortProbeTimeout sets probeTimeout for one test and puts it back after.
func shortProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := probeTimeout
	probeTimeout = d
	t.Cleanup(func() { probeTimeout = old })
}

// silentServer answers nothing until the test ends, and counts what reached it.
func silentServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv, &hits
}

// PR #138 asked a failed probe again whatever the failure. An attempt that got
// no answer spent the whole timeout; a second one doubled how long a silent
// Document Server held /api/capabilities and the server's start.
func TestProbeHTTPDetail_ATimeoutIsNotAskedAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the probe timeout")
	}
	srv, hits := silentServer(t)
	shortProbeTimeout(t, 100*time.Millisecond)

	start := time.Now()
	ok, detail := probeHTTPDetail(srv.URL)
	took := time.Since(start)
	assert.False(t, ok)
	assert.Equal(t, "GET "+srv.URL+": no answer within 100ms", detail, "one attempt, so no count")
	assert.Less(t, took, time.Second)

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), hits.Load(), "a timed-out attempt must not be retried")
}

// A quick failure is asked again, but in what the first attempt left of
// probeTimeout: the whole probe never takes longer than probeTimeout.
func TestProbeHTTPDetail_ASlowFailureLeavesTheRetryOnlyTheTimeLeft(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the probe timeout")
	}
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			time.Sleep(400 * time.Millisecond)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		<-release
	}))
	defer srv.Close()
	defer close(release)
	shortProbeTimeout(t, 600*time.Millisecond)

	start := time.Now()
	ok, detail := probeHTTPDetail(srv.URL)
	took := time.Since(start)
	assert.False(t, ok)
	assert.Equal(t, "GET "+srv.URL+": no answer within 600ms (2 attempts)", detail)
	assert.Equal(t, int32(2), hits.Load())
	// A second attempt with a timeout of its own would end at 400ms + 600ms.
	assert.Less(t, took, 900*time.Millisecond, "the retry waits only for the time the first attempt left")
}

// Every external service silent: the refresh still ends well inside
// refreshBound, and every verdict reaches the database. With PR #138 as it
// came (10s, asked again after a timeout) two silent services took 41s, the
// second verdict was never stored, and the storages read after the probes
// failed with the refresh's own deadline.
func TestService_SilentServicesStayInsideTheRefreshBound(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the probe timeout")
	}
	services := []string{"onlyoffice", "convert", "drawio"}
	assert.LessOrEqual(t, time.Duration(len(services))*probeTimeout, refreshBound/2,
		"a refresh probes one service after the other: all of them silent must stay well inside its bound")

	srv, hits := silentServer(t)
	shortProbeTimeout(t, 200*time.Millisecond)
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	for _, name := range services {
		require.NoError(t, store.UpsertExternalService(ctx, name, true, srv.URL, "jwt", "{}", time.Time{}, "unknown"))
	}

	start := time.Now()
	caps, err := New(store).Get(ctx)
	took := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, took, time.Duration(len(services))*probeTimeout+2*time.Second)
	for _, name := range services {
		assert.Equal(t, "unreachable", caps.External[name].State, name)
		es, err := store.GetExternalService(ctx, name)
		require.NoError(t, err)
		assert.Equal(t, "unreachable", es.LastState, "%s: the verdict is stored", name)
	}
	assert.Equal(t, int32(len(services)), hits.Load(), "one attempt per silent service")
}

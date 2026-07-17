package capability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestHas_KnownBinary expects a process-launching binary that is present
// on every CI image we use:
//   - on linux/macos: `ls`
//   - on windows: `cmd`
func TestHas_KnownBinary(t *testing.T) {
	bin := "ls"
	if runtime.GOOS == "windows" {
		bin = "cmd"
	}
	assert.True(t, has(bin), "expected %q to be in $PATH", bin)
}

// TestHas_MissingBinary checks the negative path with a deliberately
// fake name.
func TestHas_MissingBinary(t *testing.T) {
	assert.False(t, has("definitely-not-a-binary-zxqp-xxx"))
}

// TestProbeHTTP_Local boots a tiny httptest server and probes it. Skipped
// in -short.
func TestProbeHTTP_Local(t *testing.T) {
	if testing.Short() {
		t.Skip("skip network probe in short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	assert.True(t, probeHTTP(srv.URL))
}

// TestProbeHTTP_BadStatus returns false for non-2xx.
func TestProbeHTTP_BadStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skip network probe in short mode")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	assert.False(t, probeHTTP(srv.URL))
}

// TestProbeHTTP_Unreachable returns false for an obviously-down endpoint.
func TestProbeHTTP_Unreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skip network probe in short mode")
	}
	// Reserved-test TLD that won't resolve.
	assert.False(t, probeHTTP("http://127.0.0.1:1/"))
}

// TestService_Get_Caches verifies the second call hits the cache.
func TestService_Get_Caches(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := New(store)
	first, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, second)

	// Mutate first, ensure second is independent (they're returned by value).
	first.Upload = !first.Upload
	assert.NotEqual(t, first.Upload, second.Upload, "Get must return independent copies — modifications to one must not bleed into the other")
}

// TestService_Invalidate forces a refresh.
func TestService_Invalidate(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := New(store)
	svc.cached = nil
	svc.until = time.Time{}
	first, err := svc.Get(context.Background())
	require.NoError(t, err)

	svc.Invalidate()
	assert.Nil(t, svc.cached, "Invalidate should drop the cached value")
	assert.NotNil(t, first, "first call still returned a value")
}

// TestService_StaticInventory_OIDCAutoRedirect — the SSO-first flag flows
// from SetStaticInventory into the snapshot; a service that never got the
// inventory reports false (default-off).
func TestService_StaticInventory_OIDCAutoRedirect(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := New(store)

	caps, err := svc.Get(context.Background())
	require.NoError(t, err)
	assert.False(t, caps.OIDCAutoRedirect, "must default to false")

	svc.SetStaticInventory(
		[]string{"local", "oidc"}, nil,
		"sqlite", false,
		"test", "",
		false, "",
		"",
		true,
	)
	caps, err = svc.Get(context.Background())
	require.NoError(t, err)
	assert.True(t, caps.OIDCAutoRedirect)
}

// TestExternalProbeURL — per-service health paths join onto the base URL
// with trailing slashes collapsed; unknown services keep the raw URL.
func TestExternalProbeURL(t *testing.T) {
	assert.Equal(t, "http://x/healthcheck", externalProbeURL("onlyoffice", "http://x"))
	assert.Equal(t, "http://x/healthcheck", externalProbeURL("onlyoffice", "http://x/"))
	assert.Equal(t, "http://x/healthz", externalProbeURL("convert", "http://x"))
	assert.Equal(t, "http://x/healthz", externalProbeURL("convert", "http://x///"))
	assert.Equal(t, "http://x", externalProbeURL("drawio", "http://x"))
	assert.Equal(t, "http://x/", externalProbeURL("something-else", "http://x/"))
}

// TestService_ExternalHealthPaths — the refresh probe hits the dedicated
// health endpoint of each known service (onlyoffice → /healthcheck,
// convert → /healthz) and keeps the raw URL for the rest (drawio → /).
func TestService_ExternalHealthPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("skip network probe in short mode")
	}
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	// Trailing slash on purpose: must still probe a single /healthcheck.
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, srv.URL+"/", "", "{}", time.Time{}, "unknown"))
	require.NoError(t, store.UpsertExternalService(ctx, "convert", true, srv.URL, "", "{}", time.Time{}, "unknown"))
	require.NoError(t, store.UpsertExternalService(ctx, "drawio", true, srv.URL, "", "{}", time.Time{}, "unknown"))

	svc := New(store)
	caps, err := svc.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ok", caps.External["onlyoffice"].State)
	assert.Equal(t, "ok", caps.External["convert"].State)
	assert.Equal(t, "ok", caps.External["drawio"].State)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, hits["/healthcheck"], "onlyoffice must be probed at /healthcheck (trailing slash collapsed)")
	assert.Equal(t, 1, hits["/healthz"], "convert must be probed at /healthz")
	assert.Equal(t, 1, hits["/"], "drawio keeps the raw-URL probe")
}

// TestService_FailedProbeShortTTL — a failed external probe caches with
// the short failTTL: inside the TTL the negative verdict is served from
// cache, after it the next Get re-probes and recovers to "ok".
func TestService_FailedProbeShortTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skip network probe in short mode")
	}
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "convert", true, srv.URL, "", "{}", time.Time{}, "unknown"))

	svc := New(store)
	svc.failTTL = 40 * time.Millisecond

	caps, err := svc.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "unreachable", caps.External["convert"].State)

	// Service comes back — but inside failTTL the cached verdict holds.
	healthy.Store(true)
	caps, err = svc.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "unreachable", caps.External["convert"].State, "negative verdict must be served from cache inside failTTL")

	// After failTTL the next Get re-probes and clears the banner.
	time.Sleep(60 * time.Millisecond)
	caps, err = svc.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ok", caps.External["convert"].State, "expired failTTL must trigger a re-probe")
}

// TestService_SuccessfulProbeLongTTL — a clean probe round caches with the
// full okTTL even when failTTL is tiny: a later outage stays invisible
// until the long cache expires (asymmetry is fail-side only).
func TestService_SuccessfulProbeLongTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skip network probe in short mode")
	}
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true, srv.URL, "", "{}", time.Time{}, "unknown"))

	svc := New(store)
	svc.failTTL = 10 * time.Millisecond

	caps, err := svc.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ok", caps.External["onlyoffice"].State)

	svc.mu.RLock()
	until := svc.until
	svc.mu.RUnlock()
	assert.Greater(t, time.Until(until), 50*time.Minute, "successful round must cache with the ~1h okTTL")

	// Outage after a clean round: well past failTTL, still served from cache.
	healthy.Store(false)
	time.Sleep(30 * time.Millisecond)
	caps, err = svc.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ok", caps.External["onlyoffice"].State, "okTTL cache must hold; failTTL only applies to failed rounds")
}

// TestService_Get_NoExternalServices — fresh DB returns the canned default
// capabilities (Upload, Move, Copy, Delete, Mkdir all true).
func TestService_Get_Defaults(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	svc := New(store)
	caps, err := svc.Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, caps)
	assert.True(t, caps.Upload)
	assert.True(t, caps.Move)
	assert.True(t, caps.Copy)
	assert.True(t, caps.Delete)
	assert.True(t, caps.Mkdir)
	assert.True(t, caps.Search)
	assert.True(t, caps.Versions)
	assert.True(t, caps.Thumbs.Image, "GD-backed image thumbs are always-on")
	assert.NotNil(t, caps.External)
}

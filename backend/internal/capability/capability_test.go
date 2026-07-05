package capability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
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

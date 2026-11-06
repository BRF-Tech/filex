package s3

// Issue #134: an endpoint with a certificate nobody signed. httptest's server is
// exactly that, and this driver is built the way a storage is, through Init.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

func selfSignedEndpoint(t *testing.T, extra map[string]any) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(listXML("p/")))
	}))
	t.Cleanup(srv.Close)
	cfg := map[string]any{
		"bucket": "b", "prefix": "p", "region": "us-east-1", "endpoint": srv.URL,
		"access_key": "k", "secret_key": "s",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	d := &Driver{}
	require.NoError(t, d.Init(context.Background(), cfg))
	return d
}

func TestSelfSignedEndpoint_RefusedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	_, err := selfSignedEndpoint(t, nil).List(context.Background(), "")

	require.Error(t, err)
	require.True(t, stall.RefusedByTLS(err), "the check stays on by default: %v", err)
}

func TestSelfSignedEndpoint_AcceptedWithTheCheckOff(t *testing.T) {
	_, err := selfSignedEndpoint(t, map[string]any{storage.SkipTLSVerifyKey: true}).List(context.Background(), "")

	require.NoError(t, err)
}

// The whole point of a switch per connection: one storage's answer is not the
// next one's, whichever was opened first.
func TestSelfSignedEndpoint_TheSwitchIsTheStoragesOwn(t *testing.T) {
	on := selfSignedEndpoint(t, map[string]any{storage.SkipTLSVerifyKey: true})
	off := selfSignedEndpoint(t, map[string]any{storage.SkipTLSVerifyKey: false})

	_, err := on.List(context.Background(), "")
	require.NoError(t, err)
	_, err = off.List(context.Background(), "")
	require.Error(t, err)
	require.True(t, stall.RefusedByTLS(err), "%v", err)
}

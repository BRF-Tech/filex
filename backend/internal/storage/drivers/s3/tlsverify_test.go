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

func selfSignedEndpoint(t *testing.T) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(listXML("p/")))
	}))
	t.Cleanup(srv.Close)
	d := &Driver{}
	require.NoError(t, d.Init(context.Background(), map[string]any{
		"bucket": "b", "prefix": "p", "region": "us-east-1", "endpoint": srv.URL,
		"access_key": "k", "secret_key": "s",
	}))
	return d
}

func TestSelfSignedEndpoint_RefusedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	_, err := selfSignedEndpoint(t).List(context.Background(), "")

	require.Error(t, err)
	require.True(t, stall.RefusedByTLS(err), "the check stays on by default: %v", err)
}

func TestSelfSignedEndpoint_AcceptedWithTheCheckOff(t *testing.T) {
	storage.SetSkipTLSVerify(true)
	t.Cleanup(func() { storage.SetSkipTLSVerify(false) })

	_, err := selfSignedEndpoint(t).List(context.Background(), "")

	require.NoError(t, err)
}

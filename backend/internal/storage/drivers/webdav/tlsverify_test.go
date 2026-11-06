package webdav

// Issue #134: a server with a certificate nobody signed, which httptest's is.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

func selfSignedServer(t *testing.T) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"></D:multistatus>`))
	}))
	t.Cleanup(srv.Close)
	return newDavDriver(t, srv)
}

func TestSelfSignedServer_RefusedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	_, err := selfSignedServer(t).List(context.Background(), "/")

	require.Error(t, err)
	require.True(t, stall.RefusedByTLS(err), "the check stays on by default: %v", err)
}

func TestSelfSignedServer_AcceptedWithTheCheckOff(t *testing.T) {
	storage.SetSkipTLSVerify(true)
	t.Cleanup(func() { storage.SetSkipTLSVerify(false) })

	_, err := selfSignedServer(t).List(context.Background(), "/")

	require.NoError(t, err)
}

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

func selfSignedServer(t *testing.T, extra map[string]any) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"></D:multistatus>`))
	}))
	t.Cleanup(srv.Close)
	cfg := map[string]any{"url": srv.URL, "user": "u", "password": "p"}
	for k, v := range extra {
		cfg[k] = v
	}
	d := &Driver{}
	require.NoError(t, d.Init(context.Background(), cfg))
	return d
}

func TestSelfSignedServer_RefusedUnlessTheOperatorSaysOtherwise(t *testing.T) {
	_, err := selfSignedServer(t, nil).List(context.Background(), "/")

	require.Error(t, err)
	require.True(t, stall.RefusedByTLS(err), "the check stays on by default: %v", err)
}

func TestSelfSignedServer_AcceptedWithTheCheckOff(t *testing.T) {
	_, err := selfSignedServer(t, map[string]any{storage.SkipTLSVerifyKey: true}).List(context.Background(), "/")

	require.NoError(t, err)
}

func TestSelfSignedServer_TheSwitchIsTheStoragesOwn(t *testing.T) {
	on := selfSignedServer(t, map[string]any{storage.SkipTLSVerifyKey: true})
	off := selfSignedServer(t, nil)

	_, err := on.List(context.Background(), "/")
	require.NoError(t, err)
	_, err = off.List(context.Background(), "/")
	require.Error(t, err)
	require.True(t, stall.RefusedByTLS(err), "%v", err)
}

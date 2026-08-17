package webdav

// A WebDAV server may answer a Range request with 200 and the whole body.
// Handing that back as if it started at the offset is silent corruption —
// the caller writes bytes 0..N into a window that should have started at
// 500 — so these tests pin the refusal, not just the happy path.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
)

const davBody = "0123456789abcdefghijklmnopqrstuvwxyz"

// newDavDriver points a driver at srv with Basic auth that the test
// servers below ignore.
func newDavDriver(t *testing.T, srv *httptest.Server) *Driver {
	t.Helper()
	d := &Driver{}
	require.NoError(t, d.Init(context.Background(), map[string]any{
		"url": srv.URL, "user": "u", "password": "p",
	}))
	return d
}

// TestReadRange_HonouringServer — the normal case: 206 + exactly the
// window, and the request really carried the Range header.
func TestReadRange_HonouringServer(t *testing.T) {
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		http.ServeContent(w, r, "f.bin", time.Time{}, strings.NewReader(davBody))
	}))
	defer srv.Close()

	rc, err := newDavDriver(t, srv).ReadRange(context.Background(), "f.bin", 10, 5)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)

	require.Equal(t, "bytes=10-14", gotRange)
	require.Equal(t, davBody[10:15], string(b))
}

// TestReadRange_ServerIgnoresRange — the dangerous case. A 200 at a
// non-zero offset must be an error; returning the body would be the wrong
// bytes with no way for the caller to notice.
func TestReadRange_ServerIgnoresRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, davBody)
	}))
	defer srv.Close()

	_, err := newDavDriver(t, srv).ReadRange(context.Background(), "f.bin", 10, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ignored Range")
}

// TestReadRange_ServerIgnoresRangeAtZero — at offset 0 the whole body IS
// the requested window's superset, so capping it is correct data, not a
// guess. Refusing here would break every non-ranging server for ordinary
// downloads.
func TestReadRange_ServerIgnoresRangeAtZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, davBody)
	}))
	defer srv.Close()

	rc, err := newDavDriver(t, srv).ReadRange(context.Background(), "f.bin", 0, 5)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, davBody[:5], string(b))
}

// TestReadRange_PastEOFIsEmpty — a 416 is a short read, not a failure.
func TestReadRange_PastEOFIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f.bin", time.Time{}, strings.NewReader(davBody))
	}))
	defer srv.Close()

	rc, err := newDavDriver(t, srv).ReadRange(context.Background(), "f.bin", 9000, -1)
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Empty(t, b)
}

// TestReadRange_MissingIsErrNotFound keeps the error mapping identical to
// Read's, so callers need only one check.
func TestReadRange_MissingIsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newDavDriver(t, srv).ReadRange(context.Background(), "gone.bin", 0, -1)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestReadRange_Capability(t *testing.T) {
	require.True(t, storage.ComputeCapabilities(&Driver{}).Range)
	var _ storage.RangeReader = &Driver{}
}

package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChanges_WireShape(t *testing.T) {
	var gotPath, gotSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		require.Equal(t, "changes", q.Get("action"))
		gotPath, gotSince = q.Get("path"), q.Get("since")
		_ = json.NewEncoder(w).Encode(map[string]any{"cursor": "e.7", "changed": gotSince != "e.7"})
	}))
	t.Cleanup(srv.Close)
	api := New(Conn{URL: srv.URL, Token: "t"})

	cur, changed, err := api.Changes(context.Background(), "docs://04 Projeler", "")
	require.NoError(t, err)
	require.Equal(t, "docs://04 Projeler", gotPath)
	require.Equal(t, "", gotSince)
	require.True(t, changed)
	require.Equal(t, "e.7", cur)

	_, changed, err = api.Changes(context.Background(), "docs://04 Projeler", cur)
	require.NoError(t, err)
	require.False(t, changed)
}

// An older server answers the unknown action with 501: the caller must be
// told to walk, not handed an error to report.
func TestChanges_AnOlderServerIsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"action not implemented: changes"}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := New(Conn{URL: srv.URL, Token: "t"}).Changes(context.Background(), "docs://x", "")
	require.ErrorIs(t, err, ErrChangesUnsupported)
}

func TestChanges_A401IsStillA401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := New(Conn{URL: srv.URL, Token: "t"}).Changes(context.Background(), "docs://x", "")
	require.True(t, IsUnauthorized(err))
}

package cliclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// UploadTo is the call `filex sync` makes. Two properties matter to the engine:
// the precondition reaches the server, and a refusal comes back as a TYPED
// error the engine can turn into "changed on the server, keep both".
func TestUploadTo_SendsThePreconditionAndTypesTheRefusal(t *testing.T) {
	var mu sync.Mutex
	var seen []string // method + action, in order
	var expect string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.Method+" "+r.URL.Query().Get("action"))
		if r.URL.Query().Get("action") == "upload" {
			require.NoError(t, r.ParseMultipartForm(1<<20))
			expect = r.FormValue("expect")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"error":"the file changed on the server since it was listed","code":"PRECONDITION_FAILED"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	local := filepath.Join(t.TempDir(), "note.txt")
	require.NoError(t, os.WriteFile(local, []byte("desktop edit"), 0o644))
	c := &Client{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	dir, err := ParseRemotePath("docs://proj")
	require.NoError(t, err)

	_, err = c.UploadTo(context.Background(), local, dir, "note.txt", "12:1789999692587")
	require.True(t, errors.Is(err, ErrPreconditionFailed), "want ErrPreconditionFailed, got %v", err)
	require.Equal(t, "12:1789999692587", expect, "the precondition must reach the server")
	// ⚠ No destination probe in front of the upload: that listing was a whole
	// round-trip per synced file.
	require.Equal(t, []string{"POST upload"}, seen)
}

package mountfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// spaceServer answers action=space with body (status 200) or status, and
// records the paths it was asked about.
func spaceServer(t *testing.T, status int, body string) (*cliclient.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "space" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("path"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return cliclient.New(cliclient.Conn{URL: srv.URL}), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

// Issue #116, the `filex mount` side: the drive was 1 PiB on Linux and 4 TiB
// half full on Windows whatever the server had.
func TestSpace_IsTheServersFigures(t *testing.T) {
	cl, asked := spaceServer(t, http.StatusOK, `{"total":1000,"used":400,"free":550,"known":true}`)
	f, err := New(Config{Client: cl, Remote: "docs://projects"})
	if err != nil {
		t.Fatal(err)
	}
	got := f.Space(context.Background(), "/acme")
	if got.Total != 1000 || got.Used != 400 || got.Free != 550 || !got.Known {
		t.Errorf("got %+v", got)
	}
	if a := asked(); len(a) != 1 || a[0] != "docs://projects/acme" {
		t.Errorf("asked about %v, want the remote path docs://projects/acme", a)
	}
}

// Without --remote the mount point is the storage list, and its size is the
// storages added up: the server is asked with no path.
func TestSpace_TheStorageListAsksWithoutAPath(t *testing.T) {
	cl, asked := spaceServer(t, http.StatusOK, `{"total":1000,"used":0,"free":1000,"known":true}`)
	f, err := New(Config{Client: cl})
	if err != nil {
		t.Fatal(err)
	}
	f.Space(context.Background(), "/")
	if a := asked(); len(a) != 1 || a[0] != "" {
		t.Errorf("asked about %q, want the storage list", a)
	}
}

// Explorer asks on every folder; the server is asked once per window.
func TestSpace_IsAskedOncePerWindow(t *testing.T) {
	cl, asked := spaceServer(t, http.StatusOK, `{"total":1000,"used":0,"free":1000,"known":true}`)
	f, err := New(Config{Client: cl})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		f.Space(context.Background(), "/")
	}
	if n := len(asked()); n != 1 {
		t.Errorf("asked %d times, want 1", n)
	}
}

// A server older than the question answers 501. The drive is then the
// stand-in, large and empty: zero would make Explorer refuse to copy.
func TestSpace_AnOlderServerGetsTheStandIn(t *testing.T) {
	cl, _ := spaceServer(t, http.StatusNotImplemented, `{"error":"action not implemented: space"}`)
	f, err := New(Config{Client: cl})
	if err != nil {
		t.Fatal(err)
	}
	got := f.Space(context.Background(), "/")
	blocks, _, bavail := got.Blocks(4096)
	if blocks == 0 || bavail == 0 {
		t.Errorf("an older server made the drive %+v", got)
	}
}

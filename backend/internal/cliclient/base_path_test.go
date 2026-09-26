package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server served under a sub-path (FILEX_BASE_PATH): `filex client login
// --url https://example.com/filex` must reach /filex/api/…, not /api/… on the
// host's root. The client joins by string, which keeps the prefix; this pins
// it, together with the realtime socket address.
func TestClientKeepsTheServerPath(t *testing.T) {
	_, inner := newFakeServer(t)
	var seen []string
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if !strings.HasPrefix(r.URL.Path, "/filex/") {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/filex")
		r2.RequestURI = ""
		r2.URL.Scheme, r2.URL.Host = "http", strings.TrimPrefix(inner.URL, "http://")
		resp, err := http.DefaultTransport.RoundTrip(r2)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 32<<10)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
	}))
	t.Cleanup(outer.Close)

	api := New(Conn{URL: outer.URL + "/filex/", Token: "good-token"})
	res, err := api.List(context.Background(), "docs://")
	require.NoError(t, err)
	assert.Equal(t, "docs", res.Adapter)
	require.NotEmpty(t, seen)
	assert.Equal(t, "/filex/api/files/manager", seen[len(seen)-1])

	assert.Equal(t, strings.Replace(outer.URL, "http://", "ws://", 1)+"/filex/api/ws", api.webSocketURL())
}

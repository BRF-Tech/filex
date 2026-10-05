package wasmplugin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A download still follows a redirect: GitHub answers a release asset and a
// raw file with one (to objects.githubusercontent.com and the like). It is a
// GET with no body and nothing secret in it, so following it hands nothing
// to the next host - unlike a store's API (internal/appstore), which carries
// a license key and follows none.
func TestFetch_ADownloadFollowsARedirectAndCarriesNothing(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" auth="+r.Header.Get("Authorization")+" cookie="+r.Header.Get("Cookie")+" body="+string(b))
		mu.Unlock()
		_, _ = w.Write(packManifest(t, "lang-eo", map[string]map[string]string{"eo": {"common.cancel": "Nuligi"}}))
	}))
	t.Cleanup(assets.Close)
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, assets.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(raw.Close)
	reg, _ := newPackRegistry(t, func(o *Options) {
		o.LoopbackSources = true
		o.GitHubRawBase = raw.URL
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in, err := reg.FetchGitHub(ctx, GitHubInput{Repo: "Owner/lang-eo", Ref: "v1.0.0"})
	require.NoError(t, err, "a redirected download")
	assert.NotEmpty(t, in.Manifest)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 1)
	assert.Equal(t, "GET auth= cookie= body=", seen[0])
}

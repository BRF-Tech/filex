package wasmplugin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registry's OWN client - the one a server gets, because internal/server
// supplies none - downloads from public addresses only. A manifest (or a
// module, or an interface bundle) on this machine or the private network is
// refused before a byte is asked for: an install request an API key leaves,
// an administrator's install and the daily update check all go through it,
// and none of them may turn into a request to filex's own neighbours.
func TestFetch_TheDefaultClientRefusesThisMachine(t *testing.T) {
	var hits atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(packManifest(t, "lang-eo", map[string]map[string]string{"eo": {"common.cancel": "Nuligi"}}))
	}))
	t.Cleanup(src.Close)
	reg, _ := newPackRegistry(t, nil)
	// A refusal is immediate; the deadline only keeps an unguarded client
	// from waiting on a private address that does not answer.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, u := range []string{
		src.URL + "/filex-app.json",
		strings.Replace(src.URL, "127.0.0.1", "localhost", 1) + "/filex-app.json",
		"https://10.0.0.5/filex-app.json",
		"https://169.254.169.254/latest/meta-data",
		"https://[fd00::1]/filex-app.json",
	} {
		_, err := reg.FetchURL(ctx, URLInput{ManifestURL: u})
		var ie *InstallError
		require.True(t, errors.As(err, &ie), "%s: want an install error, got %v", u, err)
		assert.Equal(t, ErrCodeFetch, ie.Code, u)
		assert.Equal(t, FetchReasonBadURL, ie.Reason, "%s: said as an address filex does not download from", u)
	}
	assert.Zero(t, hits.Load(), "nothing on this machine was asked")
}

// FILEX_PLUGIN_LOOPBACK_SOURCES (development, the end-to-end tests) opens
// THIS machine to the downloads, and nothing else: the private network stays
// refused, by literal and on a redirect hop.
func TestFetch_LoopbackSourcesReachThisMachineOnly(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/away" {
			http.Redirect(w, r, "https://10.0.0.5/filex-app.json", http.StatusFound)
			return
		}
		_, _ = w.Write(packManifest(t, "lang-eo", map[string]map[string]string{"eo": {"common.cancel": "Nuligi"}}))
	}))
	t.Cleanup(src.Close)
	reg, _ := newPackRegistry(t, func(o *Options) { o.LoopbackSources = true })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	in, err := reg.FetchURL(ctx, URLInput{ManifestURL: src.URL + "/filex-app.json"})
	require.NoError(t, err, "a loopback source is reachable with the setting on")
	assert.NotEmpty(t, in.Manifest)

	for _, u := range []string{"https://10.0.0.5/filex-app.json", src.URL + "/away"} {
		_, err := reg.FetchURL(ctx, URLInput{ManifestURL: u})
		var ie *InstallError
		require.True(t, errors.As(err, &ie), "%s: want an install error, got %v", u, err)
		assert.Equal(t, FetchReasonBadURL, ie.Reason, u)
	}
}

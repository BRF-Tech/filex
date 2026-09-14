package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The connection guides print the address a client program is pointed at (the
// WebDAV URL, `filex mount`, rclone). They built it from wherever the page was
// loaded, so an explorer embedded in another app — which proxies /api under its
// own origin — told people to mount `https://<that app>/dav/`. The server knows
// its public address; this pins that it says so, and ONLY when it is real.
func TestCapabilities_PublicURL(t *testing.T) {
	get := func(t *testing.T, srv string, client *http.Client) map[string]any {
		t.Helper()
		res, err := client.Get(srv + "/api/files/capabilities")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out
	}

	t.Run("operator set it: published", func(t *testing.T) {
		srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) {
			c.PublicURL = "https://files.example.com"
			c.PublicURLSet = true
		})
		require.Equal(t, "https://files.example.com", get(t, srv.URL, client)["public_url"])
	})

	t.Run("built-in guess: never announced", func(t *testing.T) {
		srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) {
			c.PublicURL = config.DefaultPublicURL
			c.PublicURLSet = false
		})
		_, present := get(t, srv.URL, client)["public_url"]
		require.False(t, present,
			"the default %s was published — a guide would send every client to the reader's own machine",
			config.DefaultPublicURL)
	})
}

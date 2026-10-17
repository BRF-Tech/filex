package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/loginguard"
)

// FILEX_TRUSTED_PROXIES seeds the saved list since 0.57 (task #258). The page
// says so: "seeded" while the saved list is the variable's own, the
// variable's list named beside a saved one that overrules it, and nothing
// when the configuration names no list.

func TestLoginSecurityTrustedProxiesSeededFromTheVariable(t *testing.T) {
	const list = "auto, 172.18.0.1"
	t.Setenv(loginguard.ProxyEnvVar, list)
	e := newSecEnv(t, func(c *config.Config) { c.TrustedProxies = list })
	t.Cleanup(clientip.SetAutoResolver(dockerAuto))

	// What a first 0.57 start does before the limiter loads.
	_, err := loginguard.SeedTrustedProxies(context.Background(), e.store, list)
	require.NoError(t, err)
	e.guard.Invalidate()

	_, body := e.do(t, http.MethodGet, "", nil)
	require.Equal(t, "seeded", body["trusted_proxies_source"])
	require.Equal(t, []any{"auto", "172.18.0.1"}, body["settings"].(map[string]any)["trusted_proxies"])
	require.Equal(t, []any{"loopback", "172.18.0.0/16", "172.18.0.1"}, body["trusted_proxies_effective"],
		"the same list as before the upgrade: auto, and the gateway named by hand")
	require.Equal(t, map[string]any{
		"source": "FILEX_TRUSTED_PROXIES", "entries": []any{"auto", "172.18.0.1"}, "differs": false,
	}, body["trusted_proxies_configured"])

	// Saved on the page: the saved list wins, the variable's is named beside it.
	status, body := e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "auto, 192.0.2.10"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, "setting", body["trusted_proxies_source"])
	require.Equal(t, true, body["trusted_proxies_configured"].(map[string]any)["differs"])

	// The same list in another spelling is the variable's own.
	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "172.18.0.1,AUTO"})
	require.Equal(t, "seeded", body["trusted_proxies_source"])
	require.Equal(t, false, body["trusted_proxies_configured"].(map[string]any)["differs"])
}

func TestLoginSecurityTrustedProxiesNoConfiguredList(t *testing.T) {
	e := newSecEnv(t, nil)
	_, body := e.do(t, http.MethodGet, "", nil)
	v, ok := body["trusted_proxies_configured"]
	require.True(t, ok, "the field is always there")
	require.Nil(t, v, "the configuration names no list")
}

func TestDemo_TheConfiguredProxyListIsMasked(t *testing.T) {
	const list = "auto, 10.9.8.7"
	e := newSecEnv(t, func(c *config.Config) {
		c.Demo.Mode = true
		c.TrustedProxies = list
	})
	_, body := e.do(t, http.MethodGet, "", nil)
	raw, err := json.Marshal(body["trusted_proxies_configured"])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "10.9.8.7", "the operator's proxy is not shown on a public demo")
}

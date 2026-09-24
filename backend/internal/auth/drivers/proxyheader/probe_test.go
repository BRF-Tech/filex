package proxyheader

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// The header-trust test reads the administrator's own request: it must not
// pass a configuration with no trusted range, and it must say — not fail —
// when the request did not come through the proxy at all.
func TestProbe_HeaderTrust(t *testing.T) {
	d := &Driver{}
	checks := d.Probe(context.Background(), map[string]any{"trusted_proxies": ""}, nil)
	assert.Equal(t, auth.ProbeFail, checks[0].Status)
	assert.Equal(t, "trusted_proxies", checks[0].Params["fields"])

	checks = d.Probe(context.Background(), map[string]any{"trusted_proxies": "10.0.0.0/8, not-an-ip"}, nil)
	assert.Equal(t, "trusted_proxies", checks[len(checks)-1].ID)
	assert.Equal(t, auth.ProbeFail, checks[len(checks)-1].Status)

	direct := httptest.NewRequest("GET", "/", nil)
	direct.RemoteAddr = "203.0.113.7:5555"
	checks = d.Probe(context.Background(), map[string]any{"trusted_proxies": "10.0.0.0/8"}, direct)
	last := checks[len(checks)-1]
	assert.Equal(t, "via_proxy", last.ID)
	assert.Equal(t, auth.ProbeUnchecked, last.Status, "a direct request is not a broken proxy")
	assert.Equal(t, "203.0.113.7", last.Params["peer"])

	proxied := httptest.NewRequest("GET", "/", nil)
	proxied.RemoteAddr = "10.1.2.3:5555"
	checks = d.Probe(context.Background(), map[string]any{"trusted_proxies": "10.0.0.0/8"}, proxied)
	assert.Equal(t, auth.ProbeFail, checks[len(checks)-1].Status, "through the proxy, but no user header")
	proxied.Header.Set("X-Auth-User", "ayse")
	checks = d.Probe(context.Background(), map[string]any{"trusted_proxies": "10.0.0.0/8"}, proxied)
	assert.True(t, auth.ProbeOKAll(checks))
	assert.Equal(t, "ayse", checks[len(checks)-1].Params["user"])
}

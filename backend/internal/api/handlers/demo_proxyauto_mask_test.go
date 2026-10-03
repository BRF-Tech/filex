package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/clientip"
)

// The trusted-proxy detail of GET /api/admin/login-security carries
// addresses too: the untrusted forwarders are OTHER people's proxies (every
// visitor whose own proxy adds X-Forwarded-For shows up there), and the
// networks, gateways and interface addresses `auto` found are the operator's
// layout. The masking of that detail (maskProxyAutoForDemo) and the demo view
// that calls it (maskViewForDemo) were written on two branches and each
// tested on its own; this is the test of the connection, over HTTP, on every
// door that reads the answer.

const (
	forwarderPeer = "198.51.100.200"
	autoNetwork   = "172.31.7.0/24"
	autoGateway   = "172.31.7.1"
	autoSelf      = "172.31.7.9"
	autoIfaceAddr = "172.31.7.9/24"
)

var proxyAutoSecrets = []string{forwarderPeer, "172.31.7."}

// seedProxyAuto makes `auto` answer a container network and remembers one
// untrusted forwarder, the way a request carrying X-Forwarded-For from a peer
// that is not trusted leaves it.
func seedProxyAuto(t *testing.T) {
	t.Helper()
	t.Cleanup(clientip.SetAutoResolver(func() clientip.AutoResolution {
		return clientip.AutoResolution{
			Environment:      "container",
			Runtime:          "docker",
			Networks:         []string{autoNetwork},
			ExcludedGateways: []string{autoGateway},
			ExcludedSelf:     []string{autoSelf},
			Interfaces: []clientip.AutoInterface{
				{Name: "eth0", Kind: "veth", Addresses: []string{autoIfaceAddr}, Trusted: true, Reason: clientip.ReasonContainerNetwork},
			},
			ResolvedAt: time.Now(),
		}
	}))
	clientip.ResetForwarders()
	t.Cleanup(clientip.ResetForwarders)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = netip.AddrPortFrom(netip.MustParseAddr(forwarderPeer), 40000).String()
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	clientip.Observe(r)
	require.NotEmpty(t, clientip.UntrustedForwarders(), "the forwarder was remembered")
}

func proxyAutoLeaked(body string) []string {
	var out []string
	for _, s := range proxyAutoSecrets {
		if strings.Contains(body, s) {
			out = append(out, s)
		}
	}
	return out
}

func TestDemo_TrustedProxyDetailIsHiddenOnEveryDoor(t *testing.T) {
	e, tok := newMaskEnv(t, true)
	seedProxyAuto(t)

	for _, p := range []string{"/api/admin/login-security", "/api/ai/admin/login-security"} {
		status, body := e.get(t, tok, p)
		require.Equal(t, http.StatusOK, status, p)
		require.Empty(t, proxyAutoLeaked(body), "GET %s shows the trusted-proxy detail's addresses on a demo", p)
		require.Contains(t, body, `"untrusted_forwarders":[{"address":"hidden on the demo"`, p)
		require.Contains(t, body, `"environment":"container"`, "%s: why auto decided what it did stays", p)
	}
	body := mcpTool(t, e.srv.URL, tok, "admin_login_security_get", `{}`)
	require.Empty(t, proxyAutoLeaked(body), "MCP admin_login_security_get shows the trusted-proxy detail's addresses on a demo")
	require.Contains(t, body, "hidden on the demo")
}

// The same seed on an ordinary install: the operator reads every address,
// which is what proves the demo had something to hide.
func TestOrdinaryInstall_TrustedProxyDetailIsShown(t *testing.T) {
	e, tok := newMaskEnv(t, false)
	seedProxyAuto(t)
	status, body := e.get(t, tok, "/api/admin/login-security")
	require.Equal(t, http.StatusOK, status)
	for _, s := range []string{forwarderPeer, autoNetwork, autoGateway, autoSelf, autoIfaceAddr} {
		require.Contains(t, body, s)
	}
}

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/clientip"
)

func observeFrom(peer string) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = peer
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	clientip.Observe(r)
}

func containerAuto(t *testing.T) {
	t.Helper()
	t.Cleanup(clientip.SetSource(nil))
	t.Cleanup(clientip.SetAutoResolver(func() clientip.AutoResolution {
		return clientip.AutoResolution{
			Environment: clientip.EnvContainer, Runtime: clientip.RuntimeDocker,
			Networks: []string{"172.18.0.0/16"}, ExcludedGateways: []string{"172.18.0.1"}, ExcludedSelf: []string{"172.18.0.5"},
			Interfaces: []clientip.AutoInterface{{Name: "eth0", Kind: "veth", Addresses: []string{"172.18.0.5/16"}, Trusted: true, Reason: clientip.ReasonContainerNetwork}},
		}
	}))
	clientip.ResetForwarders()
	t.Cleanup(clientip.ResetForwarders)
}

// What the page needs to word its "trust it?" question: a public address, an
// address relayed connections arrive from (the gateway, filex's own).
func TestUntrustedForwarderFlags(t *testing.T) {
	containerAuto(t)
	for _, p := range []string{"172.18.0.1:1", "172.18.0.5:1", "203.0.113.9:1", "192.168.1.20:1", "[2001:db8::7]:1", "[::ffff:172.18.0.1]:1"} {
		observeFrom(p)
	}
	observeFrom("172.18.0.9:1") // a container on the network: trusted, never listed
	list, total := (&LoginSecurity{}).forwarders()
	require.Equal(t, 5, total)
	got := map[string][2]bool{}
	for _, f := range list {
		got[f.Address] = [2]bool{f.Public, f.Relay}
	}
	require.Equal(t, map[string][2]bool{
		"172.18.0.1":   {false, true},
		"172.18.0.5":   {false, true},
		"203.0.113.9":  {true, false},
		"192.168.1.20": {false, false},
		"2001:db8::7":  {true, false},
	}, got, "an IPv4-mapped peer is the IPv4 address it carries")
}

// One answer carries at most maxForwardersShown, the busiest first; the total
// says how many are remembered.
func TestUntrustedForwardersAreCappedInTheAnswer(t *testing.T) {
	containerAuto(t)
	for i := 0; i < maxForwardersShown+10; i++ {
		observeFrom(fmt.Sprintf("203.0.113.%d:1", i+1))
	}
	observeFrom("203.0.113.200:1")
	observeFrom("203.0.113.200:1")
	list, total := (&LoginSecurity{}).forwarders()
	require.Len(t, list, maxForwardersShown)
	require.Equal(t, maxForwardersShown+11, total)
	require.Equal(t, "203.0.113.200", list[0].Address)
}

// On a public demo the forwarders are other people's addresses and the
// networks `auto` found are the operator's: all masked, on copies - the
// resolution in force is not written to - while what each forwarder is
// (public, relay, how often) and why `auto` decided stay.
func TestProxyAutoIsMaskedOnTheDemo(t *testing.T) {
	containerAuto(t)
	observeFrom("172.18.0.1:1")
	observeFrom("203.0.113.9:1")
	h := &LoginSecurity{}
	list, total := h.forwarders()
	resp := loginSecurityResponse{
		UntrustedForwarders: list, UntrustedForwardersTotal: total,
		TrustedProxiesAuto: trustedAutoView{AutoResolution: clientip.Auto(), InUse: true},
	}
	maskProxyAutoForDemo(&resp)

	for _, f := range resp.UntrustedForwarders {
		require.Equal(t, demoMaskedIP, f.Address)
		require.EqualValues(t, 1, f.Count)
	}
	require.ElementsMatch(t, []bool{true, false}, []bool{resp.UntrustedForwarders[0].Public, resp.UntrustedForwarders[1].Public})
	a := resp.TrustedProxiesAuto
	require.Equal(t, []string{demoMaskedIP}, a.Networks)
	require.Equal(t, []string{demoMaskedIP}, a.ExcludedGateways)
	require.Equal(t, []string{demoMaskedIP}, a.ExcludedSelf)
	require.Equal(t, []string{demoMaskedIP}, a.Interfaces[0].Addresses)
	require.Equal(t, "eth0", a.Interfaces[0].Name)
	require.Equal(t, clientip.ReasonContainerNetwork, a.Interfaces[0].Reason)
	require.Equal(t, clientip.EnvContainer, a.Environment)

	b, err := json.Marshal(resp)
	require.NoError(t, err)
	require.NotContains(t, string(b), "172.18.")
	require.NotContains(t, string(b), "203.0.113")

	// The resolution in force is untouched.
	require.Equal(t, []string{"172.18.0.0/16"}, clientip.Auto().Networks)
	require.Equal(t, []string{"172.18.0.5/16"}, clientip.Auto().Interfaces[0].Addresses)
}

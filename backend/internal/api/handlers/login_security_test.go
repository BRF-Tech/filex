package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The administrator's surface of the sign-in limit: /api/admin/login-security.

type secEnv struct {
	srv    *httptest.Server
	admin  *http.Client
	store  db.Store
	guard  *loginguard.Guard
	email  string
	passwd string
}

func newSecEnv(t *testing.T, cfgMutate func(*config.Config)) *secEnv {
	t.Helper()
	e := &secEnv{}
	srv, client, store := testutil.NewTestServerWith(t, cfgMutate, func(d *api.Deps) {
		e.guard = loginguard.New(d.Store)
		d.LoginGuard = e.guard
	})
	e.srv, e.admin, e.store = srv, client, store
	e.email, e.passwd = testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, e.email, e.passwd)
	t.Cleanup(clientip.SetSource(nil))
	// `auto` as on a plain install (the machine running the tests may be a
	// container), and no forwarder remembered from another test.
	t.Cleanup(clientip.SetAutoResolver(func() clientip.AutoResolution {
		return clientip.AutoResolution{Environment: clientip.EnvPlain}
	}))
	clientip.ResetForwarders()
	t.Cleanup(clientip.ResetForwarders)
	return e
}

// dockerAuto is `auto` as a container on one Docker network resolved it.
func dockerAuto() clientip.AutoResolution {
	return clientip.AutoResolution{
		Environment: clientip.EnvContainer, Runtime: clientip.RuntimeDocker,
		Networks: []string{"172.18.0.0/16"}, ExcludedGateways: []string{"172.18.0.1"}, ExcludedSelf: []string{"172.18.0.5"},
		Interfaces: []clientip.AutoInterface{{Name: "eth0", Kind: "veth", Addresses: []string{"172.18.0.5/16"}, Trusted: true, Reason: clientip.ReasonContainerNetwork}},
	}
}

func (e *secEnv) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	return doJSON(t, e.admin, method, e.srv.URL+"/api/admin/login-security"+path, body)
}

// wrong makes n wrong sign-in attempts as someone else (a fresh client with no
// session, coming from ip).
func (e *secEnv) wrong(t *testing.T, ip, identifier string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/auth/login",
			strings.NewReader(`{"email":"`+identifier+`","password":"wrong"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
	}
}

func TestLoginSecurityGetDefaults(t *testing.T) {
	e := newSecEnv(t, nil)
	status, body := e.do(t, http.MethodGet, "", nil)
	require.Equal(t, http.StatusOK, status)

	s := body["settings"].(map[string]any)
	require.Equal(t, true, s["enabled"])
	require.EqualValues(t, 5, s["account_max_fails"])
	require.EqualValues(t, 10, s["ip_max_fails"])
	require.EqualValues(t, 600, s["window_seconds"])
	require.EqualValues(t, 60, s["lock_base_seconds"])
	require.EqualValues(t, 900, s["lock_max_seconds"])
	require.Equal(t, []any{}, s["ip_allowlist"])
	require.Equal(t, []any{}, s["trusted_proxies"])

	lim := body["limits"].(map[string]any)["account_max_fails"].(map[string]any)
	require.EqualValues(t, 1, lim["min"])
	require.EqualValues(t, 1000, lim["max"])
	require.EqualValues(t, 5, lim["default"])

	require.Equal(t, "auto", body["trusted_proxies_source"], "neither the setting nor the environment: the automatic set")
	require.Equal(t, []any{"loopback"}, body["trusted_proxies_effective"],
		"a plain install trusts this machine alone - not the LAN, not every private address")
	require.Equal(t, map[string]any{"auto": true, "loopback": false, "private": false, "link_local": false}, body["trusted_defaults"])
	require.Equal(t, []any{}, body["trusted_addresses"])
	auto := body["trusted_proxies_auto"].(map[string]any)
	require.Equal(t, true, auto["in_use"])
	require.Equal(t, "plain", auto["environment"])
	require.Equal(t, []any{}, auto["networks"])
	require.Equal(t, []any{}, auto["excluded_gateways"])
	require.Equal(t, []any{}, auto["excluded_self"])
	require.Equal(t, []any{}, auto["interfaces"])
	require.Equal(t, "", auto["warning"])
	require.Equal(t, []any{}, body["untrusted_forwarders"])
	require.EqualValues(t, 0, body["untrusted_forwarders_total"])
	require.Equal(t, "127.0.0.1", body["your_ip"], "the address filex sees this call from")
	require.Equal(t, false, body["your_ip_allowlisted"])
}

func TestLoginSecurityPatchAppliesToTheNextAttempt(t *testing.T) {
	e := newSecEnv(t, nil)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")

	status, body := e.do(t, http.MethodPatch, "", map[string]any{
		"account_max_fails": 2, "lock_base_seconds": 30, "lock_max_seconds": 120,
		"ip_allowlist": []string{"192.0.2.5", "198.51.100.0/24"},
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	s := body["settings"].(map[string]any)
	require.EqualValues(t, 2, s["account_max_fails"])
	require.Equal(t, []any{"192.0.2.5", "198.51.100.0/24"}, s["ip_allowlist"])

	// Two misses lock now (from an address that is not on the list).
	e.wrong(t, "203.0.113.1", "ada@example.com", 2)
	_, locks := e.do(t, http.MethodGet, "/locks?locked=1", nil)
	items := locks["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, "account", items[0].(map[string]any)["scope"])
	require.EqualValues(t, 30, items[0].(map[string]any)["retry_after"], "the new base length")
}

func TestLoginSecurityPatchIsAllOrNothing(t *testing.T) {
	e := newSecEnv(t, nil)
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"limit out of range", map[string]any{"ip_max_fails": 0}, "ip_max_fails"},
		{"limit too big", map[string]any{"account_max_fails": 5000}, "account_max_fails"},
		{"window too short", map[string]any{"window_seconds": 1}, "window_seconds"},
		{"bad allow-list entry", map[string]any{"ip_allowlist": []string{"10.0.0.1", "not-an-ip"}}, "ip_allowlist"},
		{"allow-list keyword", map[string]any{"ip_allowlist": "private"}, "ip_allowlist"},
		{"bad proxy entry", map[string]any{"trusted_proxies": "caddy"}, "trusted_proxies"},
		{"ceiling under the base", map[string]any{"lock_base_seconds": 300, "lock_max_seconds": 100}, "lock_max_seconds"},
		{"not a list", map[string]any{"ip_allowlist": 7}, "ip_allowlist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A valid field rides along: it must NOT be written either.
			tc.body["enabled"] = false
			status, body := e.do(t, http.MethodPatch, "", tc.body)
			require.Equal(t, http.StatusBadRequest, status, "%v", body)
			require.Equal(t, "invalid_setting", body["error"])
			require.Equal(t, tc.field, body["field"])
			require.NotEmpty(t, body["message"])
			_, cur := e.do(t, http.MethodGet, "", nil)
			require.Equal(t, true, cur["settings"].(map[string]any)["enabled"], "nothing was written")
		})
	}
}

func TestLoginSecurityAllowlistAcceptsATextAndNormalises(t *testing.T) {
	e := newSecEnv(t, nil)
	status, body := e.do(t, http.MethodPatch, "", map[string]any{"ip_allowlist": "192.0.2.5,\n2001:DB8::/48  198.51.100.9"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, []any{"192.0.2.5", "2001:db8::/48", "198.51.100.9"}, body["settings"].(map[string]any)["ip_allowlist"])

	status, body = e.do(t, http.MethodPatch, "", map[string]any{"ip_allowlist": []string{}})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, []any{}, body["settings"].(map[string]any)["ip_allowlist"], "an empty list clears it")
}

func TestLoginSecurityYourAddressAndAllowlist(t *testing.T) {
	e := newSecEnv(t, nil)
	_, body := e.do(t, http.MethodPatch, "", map[string]any{"ip_allowlist": []string{"127.0.0.0/8"}})
	require.Equal(t, true, body["your_ip_allowlisted"])
}

func TestLoginSecurityTrustedProxiesSourceAndEffect(t *testing.T) {
	e := newSecEnv(t, func(c *config.Config) { c.TrustedProxies = "203.0.113.0/24" })
	_, body := e.do(t, http.MethodGet, "", nil)
	require.Equal(t, "env", body["trusted_proxies_source"])
	require.Equal(t, []any{"203.0.113.0/24"}, body["trusted_proxies_effective"])
	require.Equal(t, map[string]any{"auto": false, "loopback": false, "private": false, "link_local": false}, body["trusted_defaults"],
		"a list replaces the default: a class it does not name is off")
	require.Equal(t, false, body["trusted_proxies_auto"].(map[string]any)["in_use"], "a list without `auto` does not use it")
	require.Equal(t, []any{"203.0.113.0/24"}, body["trusted_addresses"])

	// The setting wins over the environment.
	status, body := e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": []string{"10.1.0.0/16", "private"}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, "setting", body["trusted_proxies_source"])
	require.Equal(t, []any{"private", "10.1.0.0/16"}, body["trusted_proxies_effective"], "the words first, then the addresses")
	require.Equal(t, map[string]any{"auto": false, "loopback": false, "private": true, "link_local": false}, body["trusted_defaults"],
		"`private` is the private class alone — loopback and link-local are switches of their own")
	require.Equal(t, []any{"10.1.0.0/16"}, body["trusted_addresses"])
	require.Equal(t, []any{"10.1.0.0/16", "private"}, body["settings"].(map[string]any)["trusted_proxies"])

	// Each class by its own word.
	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "loopback link-local"})
	require.Equal(t, map[string]any{"auto": false, "loopback": true, "private": false, "link_local": true}, body["trusted_defaults"])
	require.Equal(t, []any{}, body["trusted_addresses"])

	// `none` trusts nobody.
	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "none"})
	require.Equal(t, []any{}, body["trusted_proxies_effective"])
	require.Equal(t, map[string]any{"auto": false, "loopback": false, "private": false, "link_local": false}, body["trusted_defaults"])
	require.Equal(t, "setting", body["trusted_proxies_source"])

	// Emptied, it falls back to the environment again.
	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": ""})
	require.Equal(t, "env", body["trusted_proxies_source"])

	// The live source (what the server installs at boot) reads the limiter's
	// memory: a list saved on the page is in force for the very next request —
	// no cache to wait out.
	src := e.guard.TrustedProxySource("203.0.113.0/24")
	require.True(t, src().ContainsString("203.0.113.9"))
	status, _ = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "loopback, 192.0.2.0/24"})
	require.Equal(t, http.StatusOK, status)
	require.True(t, src().ContainsString("192.0.2.9"))
	require.True(t, src().ContainsString("::1"))
	require.False(t, src().ContainsString("203.0.113.9"), "a saved list replaces the environment's")
	require.False(t, src().ContainsString("10.1.2.3"), "the private class was not named")
}

// In a container `auto` is loopback plus the container network minus its
// gateway and filex's own address, and the answer says what it resolved to
// and why.
func TestLoginSecurityAutoInAContainer(t *testing.T) {
	e := newSecEnv(t, nil)
	t.Cleanup(clientip.SetAutoResolver(dockerAuto))
	_, body := e.do(t, http.MethodGet, "", nil)
	require.Equal(t, "auto", body["trusted_proxies_source"])
	require.Equal(t, []any{"loopback", "172.18.0.0/16"}, body["trusted_proxies_effective"], "auto spelled out")
	auto := body["trusted_proxies_auto"].(map[string]any)
	require.Equal(t, true, auto["in_use"])
	require.Equal(t, "container", auto["environment"])
	require.Equal(t, "docker", auto["runtime"])
	require.Equal(t, []any{"172.18.0.0/16"}, auto["networks"])
	require.Equal(t, []any{"172.18.0.1"}, auto["excluded_gateways"])
	require.Equal(t, []any{"172.18.0.5"}, auto["excluded_self"])
	require.Equal(t, []any{map[string]any{
		"name": "eth0", "kind": "veth", "addresses": []any{"172.18.0.5/16"}, "trusted": true, "reason": "container-network",
	}}, auto["interfaces"])

	// `auto` and an address: what the page's "trust it" saves for a proxy on
	// the host, which arrives from the gateway.
	status, body := e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "auto, 172.18.0.1"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, "setting", body["trusted_proxies_source"])
	require.Equal(t, []any{"auto", "172.18.0.1"}, body["settings"].(map[string]any)["trusted_proxies"])
	require.Equal(t, []any{"loopback", "172.18.0.0/16", "172.18.0.1"}, body["trusted_proxies_effective"])
	require.Equal(t, map[string]any{"auto": true, "loopback": false, "private": false, "link_local": false}, body["trusted_defaults"])
	require.Equal(t, []any{"172.18.0.1"}, body["trusted_addresses"])
	require.Equal(t, true, body["trusted_proxies_auto"].(map[string]any)["in_use"])
	src := e.guard.TrustedProxySource("")
	require.True(t, src().ContainsString("172.18.0.1"), "named by hand, the gateway is trusted")
	require.True(t, src().ContainsString("172.18.0.9"))
	require.False(t, src().ContainsString("192.168.1.5"))

	// A list without the word leaves `auto` unused.
	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": []string{"loopback"}})
	require.Equal(t, false, body["trusted_proxies_auto"].(map[string]any)["in_use"])
	require.Equal(t, []any{"loopback"}, body["trusted_proxies_effective"])
	require.False(t, src().ContainsString("172.18.0.9"))
}

// The order of the sources: the setting wins over FILEX_TRUSTED_PROXIES,
// which wins over `auto`; and `auto` is a word the environment takes too.
func TestLoginSecurityTrustedProxiesPrecedence(t *testing.T) {
	e := newSecEnv(t, func(c *config.Config) { c.TrustedProxies = "auto, 203.0.113.0/24" })
	t.Cleanup(clientip.SetAutoResolver(dockerAuto))
	_, body := e.do(t, http.MethodGet, "", nil)
	require.Equal(t, "env", body["trusted_proxies_source"])
	require.Equal(t, []any{"loopback", "172.18.0.0/16", "203.0.113.0/24"}, body["trusted_proxies_effective"])
	require.Equal(t, true, body["trusted_proxies_auto"].(map[string]any)["in_use"])

	restore := clientip.SetSource(e.guard.TrustedProxySource("auto, 203.0.113.0/24"))
	defer func() { restore() }()
	fwd := func(peer string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr, r.Header = peer, http.Header{"X-Forwarded-For": {"198.51.100.7"}}
		return clientip.FromRequest(r)
	}
	require.Equal(t, "198.51.100.7", fwd("203.0.113.9:1"), "the environment's address")
	require.Equal(t, "198.51.100.7", fwd("172.18.0.9:1"), "the environment's auto")
	require.Equal(t, "172.18.0.1", fwd("172.18.0.1:1"), "never the gateway")

	status, body := e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "192.0.2.0/24"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.Equal(t, "setting", body["trusted_proxies_source"])
	require.Equal(t, "198.51.100.7", fwd("192.0.2.9:1"), "the setting wins")
	require.Equal(t, "203.0.113.9", fwd("203.0.113.9:1"), "the environment's list is not in force any more")
	require.Equal(t, "172.18.0.9", fwd("172.18.0.9:1"), "nor its auto")

	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": ""})
	require.Equal(t, "env", body["trusted_proxies_source"], "the saved list removed: the environment again")

	// Neither: auto.
	restore()
	restore = clientip.SetSource(e.guard.TrustedProxySource(""))
	require.Equal(t, "198.51.100.7", fwd("172.18.0.9:1"))
	require.Equal(t, "172.18.0.1", fwd("172.18.0.1:1"))
	require.Equal(t, "192.168.1.20", fwd("192.168.1.20:1"), "the LAN is not trusted by default")
}

// A peer that sends X-Forwarded-For without being trusted is named in the
// answer (seen through the router's access-log middleware, once per
// request), and leaves the list once it is trusted.
func TestLoginSecurityNamesAnUntrustedForwarder(t *testing.T) {
	e := newSecEnv(t, nil)
	// What the server installs at boot: the list the page saves is in force.
	t.Cleanup(clientip.SetSource(e.guard.TrustedProxySource("")))
	status, _ := e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": "none"})
	require.Equal(t, http.StatusOK, status)
	e.wrong(t, "198.51.100.7", "x@example.com", 2)

	_, body := e.do(t, http.MethodGet, "", nil)
	list := body["untrusted_forwarders"].([]any)
	require.Len(t, list, 1)
	f := list[0].(map[string]any)
	require.Equal(t, "127.0.0.1", f["address"], "the test client is the peer; what it wrote is not kept")
	require.EqualValues(t, 2, f["count"], "two requests, one count each")
	require.Equal(t, false, f["public"])
	require.Equal(t, false, f["relay"])
	require.NotEmpty(t, f["first_seen"])
	require.NotEmpty(t, f["last_seen"])
	require.EqualValues(t, 1, body["untrusted_forwarders_total"])

	_, body = e.do(t, http.MethodPatch, "", map[string]any{"trusted_proxies": []string{"auto"}})
	require.Equal(t, []any{}, body["untrusted_forwarders"], "trusted now: off the list")
	require.EqualValues(t, 0, body["untrusted_forwarders_total"])
}

// A `login.*` key written through the generic settings API (the Settings page,
// the admin tools) reaches the running limiter as well: it holds its settings
// in memory, and that write makes it read them again.
func TestLoginSecuritySettingWrittenElsewhereReachesTheLimiter(t *testing.T) {
	e := newSecEnv(t, nil)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	status, body := doJSON(t, e.admin, http.MethodPut, e.srv.URL+"/api/admin/settings/"+loginguard.KeyAccountMax, map[string]any{"value": "2"})
	require.Equal(t, http.StatusOK, status, "%v", body)
	require.EqualValues(t, 2, e.guard.Config(context.Background()).AccountMax)
	e.wrong(t, "203.0.113.1", "ada@example.com", 2)
	_, locks := e.do(t, http.MethodGet, "/locks?locked=1", nil)
	require.Len(t, locks["items"].([]any), 1, "two misses lock at once")
}

func TestLoginSecurityLocksAndUnlock(t *testing.T) {
	e := newSecEnv(t, nil)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	e.wrong(t, "203.0.113.1", "Ada@Example.com", 5)
	e.wrong(t, "198.51.100.7", "user1@example.com", 3)

	_, all := e.do(t, http.MethodGet, "/locks", nil)
	require.GreaterOrEqual(t, len(all["items"].([]any)), 3, "the account, and both addresses, are being counted")

	_, locked := e.do(t, http.MethodGet, "/locks?locked=1", nil)
	items := locked["items"].([]any)
	require.Len(t, items, 1)
	it := items[0].(map[string]any)
	require.Equal(t, "account", it["scope"])
	require.Equal(t, "ada@example.com", it["subject"], "the identifier is shown normalised — the counter's own key")
	require.Equal(t, true, it["locked"])
	require.EqualValues(t, 5, it["limit"])
	require.EqualValues(t, 1, it["lock_level"])
	require.Equal(t, "203.0.113.1", it["last_ip"])
	require.Equal(t, "web", it["last_protocol"])
	require.NotEmpty(t, it["locked_until"])
	require.Greater(t, it["retry_after"].(float64), 0.0)

	_, ipOnly := e.do(t, http.MethodGet, "/locks?scope=ip", nil)
	for _, i := range ipOnly["items"].([]any) {
		require.Equal(t, "ip", i.(map[string]any)["scope"])
	}
	status, _ := e.do(t, http.MethodGet, "/locks?scope=banana", nil)
	require.Equal(t, http.StatusBadRequest, status)

	// The locked person can sign in after an administrator unlocks — typing the
	// identifier the way the person did is enough.
	require.Equal(t, http.StatusTooManyRequests, e.loginAs(t, "ada@example.com", "RightPass!1"))
	status, out := e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "account", "subject": " ADA@example.com "})
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 1, out["unlocked"])
	require.Equal(t, http.StatusOK, e.loginAs(t, "ada@example.com", "RightPass!1"))

	_, out = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "account", "subject": "ada@example.com"})
	require.EqualValues(t, 0, out["unlocked"], "nothing left to unlock")

	// Refusals.
	status, _ = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "ip", "subject": "not-an-ip"})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "ip", "subject": "10.0.0.0/8"})
	require.Equal(t, http.StatusBadRequest, status, "one address, not a network")
	status, _ = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "account", "subject": " "})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "planet", "subject": "x"})
	require.Equal(t, http.StatusBadRequest, status)
}

func (e *secEnv) loginAs(t *testing.T, identifier, password string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/auth/login",
		strings.NewReader(`{"email":"`+identifier+`","password":"`+password+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.200")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestLoginSecurityUnlockAllAndAnAddress(t *testing.T) {
	e := newSecEnv(t, nil)
	e.wrong(t, "198.51.100.7", "a@example.com", 5)
	for i := 0; i < 5; i++ {
		e.wrong(t, "198.51.100.7", "b"+strconv.Itoa(i)+"@example.com", 1)
	}
	_, locked := e.do(t, http.MethodGet, "/locks?locked=1", nil)
	require.GreaterOrEqual(t, len(locked["items"].([]any)), 2, "the account and the address")

	// One address, by address.
	status, out := e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "ip", "subject": "198.51.100.7"})
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 1, out["unlocked"])

	status, out = e.do(t, http.MethodPost, "/unlock", map[string]any{"all": true})
	require.Equal(t, http.StatusOK, status)
	_, locked = e.do(t, http.MethodGet, "/locks?locked=1", nil)
	require.Empty(t, locked["items"])
}

func TestLoginSecurityAttemptsAndAuditRows(t *testing.T) {
	e := newSecEnv(t, nil)
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	e.wrong(t, "203.0.113.1", "ada@example.com", 5)

	status, out := e.do(t, http.MethodPatch, "", map[string]any{"account_max_fails": 6})
	require.Equal(t, http.StatusOK, status)
	_ = out
	status, _ = e.do(t, http.MethodPost, "/unlock", map[string]any{"scope": "account", "subject": "ada@example.com"})
	require.Equal(t, http.StatusOK, status)

	_, att := e.do(t, http.MethodGet, "/attempts?limit=100", nil)
	items := att["items"].([]any)
	actions := map[string]int{}
	for _, i := range items {
		actions[i.(map[string]any)["action"].(string)]++
	}
	require.Equal(t, 5, actions["login.failed"])
	require.Equal(t, 1, actions["login.locked"])
	require.Equal(t, 1, actions["login.unlocked"], "the administrator's unlock is in the same trail")

	_, only := e.do(t, http.MethodGet, "/attempts?action=login.failed", nil)
	require.EqualValues(t, 5, only["total"])
	first := only["items"].([]any)[0].(map[string]any)
	require.Equal(t, "ada@example.com", first["identifier"])
	require.Equal(t, "203.0.113.1", first["ip"])
	require.Equal(t, "web", first["protocol"])
	require.Equal(t, "invalid_credentials", first["reason"])

	status, _ = e.do(t, http.MethodGet, "/attempts?action=user.create", nil)
	require.Equal(t, http.StatusBadRequest, status, "only the sign-in trail is served here")

	// The change and the unlock each left their own row in the audit log,
	// carrying what changed and by whom — no secret among it.
	rows, err := e.store.ListAuditRecent(context.Background(), 500)
	require.NoError(t, err)
	var update, unlock bool
	for _, r := range rows {
		switch r.Action {
		case "login_security.update":
			update = true
			require.NotNil(t, r.UserID, "the administrator is named")
			require.Equal(t, []any{"account_max_fails"}, r.Metadata["changed_fields"])
		case "login.unlocked":
			if r.Metadata["reason"] == "admin" {
				unlock = true
				require.NotNil(t, r.UserID)
				require.Equal(t, "account", r.Metadata["scope"])
				require.Equal(t, "ada@example.com", r.Metadata["subject"])
			}
		}
	}
	require.True(t, update, "the settings change is audited")
	require.True(t, unlock, "the administrator's unlock is audited")
}

func TestLoginSecurityExpiredLockIsAuditedWhenListed(t *testing.T) {
	e := newSecEnv(t, nil)
	clock := time.Now().UTC()
	e.guard.Now = func() time.Time { return clock }
	seedUser(t, e.store, "ada@example.com", "RightPass!1")
	e.wrong(t, "203.0.113.1", "ada@example.com", 5)
	clock = clock.Add(2 * time.Minute)
	// The page lists the locks; the release that already happened is written down.
	e.do(t, http.MethodGet, "/locks", nil)
	_, att := e.do(t, http.MethodGet, "/attempts?action=login.unlocked", nil)
	items := att["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, "expired", items[0].(map[string]any)["reason"])
}

func TestLoginSecurityIsAdminOnly(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	testutil.SeedRegularUser(t, store, "user@test.local", "UserPass!1")
	testutil.LoginAs(t, srv, client, "user@test.local", "UserPass!1")
	for _, rt := range []struct{ m, p string }{
		{http.MethodGet, "/api/admin/login-security"},
		{http.MethodPatch, "/api/admin/login-security"},
		{http.MethodGet, "/api/admin/login-security/locks"},
		{http.MethodPost, "/api/admin/login-security/unlock"},
		{http.MethodGet, "/api/admin/login-security/attempts"},
	} {
		status, _ := doJSON(t, client, rt.m, srv.URL+rt.p, map[string]any{})
		require.Equal(t, http.StatusForbidden, status, "%s %s", rt.m, rt.p)
	}
}

func TestLoginSecurityOverTheAIAdminSurface(t *testing.T) {
	srv, client, store, uid := adminFixture(t)
	tok := issueToken(t, store, uid, "mcp,admin", nil)
	t.Cleanup(clientip.SetSource(nil))
	t.Cleanup(clientip.SetAutoResolver(dockerAuto))

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/ai/admin/login-security", nil)
	req.Header.Set("X-Filex-Token", tok)
	resp, err := client.Do(req)
	require.NoError(t, err)
	var got map[string]any
	testutil.ReadJSON(t, resp, &got)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.EqualValues(t, 5, got["settings"].(map[string]any)["account_max_fails"])
	require.Equal(t, "auto", got["trusted_proxies_source"], "the token route serves the same answer")
	require.Equal(t, "container", got["trusted_proxies_auto"].(map[string]any)["environment"])
	require.Contains(t, got, "untrusted_forwarders")

	// ...and so does the MCP tool.
	code, body := mcpPost(t, client, srv.URL+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"admin_login_security_get","arguments":{}}}`)
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `trusted_proxies_auto`)
	require.Contains(t, body, `excluded_gateways`)
	require.Contains(t, body, `untrusted_forwarders`)

	// An MCP tool drives the same handler.
	code, body = mcpPost(t, client, srv.URL+"/api/ai/mcp", tok,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_login_security_update","arguments":{"body":{"ip_max_fails":20}}}}`)
	require.Equal(t, http.StatusOK, code, body)
	require.NotContains(t, body, `"isError":true`, body)
	v, err := store.GetSetting(context.Background(), loginguard.KeyIPMax)
	require.NoError(t, err)
	require.Equal(t, "20", v)

	code, body = mcpPost(t, client, srv.URL+"/api/ai/mcp", tok, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	require.Equal(t, http.StatusOK, code)
	for _, n := range []string{"admin_login_security_get", "admin_login_security_update", "admin_login_security_locks", "admin_login_security_unlock", "admin_login_security_attempts"} {
		require.Contains(t, body, n)
	}
}

package clientip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func req(remote string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func TestUntrustedPeerCannotSpoofItsAddress(t *testing.T) {
	// A direct client outside every trusted range sends its own X-Forwarded-For.
	// The header is what the attacker wrote; the socket is what is true.
	r := req("203.0.113.9:5555", map[string]string{"X-Forwarded-For": "198.51.100.1", "X-Real-IP": "198.51.100.2"})
	require.Equal(t, "203.0.113.9", FromRequest(r))
}

// privateTrusted puts the private and loopback classes in force, for the
// tests about how a chain is read (not about which peer is trusted).
func privateTrusted(t *testing.T) {
	t.Helper()
	set, err := ParseList("loopback, private")
	require.NoError(t, err)
	t.Cleanup(SetSource(func() *Set { return set }))
}

func TestTrustedPeerHandsOverTheClient(t *testing.T) {
	// The default deployment: Caddy in a container on filex's network (auto).
	t.Cleanup(SetSource(nil))
	t.Cleanup(SetAutoResolver(func() AutoResolution {
		return AutoResolution{Environment: EnvContainer, Networks: []string{"172.18.0.0/16"}, ExcludedGateways: []string{"172.18.0.1"}}
	}))
	r := req("172.18.0.2:41000", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Equal(t, "198.51.100.7", FromRequest(r))
	r = req("127.0.0.1:41000", map[string]string{"X-Real-IP": "198.51.100.8"})
	require.Equal(t, "198.51.100.8", FromRequest(r))
	r = req("[::1]:41000", map[string]string{"X-Forwarded-For": "2001:db8::5"})
	require.Equal(t, "2001:db8::5", FromRequest(r))
}

func TestForwardedChainIsReadFromTheTrustedEnd(t *testing.T) {
	privateTrusted(t)
	// A client-supplied first hop must not win: the proxy APPENDS the address
	// it saw, so the last untrusted entry is the one a trusted proxy vouches for.
	r := req("10.0.0.5:1", map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.7, 10.0.0.9"})
	require.Equal(t, "198.51.100.7", FromRequest(r))
	// Repeated header lines are one list.
	r = req("10.0.0.5:1", nil)
	r.Header.Add("X-Forwarded-For", "1.2.3.4")
	r.Header.Add("X-Forwarded-For", "198.51.100.7")
	require.Equal(t, "198.51.100.7", FromRequest(r))
}

func TestGarbageInTheChainFallsBackToThePeer(t *testing.T) {
	privateTrusted(t)
	r := req("10.0.0.5:1", map[string]string{"X-Forwarded-For": "198.51.100.7, not-an-ip"})
	require.Equal(t, "10.0.0.5", FromRequest(r))
}

func TestPortsAndMappedV4AreNormalised(t *testing.T) {
	privateTrusted(t)
	r := req("10.0.0.5:1", map[string]string{"X-Forwarded-For": "198.51.100.7:4433"})
	require.Equal(t, "198.51.100.7", FromRequest(r))
	r = req("[::ffff:203.0.113.9]:80", nil)
	require.Equal(t, "203.0.113.9", FromRequest(r))
}

func TestConfiguredListReplacesTheDefault(t *testing.T) {
	set, err := ParseList("203.0.113.0/24")
	require.NoError(t, err)
	restore := SetSource(func() *Set { return set })
	t.Cleanup(restore)

	// The old default (a private address) is no longer trusted...
	r := req("10.0.0.5:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Equal(t, "10.0.0.5", FromRequest(r))
	// ...and the listed one is.
	r = req("203.0.113.77:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	require.Equal(t, "198.51.100.7", FromRequest(r))
}

func TestParseList(t *testing.T) {
	s, err := ParseList("10.0.0.0/8, 192.0.2.1\n2001:db8::/32 loopback private")
	require.NoError(t, err)
	for _, ok := range []string{"10.9.9.9", "192.0.2.1", "2001:db8::1", "127.0.0.1", "172.20.1.1"} {
		require.True(t, s.ContainsString(ok), ok)
	}
	require.False(t, s.ContainsString("8.8.8.8"))
	require.False(t, s.ContainsString("169.254.10.1"), "link-local was not named, so it is not in")

	none, err := ParseList("none")
	require.NoError(t, err)
	require.False(t, none.ContainsString("127.0.0.1"), "`none` trusts nobody, not even loopback")

	_, err = ParseList("10.0.0.0/8, banana")
	require.Error(t, err)
	require.Contains(t, err.Error(), "banana", "the error names the entry a person has to fix")
	_, err = ParseList("10.0.0.0/99")
	require.Error(t, err)

	empty, err := ParseList("  ")
	require.NoError(t, err)
	require.Nil(t, empty, "blank means not set")
}

// The class words are the standard library's own definition of loopback,
// private and link-local — not a list of ranges kept in this package. Every
// address below is answered exactly as net/netip answers it, the edges of each
// range included, and an IPv4-mapped IPv6 address as the IPv4 address it carries.
// (Until 0.50 these three were the default; the default is `auto` now.)
func TestTheClassWordsAreTheStandardLibrarysDefinition(t *testing.T) {
	all, err := ParseList("loopback, private, link-local")
	require.NoError(t, err)
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true}, {"127.9.9.9", true}, {"::1", true},
		{"10.0.0.1", true}, {"10.255.255.254", true},
		{"172.15.255.255", false}, {"172.16.0.1", true}, {"172.31.255.255", true}, {"172.32.0.1", false},
		{"192.168.1.199", true}, {"192.169.0.1", false},
		{"169.254.10.1", true}, {"fe80::1", true},
		{"fc00::1", true}, {"fd12:3456::1", true}, {"fe00::1", false},
		{"::ffff:10.1.2.3", true}, {"::ffff:198.51.100.7", false},
		{"100.64.0.1", false}, // shared address space (RFC 6598) is not "private" to Go
		{"198.51.100.7", false}, {"8.8.8.8", false}, {"2606:4700:4700::1111", false},
	} {
		a := netip.MustParseAddr(tc.ip)
		u := a.Unmap()
		std := u.IsLoopback() || u.IsPrivate() || u.IsLinkLocalUnicast()
		require.Equal(t, std, tc.want, "%s: the table disagrees with net/netip", tc.ip)
		require.Equal(t, tc.want, all.Contains(a), tc.ip)
		require.Equal(t, tc.want, all.ContainsString(tc.ip), tc.ip)
	}
	require.Equal(t, Classes{Loopback: true, Private: true, LinkLocal: true}, all.Classes())
	require.Equal(t, []string{"loopback", "private", "link-local"}, all.Strings())
	require.Empty(t, all.Addresses(), "the words name no range of their own")
}

// Unset, the list is `auto`: on a plain install that is loopback alone - the
// LAN, the private ranges and link-local are not believed any more.
func TestTheDefaultIsAuto(t *testing.T) {
	t.Cleanup(SetSource(nil))
	t.Cleanup(SetAutoResolver(func() AutoResolution { return AutoResolution{Environment: EnvPlain} }))
	def := DefaultSet()
	require.Equal(t, Classes{Auto: true}, def.Classes())
	require.Equal(t, []string{"auto"}, def.Strings())
	require.Equal(t, []string{"loopback"}, def.Effective())
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "::ffff:127.0.0.1": true,
		"10.0.0.1": false, "172.18.0.2": false, "192.168.1.199": false, "169.254.10.1": false,
		"fe80::1": false, "fd12:3456::1": false, "198.51.100.7": false,
	} {
		require.Equal(t, want, def.ContainsString(ip), ip)
	}
	fwd := map[string]string{"X-Forwarded-For": "198.51.100.7"}
	require.Equal(t, "192.168.1.20", FromRequest(req("192.168.1.20:5000", fwd)), "a LAN machine is not a proxy until it is named")
	require.Equal(t, "198.51.100.7", FromRequest(req("127.0.0.1:5000", fwd)), "a proxy on the same machine is")
}

// Each word switches its own class and nothing else, so an operator can keep
// loopback and drop the rest.
func TestEachWordIsOneClass(t *testing.T) {
	probe := map[string]string{"loopback": "127.0.0.1", "private": "10.1.2.3", "link-local": "fe80::9"}
	for word := range probe {
		s, err := ParseList(word)
		require.NoError(t, err)
		for other, ip := range probe {
			require.Equal(t, other == word, s.ContainsString(ip), "%q and %s", word, ip)
		}
	}
	s, err := ParseList("Link_Local, LOOPBACK")
	require.NoError(t, err, "the words are read in any case, link_local as link-local")
	require.Equal(t, Classes{Loopback: true, LinkLocal: true}, s.Classes())

	// Spelled back words first, in a fixed order, then the addresses.
	s, err = ParseList("203.0.113.0/24 link-local 192.0.2.7 loopback")
	require.NoError(t, err)
	require.Equal(t, []string{"loopback", "link-local", "203.0.113.0/24", "192.0.2.7"}, s.Strings())
	require.Equal(t, []string{"203.0.113.0/24", "192.0.2.7"}, s.Addresses())

	// `none` next to other entries takes nothing away.
	s, err = ParseList("none, loopback")
	require.NoError(t, err)
	require.True(t, s.ContainsString("::1"))
	require.False(t, s.ContainsString("10.1.2.3"))
}

// An allow-list of client addresses takes no word: "every private address"
// on an allow-list is a hole, so it must be written out.
func TestParseEntriesRefusesTheWords(t *testing.T) {
	for _, w := range []string{"auto", "private", "loopback", "link-local", "none"} {
		_, err := ParseEntries([]string{w})
		require.Error(t, err, w)
	}
}

func TestContext(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, "", FromContext(ctx))
	require.Equal(t, "198.51.100.7", FromContext(WithIP(ctx, "198.51.100.7")))
}

func TestPeerOf(t *testing.T) {
	require.Equal(t, "203.0.113.9", PeerOf("203.0.113.9:22"))
	require.Equal(t, "2001:db8::1", PeerOf("[2001:db8::1]:22"))
	require.Equal(t, "203.0.113.9", PeerOf("203.0.113.9"))
}

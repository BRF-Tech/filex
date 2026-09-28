package netguard

import (
	"context"
	"errors"
	"net"
	"testing"
)

// Refused is the one answer to "may filex dial this address on behalf of a
// URL somebody else chose" (an app's http_request / asset_fetch, a plugin
// download). Every range below is one no outside URL may reach.
//
// ⚠⚠ 100.64.0.0/10 is the reason this test exists: it is not RFC 1918, so
// net.IP.IsPrivate says "public", yet it is exactly where overlay meshes put
// their nodes — Cloudflare WARP (100.96/12), Tailscale (100.64.0.0/10),
// carrier-grade NAT.
func TestRefused_NonPublicRanges(t *testing.T) {
	refused := []string{
		// the classic set
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.199", "169.254.169.254",
		"::1", "fe80::1", "fc00::1", "fd12:3456::1", "224.0.0.1", "0.0.0.0", "::",
		// shared address space: CGNAT, WARP (100.96/12), Tailscale
		"100.64.0.1", "100.100.0.2", "100.110.0.6", "100.127.255.254",
		// "this network", IETF protocol assignments, benchmarking, reserved,
		// broadcast, the deprecated 6to4 relay anycast
		"0.1.2.3", "192.0.0.9", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.255", "192.88.99.1",
		// IPv4-mapped and IPv4-compatible spellings of a private address
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "::10.0.0.1",
		// NAT64 (well-known and local-use prefixes) and 6to4 wrapping a
		// private or loopback IPv4 address
		"64:ff9b::a00:1", "64:ff9b::7f00:1", "64:ff9b:1::a00:1", "2002:a00:1::1", "2002:7f00:1::",
		// Teredo tunnels an IPv4 endpoint nobody can vet
		"2001:0:4136:e378:8000:63bf:3fff:fdd2",
		// discard-only
		"100::1",
	}
	for _, s := range refused {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test data: %q does not parse", s)
		}
		if !Refused(ip) {
			t.Errorf("Refused(%s) = false, want true", s)
		}
	}
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "140.82.112.3", "100.63.255.255", "100.128.0.1",
		"2606:4700:4700::1111", "2a00:1450:4001:80b::200e",
		// NAT64 and 6to4 of a PUBLIC address stay reachable
		"64:ff9b::808:808", "2002:808:808::1",
	}
	for _, s := range allowed {
		if Refused(net.ParseIP(s)) {
			t.Errorf("Refused(%s) = true, want false", s)
		}
	}
	if !Refused(nil) {
		t.Error("a nil IP must be refused")
	}
}

// The dialer refuses the RESOLVED address, not the spelling: a literal in the
// shared address space is refused at dial time too.
func TestDialContext_RefusesSharedAddressSpace(t *testing.T) {
	dial := DialContext(nil)
	_, err := dial(context.Background(), "tcp", "100.100.0.2:443")
	if !errors.Is(err, ErrPrivateTarget) {
		t.Fatalf("dial 100.100.0.2: err = %v, want ErrPrivateTarget", err)
	}
}

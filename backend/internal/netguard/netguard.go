// Package netguard is the one answer to "may filex dial this address?".
//
// Two callers ask it, for the same reason: a URL an administrator or a plugin
// hands us must not become a probe of the server's own network. An install
// URL of http://169.254.169.254/… or http://10.0.0.5:9200/… is the SSRF
// shape, and so is a plugin's http_request to a public name that resolves to
// a private address. The check therefore runs on the DIALLED address, after
// DNS, and on every redirect hop — never on the string.
//
// It lives in its own package so the storage-plugin downloader
// (internal/plugin) and the app-plugin outbound transport
// (internal/wasmplugin) cannot drift apart: one list of refused ranges, one
// guarded dialer, one error.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrPrivateTarget is what a guarded dial answers for a refused address.
// Callers match it with errors.Is to turn it into their own wording.
var ErrPrivateTarget = errors.New("resolves to a private or local address")

// Refused reports whether a resolved address is one an outbound request may
// never reach: loopback, link-local (v4 169.254/16 and v6 fe80::/10, which is
// where cloud metadata lives), the RFC 1918 / ULA private ranges, multicast
// and the unspecified address.
//
// A nil IP counts as refused: "we could not tell" is not permission.
//
// ⚠⚠ Not only RFC 1918. net.IP.IsPrivate calls the shared address space
// 100.64.0.0/10 public, and that is where overlay meshes live (Cloudflare
// WARP 100.96/12, Tailscale, carrier-grade NAT). The other ranges below
// are not routable destinations either, and the IPv6 spellings that wrap an
// IPv4 address (NAT64, 6to4) are judged by the address they wrap.
func Refused(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if refusedBasic(ip) || inRefusedNets(ip) {
		return true
	}
	if v4 := embeddedIPv4(ip); v4 != nil {
		return refusedBasic(v4) || inRefusedNets(v4)
	}
	return false
}

func refusedBasic(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast()
}

func inRefusedNets(ip net.IP) bool {
	for _, n := range refusedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// refusedNets are the non-public ranges net.IP's own predicates do not cover.
var refusedNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",      // "this network"
		"100.64.0.0/10",  // shared address space: CGNAT, WARP, Tailscale
		"192.0.0.0/24",   // IETF protocol assignments
		"192.88.99.0/24", // deprecated 6to4 relay anycast
		"198.18.0.0/15",  // benchmarking
		"240.0.0.0/4",    // reserved, and 255.255.255.255
		"::/96",          // IPv4-compatible (deprecated); :: and ::1 fall in it too
		"100::/64",       // discard-only
		"2001::/32",      // Teredo: tunnels to an IPv4 endpoint nobody can vet
		"2001:db8::/32",  // documentation
		"64:ff9b:1::/48", // local-use NAT64: the site's own translator
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("netguard: " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}()

// embeddedIPv4 is the IPv4 address an IPv6 address stands for: the last four
// bytes of a well-known-prefix NAT64 address (64:ff9b::/96, RFC 6052), or the
// 6to4 gateway of a 2002::/16 address (RFC 3056). nil for anything else.
func embeddedIPv4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil
	}
	ip16 := ip.To16()
	if ip16 == nil {
		return nil
	}
	if ip16[0] == 0x00 && ip16[1] == 0x64 && ip16[2] == 0xff && ip16[3] == 0x9b && isZero(ip16[4:12]) {
		return net.IPv4(ip16[12], ip16[13], ip16[14], ip16[15])
	}
	if ip16[0] == 0x20 && ip16[1] == 0x02 {
		return net.IPv4(ip16[2], ip16[3], ip16[4], ip16[5])
	}
	return nil
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// Private says whether an address is inside the private network — loopback,
// link-local, RFC 1918 or ULA. This is the WEAKER question, asked about a
// destination filex will send credentials to: plain http:// is tolerable
// there and nowhere else. It deliberately does not include multicast or the
// unspecified address, which are not destinations at all.
func Private(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate())
}

// DialContext is a net.Dialer's DialContext wrapped in the Refused check: the
// name is resolved here, every answer is checked, and the connection is made
// to the address that passed — so nothing can re-resolve to something else
// between the check and the dial.
func DialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 10 * time.Second}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if Refused(ip.IP) {
				return nil, fmt.Errorf("%s %w", host, ErrPrivateTarget)
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// Transport returns an http.Transport that dials through the guard. Callers
// set their own timeouts on the fields they care about; proxies are off,
// because a proxy would do the resolving and the guard would never see it.
func Transport(responseHeaderTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            DialContext(&net.Dialer{Timeout: 10 * time.Second}),
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
}

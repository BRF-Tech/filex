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
func Refused(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast()
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

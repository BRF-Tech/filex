package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/brf-tech/filex/backend/internal/netguard"
)

// Two network rules, deliberately NOT the same rule, because they answer
// different questions:
//
//   - Installing FROM A URL is filex fetching a file the admin named. That
//     fetch must not be turned into a probe of the server's own network: a
//     plugin URL of http://169.254.169.254/… or http://10.0.0.5:9200/… is the
//     SSRF shape, so a download may reach only PUBLIC addresses — checked on
//     the DIALLED address (after DNS, so a public name that resolves privately
//     is refused too) and on every redirect hop, which goes through the same
//     dialer.
//   - A REMOTE plugin's address is where filex will send a bearer token and
//     every storage credential, for as long as the row exists. That traffic
//     must be TLS unless it never leaves the private network: plain http:// is
//     accepted only for loopback, link-local, RFC 1918 and ULA targets.
//
// The address rules themselves live in internal/netguard, shared with the
// app-plugin downloads and the app-plugin outbound transport: one list of
// refused ranges, one guarded dialer, one download client
// (netguard.Policy.DownloadClient: every hop guarded, at most five, never
// from https to plain http), one error.

// errPrivateTarget is what the download dialer answers for a refused address.
var errPrivateTarget = fmt.Errorf("%w; a plugin download must come from a public host", netguard.ErrPrivateTarget)

// checkDownloadURL is the cheap, pre-dial half of the download guard: the
// scheme always, and - when guard is set - a literal IP the policy refuses.
// The dialer does the rest (names, redirects). A nil guard is an embedder
// that supplied its own client.
func checkDownloadURL(rawURL string, guard *netguard.Policy) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, reject("url must be http(s)://host/…")
	}
	if guard != nil {
		if ip := net.ParseIP(u.Hostname()); ip != nil && guard.Refused(ip) {
			return nil, reject("%s %v", u.Hostname(), errPrivateTarget)
		}
	}
	return u, nil
}

// errRemoteNeedsTLS is the refusal for a plain-http remote plugin outside the
// private network.
var errRemoteNeedsTLS = errors.New("remote plugins outside the private network must use https://")

// privateOnlyDial is how a plugin spoken to over plain http:// is dialled -
// a remote registered with http://, a launched plugin on its loopback port.
// The name is resolved at EVERY dial, every answer must be private
// (netguard.Private), and the connection is made to the address that
// passed. checkRemoteAddress asks once, at registration and at start; without
// this a name that resolves elsewhere later (DNS rebinding) carried the bearer
// token and every storage credential there in the clear.
func privateOnlyDial(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%s does not resolve: %w", host, errRemoteNeedsTLS)
		}
		for _, ip := range ips {
			if !netguard.Private(ip.IP) {
				return nil, fmt.Errorf("%s resolves to %s: %w", host, ip.IP, errRemoteNeedsTLS)
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// checkRemoteAddress applies the remote-plugin rule to a registered address:
// https:// goes anywhere; http:// only where every address the host resolves
// to is private, loopback or link-local. A name that does not resolve is
// refused for http:// — filex cannot tell where the token would go.
func checkRemoteAddress(ctx context.Context, address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return fmt.Errorf("a remote plugin address must be an http(s):// URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
	default:
		return fmt.Errorf("a remote plugin address must be an http(s):// URL")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if netguard.Private(ip) {
			return nil
		}
		return reject("%s: %w", host, errRemoteNeedsTLS)
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(rctx, host)
	if err != nil || len(ips) == 0 {
		return reject("%s does not resolve, so plain http:// cannot be allowed: %w", host, errRemoteNeedsTLS)
	}
	for _, ip := range ips {
		if !netguard.Private(ip.IP) {
			return reject("%s resolves to %s: %w", host, ip.IP, errRemoteNeedsTLS)
		}
	}
	return nil
}

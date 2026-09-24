package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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
// app-plugin outbound transport: one list of refused ranges, one guarded
// dialer, one error.

const downloadMaxRedirects = 5

// errPrivateTarget is what the download dialer answers for a refused address.
var errPrivateTarget = fmt.Errorf("%w; a plugin download must come from a public host", netguard.ErrPrivateTarget)

// newDownloadClient is the client InstallFromURL uses when the embedder did
// not supply one: every dial resolves the name and refuses a private or local
// result, every redirect hop dials through the same guard, and the chain is
// capped.
func newDownloadClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: netguard.Transport(60 * time.Second),
		Timeout:   timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= downloadMaxRedirects {
				return errors.New("too many redirects")
			}
			if next.URL.Scheme != "https" && next.URL.Scheme != "http" {
				return fmt.Errorf("redirect to %s:// is not followed", next.URL.Scheme)
			}
			if ip := net.ParseIP(next.URL.Hostname()); ip != nil && netguard.Refused(ip) {
				return fmt.Errorf("redirect to %s refused: %w", next.URL.Hostname(), errPrivateTarget)
			}
			return nil
		},
	}
}

// checkDownloadURL is the cheap, pre-dial half of the download guard: the
// scheme always, and — when guard is on — a literal IP that is private. The
// dialer does the rest (names, redirects).
func checkDownloadURL(rawURL string, guard bool) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, reject("url must be http(s)://host/…")
	}
	if guard {
		if ip := net.ParseIP(u.Hostname()); ip != nil && netguard.Refused(ip) {
			return nil, reject("%s %v", u.Hostname(), errPrivateTarget)
		}
	}
	return u, nil
}

// errRemoteNeedsTLS is the refusal for a plain-http remote plugin outside the
// private network.
var errRemoteNeedsTLS = errors.New("remote plugins outside the private network must use https://")

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

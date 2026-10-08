package webpush

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultHosts are the push services of the browsers filex supports. An
// endpoint's host is accepted when it is one of them or below one of them.
var DefaultHosts = []string{
	// Chrome and every browser built on it (Opera, Brave, Samsung Internet,
	// Vivaldi), on every platform.
	"fcm.googleapis.com",
	"android.googleapis.com",
	// Firefox.
	"updates.push.services.mozilla.com",
	"push.services.mozilla.com",
	// Safari on macOS, and the web app added to an iPhone's or an iPad's Home
	// Screen (iOS / iPadOS 16.4 and later).
	"web.push.apple.com",
	// Edge (Windows Push Notification Services: wns2-<region>.notify.windows.com).
	"notify.windows.com",
}

// Policy decides which endpoints a push may be sent to.
type Policy struct {
	// Hosts are the push services accepted. Empty: DefaultHosts.
	Hosts []string
	// AnyHost accepts any https host that is a name, not an address
	// (FILEX_PUSH_HOSTS=*): a browser whose push service the list does not
	// know. SafeClient still refuses one that resolves into a private network.
	AnyHost bool
	// Insecure accepts http:// and any host and port - tests only, where the
	// push service is an httptest server on this machine.
	Insecure bool
}

// Check answers nil for an endpoint a push may go to, an ErrEndpoint saying
// why otherwise: an https address of a known push service, on the default
// port, with no credentials in it.
func (p Policy) Check(endpoint string) error {
	refuse := func(why string) error { return fmt.Errorf("%w: %s", ErrEndpoint, why) }
	if endpoint == "" || len(endpoint) > MaxEndpoint || strings.ContainsAny(endpoint, " \t\r\n") {
		return refuse("not an address")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return refuse("not an absolute address")
	}
	if u.User != nil {
		return refuse("an address with credentials in it")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.Insecure {
			return refuse("not https")
		}
	default:
		return refuse("not https")
	}
	if p.Insecure {
		return nil
	}
	if port := u.Port(); port != "" && port != "443" {
		return refuse("not the https port")
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return refuse("an address, not a push service")
	}
	if p.AnyHost {
		return nil
	}
	hosts := p.Hosts
	if len(hosts) == 0 {
		hosts = DefaultHosts
	}
	for _, h := range hosts {
		h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "."))
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return nil
		}
	}
	return refuse("not a push service filex knows (" + host + ")")
}

// cgnat is the shared address space (RFC 6598): carrier NAT, and the private
// mesh of more than one VPN product. Not public, whatever net/netip says.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// PublicAddr reports whether ip is an address on the public internet: not
// this machine, not a private, link-local, shared or multicast network.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsInterfaceLocalMulticast() &&
		!ip.IsMulticast() && !cgnat.Contains(ip)
}

// refusePrivate is the dialer's last word: the address a name resolved to,
// checked as the connection is made (so a name that resolves somewhere else
// the second time is checked the second time).
func refusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrEndpoint, address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !PublicAddr(ip) {
		return fmt.Errorf("%w: %s is not a public address", ErrEndpoint, host)
	}
	return nil
}

// SafeClient is the client pushes go out on: direct connections only (no
// proxy from the environment - a proxy would dial the address for us, past
// the check), never to an address that is not public, and no redirects.
func SafeClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: timeout, Control: refusePrivate}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           d.DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var (
	defaultClientOnce sync.Once
	defaultClient     *http.Client
)

// Subscription is where one browser takes its pushes: what its
// PushSubscription.toJSON() gives.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Message is one push.
type Message struct {
	Payload []byte
	// TTL is how long the push service keeps it for a device that is offline.
	TTL time.Duration
	// Urgency is very-low, low, normal or high (RFC 8030 §5.3); "" is normal.
	Urgency string
}

// Result is the push service's answer.
type Result struct {
	Status int
	// Gone: the subscription no longer exists (404, 410) and is to be
	// forgotten - the browser unsubscribed, or the person cleared the site.
	Gone bool
}

// Sender sends pushes.
type Sender struct {
	// Client is the HTTP client. Nil: SafeClient (10 s).
	Client *http.Client
	// Subject is the VAPID contact (mailto: or https:).
	Subject string
	// Now is the clock the tokens' expiry is read from. Nil: time.Now.
	Now func() time.Time
}

// tokenLifetime is how long a VAPID token is valid; at most 24 hours.
const tokenLifetime = 12 * time.Hour

// Send encrypts msg for sub, signs it with key and posts it. A push service
// that took it answers 201 (any 2xx is taken); every other answer is an error,
// with Result.Gone set for a subscription that no longer exists.
func (s *Sender) Send(ctx context.Context, key *Key, sub Subscription, msg Message) (Result, error) {
	body, err := Encrypt(msg.Payload, sub.P256dh, sub.Auth)
	if err != nil {
		return Result{}, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	authz, err := key.Authorization(sub.Endpoint, s.Subject, now().Add(tokenLifetime))
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrEndpoint, err)
	}
	req.Header.Set("Authorization", authz)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	ttl := int64(msg.TTL / time.Second)
	if ttl < 0 {
		ttl = 0
	}
	req.Header.Set("TTL", strconv.FormatInt(ttl, 10))
	if msg.Urgency != "" {
		req.Header.Set("Urgency", msg.Urgency)
	}
	client := s.Client
	if client == nil {
		defaultClientOnce.Do(func() { defaultClient = SafeClient(10 * time.Second) })
		client = defaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("webpush: send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	res := Result{Status: resp.StatusCode}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return res, nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		res.Gone = true
	}
	return res, fmt.Errorf("webpush: the push service answered %d", resp.StatusCode)
}

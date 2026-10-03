package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// The loopback policy opens this machine and nothing else.
func TestPolicy_LoopbackOpensThisMachineOnly(t *testing.T) {
	lb := Policy{Loopback: true}
	for _, s := range []string{"127.0.0.1", "127.8.9.10", "::1", "::ffff:127.0.0.1"} {
		if lb.Refused(net.ParseIP(s)) {
			t.Errorf("loopback policy refuses %s", s)
		}
		if !(Policy{}).Refused(net.ParseIP(s)) {
			t.Errorf("the zero policy reaches %s", s)
		}
	}
	for _, s := range []string{"10.0.0.5", "192.168.1.199", "169.254.169.254", "100.100.0.2", "fd00::1", "fe80::1", "0.0.0.0", "64:ff9b::7f00:1"} {
		if !lb.Refused(net.ParseIP(s)) {
			t.Errorf("loopback policy reaches %s", s)
		}
	}
	if !lb.Refused(nil) {
		t.Error("nil stays refused")
	}
}

// DownloadClient: a redirect never goes from https to plain http, a hop to a
// refused address is refused, the chain is capped - and plain http that
// started as plain http (a loopback source in development) is not a
// downgrade.
func TestDownloadClient_RedirectRules(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		_, _ = w.Write([]byte("plain"))
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/after", http.StatusFound)
	}))
	defer secure.Close()
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.5/x", http.StatusFound)
	}))
	defer private.Close()
	var loop *httptest.Server
	loop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, loop.URL+r.URL.Path+"x", http.StatusFound)
	}))
	defer loop.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/hop", http.StatusFound)
	}))
	defer hop.Close()

	c := Policy{Loopback: true}.DownloadClient(10 * time.Second)
	// Trust the test server's certificate and nothing else changes.
	c.Transport.(*http.Transport).TLSClientConfig = secure.Client().Transport.(*http.Transport).TLSClientConfig

	_, err := c.Get(secure.URL + "/start")
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("https -> http: err = %v, want ErrDowngrade", err)
	}
	if plainHits.Load() != 0 {
		t.Fatal("the plain server was asked after an https start")
	}

	_, err = c.Get(private.URL + "/start")
	if !errors.Is(err, ErrPrivateTarget) {
		t.Fatalf("a hop to 10.0.0.5: err = %v, want ErrPrivateTarget", err)
	}

	_, err = c.Get(loop.URL + "/")
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("an endless chain: err = %v", err)
	}

	resp, err := c.Get(hop.URL + "/start")
	if err != nil {
		t.Fatalf("http -> http is no downgrade: %v", err)
	}
	_ = resp.Body.Close()
	if plainHits.Load() != 1 {
		t.Fatalf("plain hits = %d, want 1", plainHits.Load())
	}

	// The zero policy reaches none of it.
	_, err = Policy{}.DownloadClient(10 * time.Second).Get(plain.URL + "/")
	if !errors.Is(err, ErrPrivateTarget) {
		t.Fatalf("zero policy, loopback server: err = %v, want ErrPrivateTarget", err)
	}
}

// The chain is capped at MaxRedirects requests - the docs' "at most five",
// counted as net/http counts its own default of ten (`len(via) >= 10`, "after
// 10 consecutive requests"). TestDownloadClient_RedirectRules only asks that
// an endless chain ENDS, which a cap of 500 satisfies too: mutation S29g
// (MaxRedirects*100) lived through it. This one counts the requests.
func TestDownloadClient_StopsAfterMaxRedirects(t *testing.T) {
	if MaxRedirects != 5 {
		t.Fatalf("MaxRedirects = %d; APP-PLUGINS.md, PLUGINS.md and the changelog say five", MaxRedirects)
	}
	c := Policy{Loopback: true}.DownloadClient(10 * time.Second)

	var hits atomic.Int32
	var loop *httptest.Server
	loop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, loop.URL+r.URL.Path+"x", http.StatusFound)
	}))
	defer loop.Close()
	_, err := c.Get(loop.URL + "/")
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("an endless chain: err = %v", err)
	}
	if got := hits.Load(); got != MaxRedirects {
		t.Fatalf("an endless chain was followed for %d requests, want %d", got, MaxRedirects)
	}

	// A chain that ends within the cap is followed to its end.
	var steps atomic.Int32
	var short *httptest.Server
	short = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if steps.Add(1) < MaxRedirects {
			http.Redirect(w, r, short.URL+"/next", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("done"))
	}))
	defer short.Close()
	resp, err := c.Get(short.URL + "/")
	if err != nil {
		t.Fatalf("a chain of %d requests: %v", MaxRedirects, err)
	}
	_ = resp.Body.Close()
	if got := steps.Load(); got != MaxRedirects {
		t.Fatalf("a chain within the cap took %d requests, want %d", got, MaxRedirects)
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

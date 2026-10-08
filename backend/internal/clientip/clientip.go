// Package clientip is the ONE answer to "which address is this request from?".
//
// # Why it exists
//
// Three copies of this question lived in the code (api/middleware.go,
// auth/audit_middleware.go, api/handlers/drop.go), and all three believed
// X-Forwarded-For from ANY caller. That was harmless while the address only
// labelled a log line. It is not harmless once the address decides something:
// the sign-in throttle counts failures per address and exempts an allow-listed
// one, so a header the client writes itself would let an attacker (a) rotate
// through forged addresses and never hit the per-address limit, and (b) claim
// an allow-listed address and walk past every lock.
//
// # The rule
//
// The socket's peer is the truth. X-Forwarded-For and X-Real-IP are read ONLY
// when the peer is a configured trusted proxy, and then the forwarded chain is
// walked from its trusted end (right to left), skipping further trusted
// proxies, until the first address that is not one: that is the address the
// outermost trusted proxy actually saw. The leftmost entry — what the old code
// took — is whatever the client wrote and is never preferred.
//
// The trusted list is the `login.trusted_proxies` setting, else
// FILEX_TRUSTED_PROXIES, else `auto` (the setting wins over the environment,
// which wins over the default). Besides addresses and CIDR networks it takes
// four words:
//
//	auto        what filex works out about where it runs (auto.go)
//	loopback    netip.Addr.IsLoopback        — this machine
//	private     netip.Addr.IsPrivate         — RFC 1918 / RFC 4193 private ranges
//	link-local  netip.Addr.IsLinkLocalUnicast
//
// Unset, it is `auto`: loopback on a plain install and with host networking;
// in a container on its own network, loopback plus the container networks it
// is attached to, never their gateways (what the host relays arrives from the
// gateway), never its own addresses (what rootless Podman relays arrives from
// them) and never a network that puts it on the LAN (macvlan, ipvlan). A
// proxy anywhere else - on the host through a published port, on another
// machine, an ingress controller on another Kubernetes node - is named by
// hand ("auto, 192.0.2.10"); a peer that sends X-Forwarded-For without being
// trusted is remembered (forwarders.go) so the Sign-in security page can name
// it. `none` trusts no proxy. A deployment whose proxy chain has PUBLIC hops
// (Cloudflare's edge in front of a proxy) lists those ranges too, or every
// visitor resolves to the edge's address and shares one per-address counter.
//
// Until 0.50 the default was loopback, private and link-local: every private
// address, the LAN included, could name the client.
//
// ⚠ The classes are the standard library's definition, not a list of ranges
// kept here: an address is compared after unmapping (an IPv4-mapped IPv6
// address is the IPv4 address it carries), so `::ffff:<private IPv4>` is
// private exactly as the IPv4 address is.
//
// Protocol servers (FTP, SFTP) have no headers: their peer is the client, and
// PeerOf is the whole answer.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
)

// Set is a list of addresses and networks, plus the classes of address (the
// words of a trusted-proxy list) it takes in whole.
type Set struct {
	prefixes []netip.Prefix
	classes  Classes
}

// The words a trusted-proxy list understands besides addresses and networks.
const (
	WordAuto      = "auto"
	WordLoopback  = "loopback"
	WordPrivate   = "private"
	WordLinkLocal = "link-local"
	WordNone      = "none"
)

// Classes says which classes of address a set takes in whole. Loopback,
// Private and LinkLocal are each the standard library's own test (net/netip),
// never a list of ranges kept here; Auto is the automatic set in force (see
// auto.go), read at the moment of asking.
type Classes struct {
	// Auto: the automatic set - loopback plus, in a container on its own
	// network, that network minus its gateways (AutoResolution).
	Auto bool `json:"auto"`
	// Loopback: netip.Addr.IsLoopback — this machine.
	Loopback bool `json:"loopback"`
	// Private: netip.Addr.IsPrivate — the RFC 1918 (IPv4) and RFC 4193 (IPv6)
	// private ranges.
	Private bool `json:"private"`
	// LinkLocal: netip.Addr.IsLinkLocalUnicast.
	LinkLocal bool `json:"link_local"`
}

// DefaultClasses is what an unset trusted-proxy list means: `auto`.
func DefaultClasses() Classes { return Classes{Auto: true} }

// Words spells the classes as a list's words, in a fixed order: auto,
// loopback, private, link-local.
func (c Classes) Words() []string {
	var out []string
	if c.Auto {
		out = append(out, WordAuto)
	}
	if c.Loopback {
		out = append(out, WordLoopback)
	}
	if c.Private {
		out = append(out, WordPrivate)
	}
	if c.LinkLocal {
		out = append(out, WordLinkLocal)
	}
	return out
}

// has reports whether ip (unmapped, zone-less) belongs to one of the classes.
func (c Classes) has(ip netip.Addr) bool {
	return (c.Loopback && ip.IsLoopback()) ||
		(c.Private && ip.IsPrivate()) ||
		(c.LinkLocal && ip.IsLinkLocalUnicast()) ||
		(c.Auto && currentAuto().contains(ip))
}

// DefaultSet is what "not configured" means: `auto`.
func DefaultSet() *Set { return &Set{classes: DefaultClasses()} }

// ParseList reads a trusted-proxy list: IP addresses and CIDR networks
// separated by commas, spaces or newlines, and the words `auto`, `loopback`,
// `private` and `link-local` (each takes that whole class of address, see
// Classes). A list replaces the default, so a list that should keep the
// automatic set names it: "auto, 203.0.113.0/24". `none` trusts no proxy at
// all (the socket's peer is always the client); written next to other entries
// it adds nothing and takes nothing away.
//
// Blank text is "not set" and answers (nil, nil), so a caller falls through to
// the next source. A bad entry is an error that names it.
func ParseList(text string) (*Set, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	if len(fields) == 0 {
		return nil, nil
	}
	set := &Set{}
	for _, f := range fields {
		switch strings.ToLower(f) {
		case WordNone:
			continue
		case WordAuto:
			set.classes.Auto = true
			continue
		case WordLoopback:
			set.classes.Loopback = true
			continue
		case WordPrivate:
			set.classes.Private = true
			continue
		case WordLinkLocal, "link_local":
			set.classes.LinkLocal = true
			continue
		}
		p, err := parseEntry(f)
		if err != nil {
			return nil, err
		}
		set.prefixes = append(set.prefixes, p)
	}
	return set, nil
}

// ParseEntries is ParseList for a caller that already holds separate entries,
// and rejects the words: an allow-list of client addresses must name
// addresses.
func ParseEntries(entries []string) (*Set, error) {
	set := &Set{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		p, err := parseEntry(e)
		if err != nil {
			return nil, err
		}
		set.prefixes = append(set.prefixes, p)
	}
	return set, nil
}

func parseEntry(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an address or a network (CIDR)", s)
		}
		return netip.PrefixFrom(p.Addr().Unmap(), unmapBits(p)).Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a network (CIDR)", s)
	}
	a = a.Unmap().WithZone("")
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// unmapBits keeps a ::ffff:a.b.c.d/N prefix meaning what it says once its
// address is unmapped to IPv4.
func unmapBits(p netip.Prefix) int {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return p.Bits() - 96
	}
	return p.Bits()
}

// Contains reports whether ip is in the set. A nil set holds nothing.
func (s *Set) Contains(ip netip.Addr) bool {
	if s == nil || !ip.IsValid() {
		return false
	}
	ip = ip.Unmap().WithZone("")
	if s.classes.has(ip) {
		return true
	}
	for _, p := range s.prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ContainsString is Contains for an address in text; anything that does not
// parse is not contained.
func (s *Set) ContainsString(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	return s.Contains(a)
}

// Strings lists the set's entries in the form they were parsed to: the words
// of its classes first (auto, loopback, private, link-local), then its
// addresses and networks.
func (s *Set) Strings() []string {
	if s == nil {
		return nil
	}
	return append(s.classes.Words(), s.Addresses()...)
}

// Effective lists what the set trusts right now, with `auto` spelled out: the
// class words (loopback, private, link-local - `auto` always brings
// loopback), then the container networks `auto` resolved to, then the set's
// own addresses and networks; nothing twice. What `auto` carves out of its
// networks is AutoResolution.ExcludedGateways and ExcludedSelf.
func (s *Set) Effective() []string {
	if s == nil {
		return nil
	}
	c := s.classes
	var auto AutoResolution
	if c.Auto {
		auto = currentAuto().res
		c.Loopback = true
	}
	c.Auto = false
	out := c.Words()
	seen := map[string]bool{}
	for _, w := range out {
		seen[w] = true
	}
	for _, e := range append(append([]string{}, auto.Networks...), s.Addresses()...) {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// Classes reports which classes of address the set takes in whole (none for a
// nil set).
func (s *Set) Classes() Classes {
	if s == nil {
		return Classes{}
	}
	return s.classes
}

// Addresses lists the set's own addresses and networks, without its classes.
func (s *Set) Addresses() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.prefixes))
	for _, p := range s.prefixes {
		if p.IsSingleIP() {
			out = append(out, p.Addr().String())
		} else {
			out = append(out, p.String())
		}
	}
	return out
}

// source answers the trusted-proxy set in force right now. It is a func, not a
// value, because the set can be edited at run time (the `login.trusted_proxies`
// setting) and the three callers that used to hold a copy of this code have no
// constructor to thread a dependency through.
var source atomic.Pointer[func() *Set]

// SetSource installs the function that answers the trusted set (nil = the
// default). It returns a func that puts the previous source back — for tests.
func SetSource(fn func() *Set) (restore func()) {
	prev := source.Load()
	if fn == nil {
		source.Store(nil)
	} else {
		source.Store(&fn)
	}
	return func() { source.Store(prev) }
}

func trusted() *Set {
	if fn := source.Load(); fn != nil {
		if s := (*fn)(); s != nil {
			return s
		}
	}
	return DefaultSet()
}

// FromRequest is the address the request came from, without a port; see the
// package comment for what is believed and why.
func FromRequest(r *http.Request) string {
	peer, ok := parseHostPort(r.RemoteAddr)
	if !ok {
		return strings.TrimSpace(r.RemoteAddr)
	}
	set := trusted()
	if !set.Contains(peer) {
		return peer.String()
	}
	if chain := forwardedChain(r); len(chain) > 0 {
		// From the trusted end: skip our own proxies, stop at the first
		// address that is not one.
		var last netip.Addr
		for i := len(chain) - 1; i >= 0; i-- {
			a, ok := parseAny(chain[i])
			if !ok {
				// A hop we cannot read is a hop we cannot vouch for.
				return peer.String()
			}
			if !set.Contains(a) {
				return a.String()
			}
			last = a
		}
		// Every hop was one of ours: the origin is the leftmost.
		return last.String()
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if a, ok := parseAny(v); ok {
			return a.String()
		}
	}
	return peer.String()
}

// ProxyItself reports whether a request was made BY a trusted proxy, not
// forwarded through one: the socket's peer is in the trusted set and the
// request carries no forwarded-for header (X-Forwarded-For, X-Real-IP,
// Forwarded) that a proxy adds to what it passes on. The question a hook
// the proxy alone may ask must answer (/api/tls/ask, docs/TENANT-ADMIN.md):
// a stranger's request forwarded by the same proxy is not the proxy's.
func ProxyItself(r *http.Request) bool {
	peer, ok := parseHostPort(r.RemoteAddr)
	if !ok || !trusted().Contains(peer) {
		return false
	}
	return r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == "" && r.Header.Get("Forwarded") == ""
}

// PeerTrusted reports whether the socket's peer is a trusted proxy: the only
// sender whose X-Forwarded-* headers are believed (X-Forwarded-Proto as much
// as X-Forwarded-For). Unlike ProxyItself it does not ask whether the request
// was forwarded - a proxy passing a visitor's request on is exactly the
// sender whose X-Forwarded-Proto says how the visitor arrived.
func PeerTrusted(r *http.Request) bool {
	if r == nil {
		return false
	}
	peer, ok := parseHostPort(r.RemoteAddr)
	return ok && trusted().Contains(peer)
}

func forwardedChain(r *http.Request) []string {
	var out []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(line, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// PeerOf is the address of a socket peer ("host:port" or a bare address), for
// the protocol servers that have no headers.
func PeerOf(remote string) string {
	if a, ok := parseHostPort(remote); ok {
		return a.String()
	}
	return strings.TrimSpace(remote)
}

func parseHostPort(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	return parseAddr(s)
}

// parseAny reads an address that may carry a port or brackets, as forwarding
// proxies write them.
func parseAny(s string) (netip.Addr, bool) { return parseHostPort(s) }

func parseAddr(s string) (netip.Addr, bool) {
	s = strings.Trim(s, "[]")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

type ctxKey struct{}

// WithIP carries the client address down to code that has no *http.Request —
// the protocol authentication path.
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ip)
}

// FromContext is the address WithIP carried, or "".
func FromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxKey{}).(string)
	return s
}

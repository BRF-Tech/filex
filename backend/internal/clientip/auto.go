package clientip

// auto.go - the `auto` word: which peers filex believes about a client's
// address when nobody said, worked out from where it runs.
//
// # The rule
//
// Loopback always. Beyond it only the networks a CONTAINER is attached to on
// its own network namespace, because on such a network the only peers are the
// other containers of the deployment (a Caddy or nginx container in front of
// filex) - minus every gateway:
//
//   - plain install (no container marker): loopback only. The LAN is never
//     trusted by default; a proxy on another machine is named by hand.
//   - container on its own network (Docker bridge / user-defined networks,
//     Podman rootful netavark or CNI bridges, a Kubernetes pod): loopback plus
//     the subnet of each of its OWN veth interfaces, all of them when it is
//     attached to several networks.
//   - minus the gateways: every next hop in /proc/net/route and
//     /proc/net/ipv6_route, and the first host address of each trusted subnet
//     (Docker's IPAM default gateway, .1 or ::1). What the host relays arrives
//     FROM the gateway - measured on Docker 29 (docs/DOCKER.md): a connection
//     to a published port on 127.0.0.1 goes through docker-proxy and reaches
//     the container from 172.x.0.1; a host process dialling the container's
//     address directly does too. Trusting the gateway would let anything that
//     reaches a published port through the host choose its own address.
//   - minus filex's OWN addresses: a connection from one of them was made
//     inside its own network namespace. Measured on Podman 5.8 rootless: on a
//     user-defined network (a veth into the rootless bridge) rootlessport
//     relays every published-port connection - from the internet too - from
//     the container's own address; with slirp4netns' default port handler it
//     arrives from 10.0.2.100, the container's own tap address.
//   - host networking (the container sees the host's interfaces: a physical
//     NIC, a bridge such as docker0): like a plain install, loopback only.
//   - macvlan / ipvlan put the container ON the LAN: never trusted.
//   - tun/tap (Podman rootless: slirp4netns' tap0 10.0.2.100/24, pasta's
//     copy of the host's interface and address): never trusted. Rootless
//     Podman says so in /run/.containerenv (rootless=1) only when the
//     container is privileged - the file is empty otherwise (measured) - and
//     is then loopback only; without that word the rules above already keep
//     every relay it uses out (the tun interface, its own address, the
//     gateway).
//   - IPv6: a container network's ULA (or global) subnet counts like an IPv4
//     one, minus its gateway; link-local (fe80::/10, 169.254.0.0/16) never -
//     the host bridge's own link-local address sits there.
//   - Kubernetes (KUBERNETES_SERVICE_HOST, or kubepods in the cgroup): the
//     pod's own interface subnet - a node's /24 with flannel, a /32 with
//     Calico or Cilium (then loopback and the pod itself). An ingress
//     controller on another node is outside it: list the pod CIDR by hand.
//   - anything that cannot be read, or that contradicts itself: loopback only
//     AND a warning (logged once, shown on the page).
//
// The resolution is re-run every minute (the sign-in limiter's loop, see
// server.go): `docker network connect` adds an interface at run time. It is a
// handful of small /proc and /sys reads and one netlink dump, and it logs only
// when the answer changes.
//
// Everything is read through the System seam, so every case above is a unit
// test with a fixture (auto_test.go) taken from real containers.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The environments `auto` tells apart (AutoResolution.Environment).
const (
	EnvPlain          = "plain"
	EnvContainer      = "container"
	EnvHostNetwork    = "host-network"
	EnvKubernetes     = "kubernetes"
	EnvPodmanRootless = "podman-rootless"
	EnvUnknown        = "unknown"
)

// The container runtimes the markers name (AutoResolution.Runtime).
const (
	RuntimeDocker     = "docker"
	RuntimePodman     = "podman"
	RuntimeKubernetes = "kubernetes"
	RuntimeContainerd = "containerd"
	RuntimeOther      = "container"
)

// Why an interface is or is not trusted (AutoInterface.Reason).
const (
	ReasonContainerNetwork = "container-network" // a veth on a container network: its subnet is trusted
	ReasonLAN              = "lan"               // macvlan / ipvlan: the container sits on the LAN
	ReasonTunnel           = "tunnel"            // tun/tap: user-mode networking or a VPN
	ReasonHost             = "host"              // a physical NIC or a bridge: the host's own network
	ReasonLinkLocal        = "link-local-only"   // a veth with nothing but link-local addresses
	ReasonDown             = "down"              // not up
	ReasonRootless         = "rootless"          // rootless Podman: nothing but loopback
	ReasonOther            = "other"             // another kind of interface
)

// The warnings `auto` can raise (AutoResolution.Warning). Each means it fell
// back to loopback only.
const (
	// WarnUnreadable: something the answer depends on could not be read
	// (the container markers, the interfaces, their kinds, the routes).
	WarnUnreadable = "unreadable"
	// WarnAmbiguous: what was read contradicts itself (a container network
	// next to a bridge of the host's kind, with no physical NIC to settle it).
	WarnAmbiguous = "ambiguous"
)

// AutoResolution is what `auto` resolved to, and why.
type AutoResolution struct {
	// Environment is plain, container, host-network, kubernetes,
	// podman-rootless or unknown (the Env* constants).
	Environment string `json:"environment"`
	// Runtime is the container runtime the markers name: docker, podman,
	// kubernetes, containerd, container (a marker without a name); "" outside
	// a container.
	Runtime string `json:"runtime"`
	// Networks are the container networks trusted besides loopback, as CIDR.
	Networks []string `json:"networks"`
	// ExcludedGateways are the gateway addresses found (route next hops, the
	// first host address of each trusted network): never trusted by `auto`,
	// even inside a trusted network.
	ExcludedGateways []string `json:"excluded_gateways"`
	// ExcludedSelf are filex's own addresses on the trusted networks: never
	// trusted either. A connection from one of them was made inside filex's
	// own network namespace - measured: rootless Podman on a user-defined
	// network relays every published-port connection (from the internet
	// too) from the container's own address.
	ExcludedSelf []string `json:"excluded_self"`
	// Interfaces is what was looked at and what became of each (the "why").
	Interfaces []AutoInterface `json:"interfaces"`
	// Warning is "" or one of the Warn* codes; WarningDetail says what
	// exactly (English, for the log and the page's detail line).
	Warning       string `json:"warning"`
	WarningDetail string `json:"warning_detail,omitempty"`
	// ResolvedAt is when this answer was worked out.
	ResolvedAt time.Time `json:"resolved_at"`
}

// AutoInterface is one network interface as `auto` saw it.
type AutoInterface struct {
	Name string `json:"name"`
	// Kind is the link kind the kernel reports (veth, macvlan, ipvlan, tun,
	// bridge, ...), "physical" for a NIC with a device behind it, "" when it
	// has none of either.
	Kind string `json:"kind"`
	// Addresses are its addresses with their prefix length.
	Addresses []string `json:"addresses"`
	// Trusted: its network is part of the automatic set.
	Trusted bool `json:"trusted"`
	// Reason is one of the Reason* codes.
	Reason string `json:"reason"`
}

// Iface is one network interface as the System reports it.
type Iface struct {
	Name     string
	Up       bool
	Loopback bool
	// Addrs are the interface's addresses with their prefix length (the
	// host part kept: 172.18.0.5/16).
	Addrs []netip.Prefix
}

// System is everything `auto` reads. The real one (auto_linux.go,
// auto_other.go) reads the running system; tests hand in fixtures.
type System interface {
	// GOOS is runtime.GOOS; only Linux has containers to look for.
	GOOS() string
	// ReadFile reads a file (/proc/..., /run/.containerenv).
	ReadFile(name string) ([]byte, error)
	// Stat reports whether a path exists: nil, fs.ErrNotExist, or another
	// error when it cannot be told.
	Stat(name string) error
	// Getenv reads an environment variable.
	Getenv(key string) string
	// Interfaces lists the network interfaces with their addresses.
	Interfaces() ([]Iface, error)
	// LinkKinds answers the kernel's link kind of each interface by name
	// (netlink IFLA_INFO_KIND: veth, macvlan, ipvlan, tun, bridge, ...); an
	// interface with no kind (a physical NIC, loopback) is absent or "".
	LinkKinds() (map[string]string, error)
	// ByteOrder is how /proc/net/route prints an address: the host's own.
	ByteOrder() binary.ByteOrder
	// Now is the clock.
	Now() time.Time
}

/* ── the state in force ─────────────────────────────────────────────────── */

// autoSet is an AutoResolution ready to answer Contains.
type autoSet struct {
	res  AutoResolution
	nets []netip.Prefix
	// excl are the addresses carved out of nets: the gateways and filex's own.
	excl []netip.Addr
}

// contains: loopback, or inside a trusted network and neither a gateway nor
// filex's own address.
func (a *autoSet) contains(ip netip.Addr) bool {
	if ip.IsLoopback() {
		return true
	}
	if a == nil || slices.Contains(a.excl, ip) {
		return false
	}
	for _, p := range a.nets {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// isRelay reports whether ip is an address `auto` carves out because relayed
// connections arrive from it: a gateway, or filex's own address.
func (a *autoSet) isRelay(ip netip.Addr) bool {
	return a != nil && slices.Contains(a.excl, ip)
}

// buildAutoSet parses a resolution's networks and gateways. An entry that does
// not parse is dropped (a resolver in a test may hand in anything).
func buildAutoSet(res AutoResolution) *autoSet {
	a := &autoSet{res: res}
	if a.res.Networks == nil {
		a.res.Networks = []string{}
	}
	if a.res.ExcludedGateways == nil {
		a.res.ExcludedGateways = []string{}
	}
	if a.res.ExcludedSelf == nil {
		a.res.ExcludedSelf = []string{}
	}
	if a.res.Interfaces == nil {
		a.res.Interfaces = []AutoInterface{}
	}
	for _, n := range res.Networks {
		if p, err := parseEntry(n); err == nil {
			a.nets = append(a.nets, p)
		}
	}
	for _, g := range append(append([]string{}, res.ExcludedGateways...), res.ExcludedSelf...) {
		if ip, ok := parseAddr(g); ok {
			a.excl = append(a.excl, ip)
		}
	}
	return a
}

var (
	autoCur atomic.Pointer[autoSet]
	// autoMu serialises resolving: one resolution at a time, the resolver
	// swapped only between them.
	autoMu       sync.Mutex
	autoResolver = func() AutoResolution { return ResolveAuto(realSystem{}) }
	// autoLogged: the first resolution has been logged.
	autoLogged bool
)

// currentAuto is the automatic set in force, resolved on first use.
func currentAuto() *autoSet {
	if a := autoCur.Load(); a != nil {
		return a
	}
	autoMu.Lock()
	defer autoMu.Unlock()
	if a := autoCur.Load(); a != nil {
		return a
	}
	a := buildAutoSet(autoResolver())
	autoCur.Store(a)
	logAuto(nil, a)
	return a
}

// Auto answers what `auto` resolves to right now (resolving it if nothing has
// yet).
func Auto() AutoResolution { return currentAuto().res }

// IsAutoRelay reports whether ip is an address `auto` carves out of its
// networks because relayed connections arrive from it: a gateway (the host's
// docker-proxy, a host process) or filex's own address (rootless Podman's
// port forwarding).
func IsAutoRelay(ip netip.Addr) bool {
	return currentAuto().isRelay(ip.Unmap().WithZone(""))
}

// RefreshAuto works the automatic set out again and puts it in force. It logs
// only when the answer changed (the first answer is always logged), so it can
// run every minute. It returns the answer.
func RefreshAuto() AutoResolution {
	autoMu.Lock()
	defer autoMu.Unlock()
	prev := autoCur.Load()
	a := buildAutoSet(autoResolver())
	autoCur.Store(a)
	logAuto(prev, a)
	return a.res
}

// SetAutoResolver replaces what works the automatic set out (tests; nil = the
// running system) and forgets the answer in force, so the next use asks fn. It
// returns a func that puts the previous resolver back.
func SetAutoResolver(fn func() AutoResolution) (restore func()) {
	autoMu.Lock()
	defer autoMu.Unlock()
	prevFn, prevSet := autoResolver, autoCur.Load()
	if fn == nil {
		fn = func() AutoResolution { return ResolveAuto(realSystem{}) }
	}
	autoResolver = fn
	autoCur.Store(nil)
	return func() {
		autoMu.Lock()
		defer autoMu.Unlock()
		autoResolver = prevFn
		autoCur.Store(prevSet)
	}
}

// sameAnswer: nothing an operator would want to hear about changed.
func sameAnswer(a, b AutoResolution) bool {
	return a.Environment == b.Environment && a.Runtime == b.Runtime &&
		slices.Equal(a.Networks, b.Networks) && slices.Equal(a.ExcludedGateways, b.ExcludedGateways) &&
		slices.Equal(a.ExcludedSelf, b.ExcludedSelf) &&
		a.Warning == b.Warning && a.WarningDetail == b.WarningDetail
}

// logAuto writes the first answer, and afterwards only a changed one. Caller
// holds autoMu.
func logAuto(prev, cur *autoSet) {
	if autoLogged && prev != nil && sameAnswer(prev.res, cur.res) {
		return
	}
	first := !autoLogged
	autoLogged = true
	r := cur.res
	attrs := []any{
		slog.String("environment", r.Environment),
		slog.String("runtime", r.Runtime),
		slog.Any("networks", r.Networks),
		slog.Any("excluded_gateways", r.ExcludedGateways),
		slog.Any("excluded_self", r.ExcludedSelf),
	}
	msg := "clientip: the automatic trusted-proxy set (auto) changed"
	if first {
		msg = "clientip: the automatic trusted-proxy set (auto) resolved: loopback plus the networks listed"
	}
	slog.Info(msg, attrs...)
	if r.Warning != "" && (prev == nil || prev.res.Warning != r.Warning || prev.res.WarningDetail != r.WarningDetail || first) {
		slog.Warn("clientip: could not tell where filex runs, so auto trusts loopback only - name your proxy in FILEX_TRUSTED_PROXIES or on the Sign-in security page",
			slog.String("warning", r.Warning), slog.String("detail", r.WarningDetail))
	}
}

/* ── working it out ─────────────────────────────────────────────────────── */

// ResolveAuto works out the automatic set from sys. See the file comment for
// the rule; every branch has a fixture in auto_test.go.
func ResolveAuto(sys System) AutoResolution {
	res := AutoResolution{
		Networks: []string{}, ExcludedGateways: []string{}, ExcludedSelf: []string{}, Interfaces: []AutoInterface{},
		ResolvedAt: sys.Now().UTC(),
	}
	if sys.GOOS() != "linux" {
		// Windows and macOS: filex runs on the machine itself (Docker Desktop
		// runs the Linux build, inside its VM).
		res.Environment = EnvPlain
		return res
	}
	m, merr := detectContainer(sys)
	if !m.found {
		res.Environment = EnvPlain
		if merr != nil {
			warn(&res, WarnUnreadable, "could not tell whether filex runs in a container: %v", merr)
		}
		return res
	}
	res.Runtime = m.runtime

	ifaces, err := sys.Interfaces()
	if err != nil {
		res.Environment = EnvUnknown
		warn(&res, WarnUnreadable, "could not list the network interfaces: %v", err)
		return res
	}
	kinds, err := sys.LinkKinds()
	if err != nil {
		res.Environment = EnvUnknown
		warn(&res, WarnUnreadable, "could not read the network interfaces' kinds (netlink): %v", err)
		return res
	}

	// Each interface: its kind, and whether it is the host's own.
	var (
		physical, bridged bool
		views             []AutoInterface
		candidates        = map[string][]netip.Prefix{} // veth name -> its addresses on a container network
	)
	for _, ifc := range ifaces {
		if ifc.Loopback {
			continue
		}
		kind := strings.ToLower(kinds[ifc.Name])
		isPhys := false
		if kind == "" {
			switch err := sys.Stat("/sys/class/net/" + ifc.Name + "/device"); {
			case err == nil:
				isPhys, kind = true, "physical"
			case errors.Is(err, fs.ErrNotExist):
			default:
				res.Environment = EnvUnknown
				warn(&res, WarnUnreadable, "could not tell whether %s is a physical interface: %v", ifc.Name, err)
				return res
			}
		}
		v := AutoInterface{Name: ifc.Name, Kind: kind, Addresses: []string{}}
		for _, p := range ifc.Addrs {
			v.Addresses = append(v.Addresses, prefixText(p))
		}
		switch {
		case isPhys:
			physical, v.Reason = true, ReasonHost
		case kind == "bridge" || kind == "bond" || kind == "team" || kind == "openvswitch":
			bridged, v.Reason = true, ReasonHost
		case kind == "macvlan" || kind == "macvtap" || kind == "ipvlan" || kind == "ipvtap":
			v.Reason = ReasonLAN
		case kind == "tun" || kind == "tap":
			v.Reason = ReasonTunnel
		case kind == "veth" && !ifc.Up:
			v.Reason = ReasonDown
		case kind == "veth":
			for _, p := range ifc.Addrs {
				a := p.Addr().Unmap()
				if a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsMulticast() || !a.IsValid() {
					continue
				}
				candidates[ifc.Name] = append(candidates[ifc.Name], netip.PrefixFrom(a, unmapBits(p)))
			}
			v.Reason = ReasonLinkLocal
			if len(candidates[ifc.Name]) > 0 {
				v.Reason = ReasonContainerNetwork
			}
		default:
			v.Reason = ReasonOther
		}
		// An interface with no address and no say in the answer (the tunl0,
		// sit0, ip6tnl0 a kernel puts in every namespace) is left out.
		if len(v.Addresses) == 0 && v.Reason != ReasonHost {
			continue
		}
		views = append(views, v)
	}
	sortIfaces(views)
	res.Interfaces = views

	if m.rootless {
		res.Environment = EnvPodmanRootless
		for i := range res.Interfaces {
			res.Interfaces[i].Reason = ReasonRootless
		}
		return res
	}
	switch {
	case physical:
		// The host's own interfaces: host networking (or a pod with
		// hostNetwork). Like a plain install.
		res.Environment = EnvHostNetwork
		res.Interfaces = hostViews(res.Interfaces)
		return res
	case bridged && len(candidates) > 0:
		// A bridge of the host's kind next to a container network, and no
		// NIC to say which it is (Docker-in-Docker, a nested runtime).
		res.Environment = EnvUnknown
		warn(&res, WarnAmbiguous, "a bridge interface sits next to a container network and no physical interface says which namespace this is")
		return res
	case bridged:
		res.Environment = EnvHostNetwork
		res.Interfaces = hostViews(res.Interfaces)
		return res
	}

	res.Environment = EnvContainer
	if m.runtime == RuntimeKubernetes {
		res.Environment = EnvKubernetes
	}
	if len(candidates) == 0 {
		return res
	}

	// The networks, and what is carved out of them: every next hop in the
	// routing tables, the first host address of each network, and filex's own
	// addresses.
	var nets []netip.Prefix
	self := map[netip.Addr]bool{}
	has6 := false
	for _, ps := range candidates {
		for _, p := range ps {
			nets = append(nets, p.Masked())
			self[p.Addr()] = true
			if p.Addr().Is6() {
				has6 = true
			}
		}
	}
	gws := map[netip.Addr]bool{}
	hops, err := routeGateways4(sys)
	if err != nil {
		return fallBack(res, "could not read the IPv4 routing table: %v", err)
	}
	for _, g := range hops {
		gws[g] = true
	}
	if has6 {
		hops6, err := routeGateways6(sys)
		if err != nil {
			return fallBack(res, "could not read the IPv6 routing table: %v", err)
		}
		for _, g := range hops6 {
			gws[g] = true
		}
	}
	for _, p := range nets {
		if g, ok := firstHost(p); ok {
			gws[g] = true
		}
	}
	slices.SortFunc(nets, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	nets = slices.Compact(nets)
	for _, p := range nets {
		res.Networks = append(res.Networks, prefixText(p))
	}
	res.ExcludedGateways = sortedAddrs(gws)
	for g := range gws {
		delete(self, g)
	}
	res.ExcludedSelf = sortedAddrs(self)
	for i := range res.Interfaces {
		if res.Interfaces[i].Reason == ReasonContainerNetwork {
			res.Interfaces[i].Trusted = true
		}
	}
	return res
}

// maxHostViews bounds the interfaces a host-network answer lists.
const maxHostViews = 32

// hostViews keeps, of the host's interfaces, the ones worth reading: its NICs
// and bridges, and anything with an address that is not link-local - not the
// other containers' veth ends a Docker host is full of.
func hostViews(v []AutoInterface) []AutoInterface {
	out := []AutoInterface{}
	for _, ifc := range v {
		keep := ifc.Reason == ReasonHost
		for _, a := range ifc.Addresses {
			if p, err := netip.ParsePrefix(a); err == nil && !p.Addr().IsLinkLocalUnicast() {
				keep = true
			}
		}
		if keep && len(out) < maxHostViews {
			out = append(out, ifc)
		}
	}
	return out
}

func sortedAddrs(set map[netip.Addr]bool) []string {
	list := make([]netip.Addr, 0, len(set))
	for a := range set {
		list = append(list, a)
	}
	slices.SortFunc(list, func(a, b netip.Addr) int { return a.Compare(b) })
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.String())
	}
	return out
}

// fallBack drops everything but loopback and says why: without the gateways a
// container network cannot be trusted safely.
func fallBack(res AutoResolution, format string, args ...any) AutoResolution {
	res.Environment = EnvUnknown
	res.Networks = []string{}
	res.ExcludedGateways = []string{}
	res.ExcludedSelf = []string{}
	for i := range res.Interfaces {
		res.Interfaces[i].Trusted = false
	}
	warn(&res, WarnUnreadable, format, args...)
	return res
}

func warn(res *AutoResolution, code, format string, args ...any) {
	res.Warning = code
	res.WarningDetail = fmt.Sprintf(format, args...)
}

func sortIfaces(v []AutoInterface) {
	slices.SortStableFunc(v, func(a, b AutoInterface) int { return strings.Compare(a.Name, b.Name) })
}

func prefixText(p netip.Prefix) string {
	if p.Addr().Is4In6() {
		p = netip.PrefixFrom(p.Addr().Unmap(), unmapBits(p))
	}
	return p.String()
}

// firstHost is the network's first host address - Docker's IPAM default
// gateway (.1, ::1). A network too small to have one apart from its own
// members (/31, /32, /127, /128) has none.
func firstHost(p netip.Prefix) (netip.Addr, bool) {
	if p.Bits() >= p.Addr().BitLen()-1 {
		return netip.Addr{}, false
	}
	return p.Masked().Addr().Next(), true
}

/* ── container markers ──────────────────────────────────────────────────── */

type markers struct {
	found    bool
	runtime  string
	rootless bool
}

// detectContainer reads the markers a container runtime leaves behind:
// /.dockerenv (Docker), /run/.containerenv (Podman; rootless=1 in it), the
// environment (KUBERNETES_SERVICE_HOST, container=podman|docker|oci), the
// cgroup paths of PID 1 and of this process (kubepods, docker, libpod,
// containerd, crio), and the mounts the runtime puts on / and on
// /etc/hosts|hostname|resolv.conf (a mount elsewhere on the host names docker
// too, so only those mount points are read). The error is what could not be
// read; it matters only when no marker was found.
func detectContainer(sys System) (markers, error) {
	var m markers
	var errs []error
	seen := map[string]bool{}

	if sys.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		seen[RuntimeKubernetes] = true
	}
	switch strings.ToLower(strings.TrimSpace(sys.Getenv("container"))) {
	case "podman":
		seen[RuntimePodman] = true
	case "docker":
		seen[RuntimeDocker] = true
	case "oci":
		seen[RuntimeOther] = true
	}
	switch err := sys.Stat("/.dockerenv"); {
	case err == nil:
		seen[RuntimeDocker] = true
	case !errors.Is(err, fs.ErrNotExist):
		errs = append(errs, err)
	}
	switch data, err := sys.ReadFile("/run/.containerenv"); {
	case err == nil:
		seen[RuntimePodman] = true
		m.rootless = containerenvRootless(data)
	case !errors.Is(err, fs.ErrNotExist):
		// It exists but cannot be read: Podman, rootless unknown.
		if serr := sys.Stat("/run/.containerenv"); serr == nil {
			seen[RuntimePodman] = true
		}
		errs = append(errs, err)
	}
	for _, f := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
		data, err := sys.ReadFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, r := range cgroupRuntimes(string(data)) {
			seen[r] = true
		}
	}
	if data, err := sys.ReadFile("/proc/self/mountinfo"); err != nil {
		errs = append(errs, err)
	} else {
		for _, r := range mountRuntimes(string(data)) {
			seen[r] = true
		}
	}

	for _, r := range []string{RuntimeKubernetes, RuntimePodman, RuntimeDocker, RuntimeContainerd, RuntimeOther} {
		if seen[r] {
			m.found, m.runtime = true, r
			break
		}
	}
	if m.found && m.runtime != RuntimePodman {
		// rootless=1 is Podman's word; another runtime's container has none.
		m.rootless = m.rootless && seen[RuntimePodman]
	}
	return m, errors.Join(errs...)
}

// containerenvRootless reads rootless=1 out of /run/.containerenv.
func containerenvRootless(data []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && strings.TrimSpace(k) == "rootless" {
			return strings.Trim(strings.TrimSpace(v), `"`) == "1"
		}
	}
	return false
}

// cgroupRuntimes names the runtimes a /proc/<pid>/cgroup text points at. With
// a private cgroup namespace (cgroup v2, Docker's default) it is "0::/" and
// names nothing - the other markers cover that.
func cgroupRuntimes(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		p := strings.ToLower(parts[2])
		switch {
		case strings.Contains(p, "kubepods"):
			out = append(out, RuntimeKubernetes)
		case strings.Contains(p, "libpod"):
			out = append(out, RuntimePodman)
		case strings.Contains(p, "/docker/") || strings.Contains(p, "/docker-") || strings.HasSuffix(p, "/docker"):
			out = append(out, RuntimeDocker)
		case strings.Contains(p, "containerd") || strings.Contains(p, "crio-") || strings.Contains(p, "/crio/"):
			out = append(out, RuntimeContainerd)
		}
	}
	return out
}

// mountRuntimes names the runtimes the mounts on /, /etc/hosts, /etc/hostname
// and /etc/resolv.conf point at (their source path or the overlay's layers).
// Measured on Docker 29 (containerd snapshotter): / is an overlay whose
// lowerdir is under /var/lib/containerd, and /etc/hosts is bound from
// /var/lib/docker/containers/<id>/hosts.
func mountRuntimes(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mountPoint := f[4]
		switch mountPoint {
		case "/", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf":
		default:
			continue
		}
		l := strings.ToLower(line)
		switch {
		case strings.Contains(l, "/kubelet/pods/") || strings.Contains(l, "kubepods"):
			out = append(out, RuntimeKubernetes)
		case strings.Contains(l, "/containers/storage/") || strings.Contains(l, "libpod") ||
			strings.Contains(l, "/run/containers/") || strings.Contains(l, "overlay-containers"):
			out = append(out, RuntimePodman)
		case strings.Contains(l, "/var/lib/docker/") || strings.Contains(l, "/docker/containers/"):
			out = append(out, RuntimeDocker)
		case mountPoint == "/" && (strings.Contains(l, "containerd") || strings.Contains(l, "/var/lib/rancher/")):
			out = append(out, RuntimeContainerd)
		}
	}
	return out
}

/* ── routes ─────────────────────────────────────────────────────────────── */

// routeGateways4 reads every IPv4 next hop out of /proc/net/route (a route
// with RTF_GATEWAY). The addresses are printed in the host's byte order.
func routeGateways4(sys System) ([]netip.Addr, error) {
	data, err := sys.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	return parseRoute4(data, sys.ByteOrder())
}

func parseRoute4(data []byte, order binary.ByteOrder) ([]netip.Addr, error) {
	var out []netip.Addr
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		f := strings.Fields(line)
		if i == 0 || len(f) < 4 {
			continue
		}
		gw, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("/proc/net/route line %d: %q is not an address", i+1, f[2])
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("/proc/net/route line %d: %q is not a flag word", i+1, f[3])
		}
		if flags&0x2 == 0 || gw == 0 {
			continue
		}
		var b [4]byte
		order.PutUint32(b[:], uint32(gw))
		out = append(out, netip.AddrFrom4(b))
	}
	return out, nil
}

// routeGateways6 reads every IPv6 next hop out of /proc/net/ipv6_route (a
// route with RTF_GATEWAY; the address is printed as 32 hex digits in network
// order). A kernel without IPv6 has no such file, and then no IPv6 network is
// a candidate either.
func routeGateways6(sys System) ([]netip.Addr, error) {
	data, err := sys.ReadFile("/proc/net/ipv6_route")
	if err != nil {
		return nil, err
	}
	return parseRoute6(data)
}

func parseRoute6(data []byte) ([]netip.Addr, error) {
	var out []netip.Addr
	for i, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		raw, err := hex.DecodeString(f[4])
		if err != nil || len(raw) != 16 {
			return nil, fmt.Errorf("/proc/net/ipv6_route line %d: %q is not an address", i+1, f[4])
		}
		flags, err := strconv.ParseUint(f[8], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("/proc/net/ipv6_route line %d: %q is not a flag word", i+1, f[8])
		}
		a := netip.AddrFrom16([16]byte(raw))
		if flags&0x2 == 0 || a.IsUnspecified() {
			continue
		}
		out = append(out, a.Unmap())
	}
	return out, nil
}

/* ── the running system ─────────────────────────────────────────────────── */

// realSystem reads the machine filex runs on. LinkKinds lives in
// auto_linux.go / auto_other.go; the rest is portable.
type realSystem struct{}

func (realSystem) GOOS() string                         { return runtime.GOOS }
func (realSystem) Getenv(key string) string             { return getenv(key) }
func (realSystem) ReadFile(name string) ([]byte, error) { return readFile(name) }
func (realSystem) ByteOrder() binary.ByteOrder          { return binary.NativeEndian }
func (realSystem) Now() time.Time                       { return time.Now() }
func (realSystem) Stat(name string) error {
	_, err := statFile(name)
	return err
}
func (realSystem) Interfaces() ([]Iface, error)          { return netInterfaces() }
func (realSystem) LinkKinds() (map[string]string, error) { return linkKinds() }

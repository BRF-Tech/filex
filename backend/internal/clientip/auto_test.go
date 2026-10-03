package clientip

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fixtures below are what real containers showed (Docker 29.5 and Podman
// 5.8.7 on a test host, kind v0.30 with kindnet), 2026-10-01: the routing tables
// byte for byte, the interfaces and their netlink kinds as `ip -d link` and
// the probe read them, the markers each runtime left. docs/DOCKER.md
// "Reverse proxies" has the measurements that decided the rules.

type fakeSys struct {
	goos    string
	files   map[string]string
	errs    map[string]error // ReadFile / Stat answer this error for the path
	rerrs   map[string]error // ReadFile alone answers this error (a file stat can see but not read)
	exists  map[string]bool  // Stat: present though not a file here (/.dockerenv, /sys/.../device)
	env     map[string]string
	ifaces  []Iface
	ifErr   error
	kinds   map[string]string
	kindErr error
}

func (f *fakeSys) GOOS() string {
	if f.goos == "" {
		return "linux"
	}
	return f.goos
}

func (f *fakeSys) ReadFile(name string) ([]byte, error) {
	if err := f.rerrs[name]; err != nil {
		return nil, err
	}
	if err := f.errs[name]; err != nil {
		return nil, err
	}
	if s, ok := f.files[name]; ok {
		return []byte(s), nil
	}
	return nil, fs.ErrNotExist
}

func (f *fakeSys) Stat(name string) error {
	if err := f.errs[name]; err != nil {
		return err
	}
	if f.exists[name] {
		return nil
	}
	if _, ok := f.files[name]; ok {
		return nil
	}
	return fs.ErrNotExist
}

func (f *fakeSys) Getenv(key string) string              { return f.env[key] }
func (f *fakeSys) Interfaces() ([]Iface, error)          { return f.ifaces, f.ifErr }
func (f *fakeSys) LinkKinds() (map[string]string, error) { return f.kinds, f.kindErr }
func (f *fakeSys) ByteOrder() binary.ByteOrder           { return binary.LittleEndian }
func (f *fakeSys) Now() time.Time                        { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }

func lo() Iface {
	return Iface{Name: "lo", Up: true, Loopback: true, Addrs: pfx("127.0.0.1/8", "::1/128")}
}

func ifc(name string, addrs ...string) Iface {
	return Iface{Name: name, Up: true, Addrs: pfx(addrs...)}
}

func pfx(list ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range list {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

const routeHeader = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"

// Docker 29 with the containerd snapshotter, private cgroup namespace.
const dockerMountinfo = `1902 209 0:58 / / rw,relatime - overlay overlay rw,lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/102335/fs,upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/102336/fs,workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/102336/work,nouserxattr
1912 1902 252:0 /var/lib/docker/containers/695d9146448f/resolv.conf /etc/resolv.conf rw,relatime - ext4 /dev/mapper/ubuntu--vg-ubuntu--lv rw
1913 1902 252:0 /var/lib/docker/containers/695d9146448f/hostname /etc/hostname rw,relatime - ext4 /dev/mapper/ubuntu--vg-ubuntu--lv rw
1914 1902 252:0 /var/lib/docker/containers/695d9146448f/hosts /etc/hosts rw,relatime - ext4 /dev/mapper/ubuntu--vg-ubuntu--lv rw
`

// The docker markers as a container on its own network saw them.
func dockerSys() *fakeSys {
	return &fakeSys{
		files: map[string]string{
			"/.dockerenv":          "",
			"/proc/1/cgroup":       "0::/\n",
			"/proc/self/cgroup":    "0::/\n",
			"/proc/self/mountinfo": dockerMountinfo,
		},
		kinds: map[string]string{"lo": ""},
	}
}

// Every environment, from its fixture: what `auto` resolves to, and which
// peers it then believes about X-Forwarded-For.
func TestAutoResolvesEveryEnvironment(t *testing.T) {
	type peer struct {
		ip      string
		trusted bool
	}
	for _, tc := range []struct {
		name     string
		sys      func() *fakeSys
		env      string
		runtime  string
		networks []string
		gws      []string
		self     []string
		warning  string
		peers    []peer
	}{
		{
			// A plain install on a Docker host: the host's own mounts name
			// docker (a container's overlay), and it is still no container.
			name: "plain install",
			sys: func() *fakeSys {
				return &fakeSys{
					files: map[string]string{
						"/proc/1/cgroup":    "0::/init.scope\n",
						"/proc/self/cgroup": "0::/system.slice/filex.service\n",
						"/proc/self/mountinfo": "22 1 252:0 / / rw,relatime - ext4 /dev/mapper/ubuntu--vg-ubuntu--lv rw\n" +
							"900 22 0:58 / /var/lib/docker/rootfs/overlayfs/abc rw - overlay overlay rw,lowerdir=/var/lib/containerd/snap/1/fs\n" +
							"901 22 0:4 net:[4026532999] /run/docker/netns/1a2b rw - nsfs nsfs rw\n",
					},
					ifaces: []Iface{lo(), ifc("wlp8s0", "192.168.1.198/24"), ifc("docker0", "172.17.0.1/16")},
					kinds:  map[string]string{"docker0": "bridge"},
				}
			},
			env: EnvPlain,
			peers: []peer{
				{"127.0.0.1", true}, {"::1", true},
				{"192.168.1.20", false}, // the LAN: a proxy on another machine is named by hand
				{"172.17.0.5", false}, {"10.0.0.5", false}, {"169.254.1.1", false}, {"fe80::1", false},
			},
		},
		{
			name: "windows",
			sys:  func() *fakeSys { return &fakeSys{goos: "windows"} },
			env:  EnvPlain,
			peers: []peer{
				{"127.0.0.1", true}, {"192.168.1.10", false},
			},
		},
		{
			// docker run on the default bridge (measured: 172.17.0.6/16, the
			// default route via 172.17.0.1).
			name: "docker, one network",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "172.17.0.6/16")}
				s.kinds["eth0"] = "veth"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			networks: []string{"172.17.0.0/16"}, gws: []string{"172.17.0.1"}, self: []string{"172.17.0.6"},
			peers: []peer{
				{"172.17.0.3", true},  // the Caddy container next to filex
				{"172.17.0.1", false}, // the gateway: docker-proxy, a host process
				{"172.17.0.6", false}, // filex itself
				{"192.168.1.20", false}, {"10.1.1.1", false}, {"127.0.0.1", true},
			},
		},
		{
			// docker network connect: two user-defined networks (measured:
			// eth0 172.26.0.2/16 with the default route, eth1 172.27.0.2/16
			// with none). The second network's gateway is known only as its
			// first host address.
			name: "docker, two networks",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"eth0\t00000000\t01001AAC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"eth0\t00001AAC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
					"eth1\t00001BAC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "172.26.0.2/16"), ifc("eth1", "172.27.0.2/16")}
				s.kinds["eth0"], s.kinds["eth1"] = "veth", "veth"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			networks: []string{"172.26.0.0/16", "172.27.0.0/16"},
			gws:      []string{"172.26.0.1", "172.27.0.1"},
			self:     []string{"172.26.0.2", "172.27.0.2"},
			peers: []peer{
				{"172.26.0.9", true}, {"172.27.0.9", true},
				{"172.26.0.1", false}, {"172.27.0.1", false},
			},
		},
		{
			// network_mode: host (measured): the host's NICs (a device behind
			// them), its bridges, the other containers' veth ends, the WARP
			// tunnel. Plus a veth that carries a subnet on the host side (a
			// VPN in a network namespace): host networking trusts none of it.
			name: "docker, host network",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"wlp8s0\t00000000\t0101A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
				s.exists = map[string]bool{"/sys/class/net/wlp8s0/device": true, "/sys/class/net/enp7s0/device": true}
				s.ifaces = []Iface{
					lo(), {Name: "enp7s0"}, ifc("wlp8s0", "192.168.1.198/24", "fe80::1/64"),
					ifc("docker0", "172.17.0.1/16"), ifc("br-11bc648a3f59", "172.26.0.1/16"),
					ifc("veth3316cf9", "fe80::744b:89ff:fe69:bc05/64"),
					ifc("veth-vpn", "10.200.1.1/24"),
					ifc("tun0", "100.64.0.6/32"),
				}
				s.kinds = map[string]string{
					"docker0": "bridge", "br-11bc648a3f59": "bridge", "veth3316cf9": "veth", "veth-vpn": "veth",
					"tun0": "tun",
				}
				return s
			},
			env: EnvHostNetwork, runtime: RuntimeDocker,
			peers: []peer{
				{"127.0.0.1", true}, {"172.17.0.5", false}, {"172.26.0.3", false},
				{"10.200.1.2", false}, {"192.168.1.20", false},
			},
		},
		{
			// docker network create --gateway 172.20.0.254: the host's bridge
			// is not the first host address, and the routing table names it.
			name: "docker, a custom gateway",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"eth0\t00000000\tFE0014AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"eth0\t000014AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "172.20.0.2/16")}
				s.kinds["eth0"] = "veth"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			networks: []string{"172.20.0.0/16"}, gws: []string{"172.20.0.1", "172.20.0.254"}, self: []string{"172.20.0.2"},
			peers: []peer{{"172.20.0.254", false}, {"172.20.0.1", false}, {"172.20.0.3", true}},
		},
		{
			// A macvlan network next to a bridge network (measured): the
			// macvlan puts the container on the LAN; the bridge is trusted.
			name: "docker, macvlan and bridge",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"eth0\t00000000\t014DA8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"eth1\t00001AAC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
					"eth0\t004DA8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "192.168.77.2/24"), ifc("eth1", "172.26.0.3/16")}
				s.kinds["eth0"], s.kinds["eth1"] = "macvlan", "veth"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			networks: []string{"172.26.0.0/16"}, gws: []string{"172.26.0.1", "192.168.77.1"}, self: []string{"172.26.0.3"},
			peers: []peer{
				{"192.168.77.50", false}, {"172.26.0.9", true},
			},
		},
		{
			// ipvlan only (measured): on the LAN, nothing but loopback.
			name: "docker, ipvlan",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"eth0\t00000000\t014EA8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"eth0\t004EA8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "192.168.78.2/24")}
				s.kinds["eth0"] = "ipvlan"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			peers: []peer{{"192.168.78.9", false}, {"127.0.0.1", true}},
		},
		{
			// An IPv6-enabled network (measured: --ipv6 --subnet
			// fd00:dead:beef::/64): the ULA counts like the IPv4 subnet, minus
			// its gateway; the link-local range never.
			name: "docker, IPv6 ULA and link-local",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader +
					"eth0\t00000000\t01001CAC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
					"eth0\t00001CAC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
				s.files["/proc/net/ipv6_route"] = "" +
					"fd00deadbeef00000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000003 00000000 00000001     eth0\n" +
					"fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001     eth0\n" +
					"00000000000000000000000000000000 00 00000000000000000000000000000000 00 fd00deadbeef00000000000000000001 00000400 00000001 00000000 00000003     eth0\n" +
					"00000000000000000000000000000001 80 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000004 00000000 80200001       lo\n" +
					"00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200       lo\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "172.28.0.2/16", "fd00:dead:beef::2/64", "fe80::f441:4fff:fe3e:3709/64")}
				s.kinds["eth0"] = "veth"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			networks: []string{"172.28.0.0/16", "fd00:dead:beef::/64"},
			gws:      []string{"172.28.0.1", "fd00:dead:beef::1"},
			self:     []string{"172.28.0.2", "fd00:dead:beef::2"},
			peers: []peer{
				{"fd00:dead:beef::4", true}, {"fd00:dead:beef::1", false}, {"fd00:dead:beef::2", false},
				{"fe80::1", false}, {"fe80::f441:4fff:fe3e:3709", false},
				{"::ffff:172.28.0.4", true}, {"::ffff:172.28.0.1", false}, {"::1", true},
				{"fd00:beef::1", false},
			},
		},
		{
			// docker run --network none: loopback alone.
			name: "docker, no network",
			sys: func() *fakeSys {
				s := dockerSys()
				s.ifaces = []Iface{lo()}
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			peers: []peer{{"127.0.0.1", true}, {"172.17.0.1", false}},
		},
		{
			// kind v0.30, kindnet (measured): the pod's /24, reached through
			// the node at .1 (a route for the /24 via .1 as well).
			name: "kubernetes, kindnet /24",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1"},
					files: map[string]string{
						"/proc/1/cgroup":    "0::/\n",
						"/proc/self/cgroup": "0::/\n",
						"/proc/self/mountinfo": "3977 3861 0:1049 / / rw,relatime - overlay overlay rw,lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/131/fs\n" +
							"3986 3977 252:0 /var/lib/docker/volumes/97d8/_data/lib/kubelet/pods/553d3c13/etc-hosts /etc/hosts rw,relatime - ext4 /dev/mapper/ubuntu--vg-ubuntu--lv rw\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t0100F40A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"eth0\t0000F40A\t0100F40A\t0003\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
							"eth0\t0100F40A\t00000000\t0005\t0\t0\t0\tFFFFFFFF\t0\t0\t0\n",
						"/proc/net/ipv6_route": "",
					},
					ifaces: []Iface{lo(), ifc("eth0", "10.244.0.7/24", "fe80::80aa:fcff:fe1c:a572/64")},
					kinds:  map[string]string{"eth0": "veth"},
				}
			},
			env: EnvKubernetes, runtime: RuntimeKubernetes,
			networks: []string{"10.244.0.0/24"}, gws: []string{"10.244.0.1"}, self: []string{"10.244.0.7"},
			peers: []peer{
				{"10.244.0.8", true},  // a pod on the same node (measured: its own address)
				{"10.244.0.1", false}, // the node (measured: node -> pod arrives from .1)
				{"10.244.1.5", false}, // an ingress controller on another node
			},
		},
		{
			// flannel: the node's /24 behind the cni0 bridge at .1, the
			// cluster's /16 through it too.
			name: "kubernetes, flannel /24",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.43.0.1"},
					files: map[string]string{
						"/proc/1/cgroup":    "0::/kubepods/besteffort/pod1234/abcd\n",
						"/proc/self/cgroup": "0::/kubepods/besteffort/pod1234/abcd\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t0101F40A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"eth0\t0000F40A\t0101F40A\t0003\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
							"eth0\t0001F40A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("eth0", "10.244.1.5/24")},
					kinds:  map[string]string{"eth0": "veth"},
				}
			},
			env: EnvKubernetes, runtime: RuntimeKubernetes,
			networks: []string{"10.244.1.0/24"}, gws: []string{"10.244.1.1"}, self: []string{"10.244.1.5"},
			peers: []peer{{"10.244.1.9", true}, {"10.244.1.1", false}, {"10.244.2.7", false}},
		},
		{
			// Calico: a /32 and a link-local gateway - the pod trusts nothing
			// but loopback (the /32 is itself).
			name: "kubernetes, calico /32",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1"},
					files: map[string]string{
						"/proc/1/cgroup":    "0::/\n",
						"/proc/self/cgroup": "0::/\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t0101FEA9\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"eth0\t0101FEA9\t00000000\t0005\t0\t0\t0\tFFFFFFFF\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("eth0", "10.1.2.3/32")},
					kinds:  map[string]string{"eth0": "veth"},
				}
			},
			env: EnvKubernetes, runtime: RuntimeKubernetes,
			networks: []string{"10.1.2.3/32"}, gws: []string{"169.254.1.1"}, self: []string{"10.1.2.3"},
			peers: []peer{{"10.1.2.3", false}, {"10.1.2.9", false}, {"169.254.1.1", false}, {"127.0.0.1", true}},
		},
		{
			// Podman 5.8 rootless, pasta (measured): a tun interface that
			// copies the host's name and address; a connection from the
			// host's loopback arrives from that copied address.
			name: "podman rootless, pasta",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"container": "podman"},
					files: map[string]string{
						"/run/.containerenv": "",
						"/proc/1/cgroup":     "0::/\n",
						"/proc/self/cgroup":  "0::/\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("eth0", "172.17.0.6/16", "fe80::1425:16ff:fef4:4ff/64")},
					kinds:  map[string]string{"eth0": "tun"},
				}
			},
			env: EnvContainer, runtime: RuntimePodman,
			peers: []peer{{"172.17.0.6", false}, {"172.17.0.1", false}, {"172.17.0.9", false}, {"127.0.0.1", true}},
		},
		{
			// Podman rootless, slirp4netns (measured): tap0 10.0.2.100/24,
			// gateway 10.0.2.2; the rootlesskit port handler relays every
			// published-port connection from 10.0.2.100.
			name: "podman rootless, slirp4netns",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"container": "podman"},
					files: map[string]string{
						"/run/.containerenv": "",
						"/proc/1/cgroup":     "0::/\n",
						"/proc/self/cgroup":  "0::/\n",
						"/proc/net/route": routeHeader +
							"tap0\t00000000\t0202000A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"tap0\t0002000A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("tap0", "10.0.2.100/24", "fd00::f4e5:3dff:fe21:276b/64")},
					kinds:  map[string]string{"tap0": "tun"},
				}
			},
			env: EnvContainer, runtime: RuntimePodman,
			peers: []peer{{"10.0.2.100", false}, {"10.0.2.2", false}, {"10.0.2.9", false}},
		},
		{
			// Podman rootless on a user-defined network (measured): a veth into
			// the rootless bridge - and rootlessport relays every
			// published-port connection, from the internet too, from the
			// container's OWN address (10.89.0.5 here).
			name: "podman rootless, user-defined network",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"container": "podman"},
					files: map[string]string{
						"/run/.containerenv": "",
						"/proc/1/cgroup":     "0::/\n",
						"/proc/self/cgroup":  "0::/\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t0100590A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"eth0\t0000590A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("eth0", "10.89.0.5/24", "fe80::b0c8:f7ff:fe0d:68c/64")},
					kinds:  map[string]string{"eth0": "veth"},
				}
			},
			env: EnvContainer, runtime: RuntimePodman,
			networks: []string{"10.89.0.0/24"}, gws: []string{"10.89.0.1"}, self: []string{"10.89.0.5"},
			peers: []peer{{"10.89.0.5", false}, {"10.89.0.1", false}, {"10.89.0.7", true}},
		},
		{
			// Podman rootful, netavark bridge (measured: 10.88.0.2/16; a
			// published port on 127.0.0.1 and a direct dial both arrive from
			// 10.88.0.1).
			name: "podman rootful, bridge",
			sys: func() *fakeSys {
				return &fakeSys{
					env: map[string]string{"container": "podman"},
					files: map[string]string{
						"/run/.containerenv": "",
						"/proc/1/cgroup":     "0::/\n",
						"/proc/self/cgroup":  "0::/\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t0100580A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
							"eth0\t0000580A\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("eth0", "10.88.0.2/16", "fe80::602d:efff:fe03:955b/64")},
					kinds:  map[string]string{"eth0": "veth"},
				}
			},
			env: EnvContainer, runtime: RuntimePodman,
			networks: []string{"10.88.0.0/16"}, gws: []string{"10.88.0.1"}, self: []string{"10.88.0.2"},
			peers: []peer{{"10.88.0.1", false}, {"10.88.0.3", true}},
		},
		{
			// A privileged rootless Podman container says rootless=1: loopback
			// only, whatever its interfaces.
			name: "podman rootless, said so",
			sys: func() *fakeSys {
				return &fakeSys{
					files: map[string]string{
						"/run/.containerenv": "engine=\"podman-5.8.7\"\nname=\"x\"\nrootless=1\n",
						"/proc/1/cgroup":     "0::/\n",
						"/proc/self/cgroup":  "0::/\n",
						"/proc/net/route": routeHeader +
							"eth0\t00000000\t0100590A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n",
					},
					ifaces: []Iface{lo(), ifc("eth0", "10.89.0.5/24")},
					kinds:  map[string]string{"eth0": "veth"},
				}
			},
			env: EnvPodmanRootless, runtime: RuntimePodman,
			peers: []peer{{"10.89.0.7", false}, {"127.0.0.1", true}},
		},
		{
			// Docker-in-Docker: a bridge of the inner daemon next to the
			// container's own network, no NIC to tell them apart.
			name: "ambiguous: a bridge next to a container network",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader + "eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
				s.ifaces = []Iface{lo(), ifc("eth0", "172.17.0.2/16"), ifc("docker0", "172.18.0.1/16")}
				s.kinds["eth0"], s.kinds["docker0"] = "veth", "bridge"
				return s
			},
			env: EnvUnknown, runtime: RuntimeDocker, warning: WarnAmbiguous,
			peers: []peer{{"172.17.0.3", false}, {"127.0.0.1", true}},
		},
		{
			name: "unreadable: the interfaces' kinds",
			sys: func() *fakeSys {
				s := dockerSys()
				s.ifaces = []Iface{lo(), ifc("eth0", "172.17.0.6/16")}
				s.kinds, s.kindErr = nil, errors.New("netlink: operation not permitted")
				return s
			},
			env: EnvUnknown, runtime: RuntimeDocker, warning: WarnUnreadable,
			peers: []peer{{"172.17.0.3", false}, {"127.0.0.1", true}},
		},
		{
			name: "unreadable: the interfaces",
			sys: func() *fakeSys {
				s := dockerSys()
				s.ifErr = errors.New("route ip+net: netlinkrib: permission denied")
				return s
			},
			env: EnvUnknown, runtime: RuntimeDocker, warning: WarnUnreadable,
			peers: []peer{{"172.17.0.3", false}},
		},
		{
			name: "unreadable: the routing table",
			sys: func() *fakeSys {
				s := dockerSys()
				s.errs = map[string]error{"/proc/net/route": fs.ErrPermission}
				s.ifaces = []Iface{lo(), ifc("eth0", "172.17.0.6/16")}
				s.kinds["eth0"] = "veth"
				return s
			},
			env: EnvUnknown, runtime: RuntimeDocker, warning: WarnUnreadable,
			peers: []peer{{"172.17.0.3", false}, {"172.17.0.1", false}},
		},
		{
			name: "unreadable: the IPv6 routing table, with an IPv6 network",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader + "eth0\t00000000\t01001CAC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
				s.errs = map[string]error{"/proc/net/ipv6_route": fs.ErrPermission}
				s.ifaces = []Iface{lo(), ifc("eth0", "172.28.0.2/16", "fd00:dead:beef::2/64")}
				s.kinds["eth0"] = "veth"
				return s
			},
			env: EnvUnknown, runtime: RuntimeDocker, warning: WarnUnreadable,
			peers: []peer{{"172.28.0.3", false}, {"fd00:dead:beef::4", false}},
		},
		{
			name: "unreadable: whether an interface is physical",
			sys: func() *fakeSys {
				s := dockerSys()
				s.errs = map[string]error{"/sys/class/net/eth0/device": fs.ErrPermission}
				s.ifaces = []Iface{lo(), ifc("eth0", "172.17.0.6/16")}
				return s
			},
			env: EnvUnknown, runtime: RuntimeDocker, warning: WarnUnreadable,
			peers: []peer{{"172.17.0.3", false}},
		},
		{
			// Nothing under /proc could be read and no marker file exists:
			// plain, and it says it could not tell.
			name: "unreadable: /proc",
			sys: func() *fakeSys {
				return &fakeSys{errs: map[string]error{
					"/proc/1/cgroup": fs.ErrPermission, "/proc/self/cgroup": fs.ErrPermission,
					"/proc/self/mountinfo": fs.ErrPermission,
				}}
			},
			env: EnvPlain, warning: WarnUnreadable,
			peers: []peer{{"127.0.0.1", true}, {"172.17.0.3", false}},
		},
		{
			// A veth that is down, and one with only link-local addresses.
			name: "docker, a veth down and one link-local only",
			sys: func() *fakeSys {
				s := dockerSys()
				s.files["/proc/net/route"] = routeHeader
				s.ifaces = []Iface{lo(), {Name: "eth0", Addrs: pfx("172.30.0.2/16")}, ifc("eth1", "fe80::1/64", "169.254.3.4/16")}
				s.kinds["eth0"], s.kinds["eth1"] = "veth", "veth"
				return s
			},
			env: EnvContainer, runtime: RuntimeDocker,
			peers: []peer{{"172.30.0.3", false}, {"169.254.3.5", false}, {"fe80::2", false}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := ResolveAuto(tc.sys())
			require.Equal(t, tc.env, res.Environment, "%+v", res)
			require.Equal(t, tc.runtime, res.Runtime)
			require.Equal(t, orEmpty(tc.networks), res.Networks)
			require.Equal(t, orEmpty(tc.gws), res.ExcludedGateways)
			require.Equal(t, orEmpty(tc.self), res.ExcludedSelf)
			require.Equal(t, tc.warning, res.Warning, res.WarningDetail)
			if tc.warning != "" {
				require.NotEmpty(t, res.WarningDetail, "a warning says what could not be told")
				require.Empty(t, res.Networks, "a warning means loopback only")
			}

			restore := SetAutoResolver(func() AutoResolution { return res })
			defer restore()
			for _, p := range tc.peers {
				r := req(netip.MustParseAddrPort(addrPort(p.ip)).String(), map[string]string{"X-Forwarded-For": "198.51.100.77"})
				want := netip.MustParseAddr(p.ip).Unmap().String()
				if p.trusted {
					want = "198.51.100.77"
				}
				require.Equal(t, want, FromRequest(r), "peer %s", p.ip)
			}
		})
	}
}

func addrPort(ip string) string {
	if strings.Contains(ip, ":") {
		return "[" + ip + "]:41000"
	}
	return ip + ":41000"
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// The interfaces a resolution shows are its "why": each with its kind, what
// became of it and why.
func TestAutoExplainsEachInterface(t *testing.T) {
	s := dockerSys()
	s.files["/proc/net/route"] = routeHeader + "eth0\t00000000\t014DA8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	s.ifaces = []Iface{lo(), ifc("eth1", "172.26.0.3/16"), ifc("eth0", "192.168.77.2/24"), ifc("tunl0"), ifc("wg0", "10.9.0.2/24")}
	s.kinds = map[string]string{"eth0": "macvlan", "eth1": "veth", "tunl0": "ipip", "wg0": "wireguard"}
	res := ResolveAuto(s)
	require.Equal(t, []AutoInterface{
		{Name: "eth0", Kind: "macvlan", Addresses: []string{"192.168.77.2/24"}, Reason: ReasonLAN},
		{Name: "eth1", Kind: "veth", Addresses: []string{"172.26.0.3/16"}, Trusted: true, Reason: ReasonContainerNetwork},
		{Name: "wg0", Kind: "wireguard", Addresses: []string{"10.9.0.2/24"}, Reason: ReasonOther},
	}, res.Interfaces, "sorted by name; an addressless tunl0 says nothing and is left out")

	// Host networking lists the host's NICs and bridges, not the other
	// containers' veth ends a Docker host is full of.
	h := dockerSys()
	h.exists = map[string]bool{"/sys/class/net/wlp8s0/device": true}
	h.ifaces = []Iface{lo(), ifc("wlp8s0", "192.168.1.198/24"), ifc("docker0", "172.17.0.1/16"), ifc("veth1", "fe80::1/64"), ifc("veth2", "fe80::2/64")}
	h.kinds = map[string]string{"docker0": "bridge", "veth1": "veth", "veth2": "veth"}
	res = ResolveAuto(h)
	require.Equal(t, EnvHostNetwork, res.Environment)
	var names []string
	for _, i := range res.Interfaces {
		names = append(names, i.Name+"="+i.Kind+"/"+i.Reason)
	}
	require.Equal(t, []string{"docker0=bridge/host", "wlp8s0=physical/host"}, names)
}

// The markers each runtime leaves, and the ones a plain host shows that must
// not be mistaken for them.
func TestContainerMarkers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sys      *fakeSys
		found    bool
		runtime  string
		rootless bool
	}{
		{"nothing", &fakeSys{files: map[string]string{"/proc/1/cgroup": "0::/init.scope\n", "/proc/self/cgroup": "0::/user.slice\n", "/proc/self/mountinfo": "22 1 8:1 / / rw - ext4 /dev/sda1 rw\n"}}, false, "", false},
		{"/.dockerenv", &fakeSys{exists: map[string]bool{"/.dockerenv": true}}, true, RuntimeDocker, false},
		{"cgroup v1 docker", &fakeSys{files: map[string]string{"/proc/1/cgroup": "12:memory:/docker/0123abcd\n"}}, true, RuntimeDocker, false},
		{"cgroup v2 host namespace", &fakeSys{files: map[string]string{"/proc/self/cgroup": "0::/system.slice/docker-0123abcd.scope\n"}}, true, RuntimeDocker, false},
		{"docker mountinfo only", &fakeSys{files: map[string]string{"/proc/self/mountinfo": dockerMountinfo}}, true, RuntimeDocker, false},
		{"containerd root overlay only", &fakeSys{files: map[string]string{"/proc/self/mountinfo": "1 0 0:58 / / rw - overlay overlay rw,lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/1/fs\n"}}, true, RuntimeContainerd, false},
		{"kubepods cgroup", &fakeSys{files: map[string]string{"/proc/1/cgroup": "0::/kubepods.slice/kubepods-burstable.slice/x\n"}}, true, RuntimeKubernetes, false},
		{"KUBERNETES_SERVICE_HOST beats docker", &fakeSys{env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1"}, exists: map[string]bool{"/.dockerenv": true}}, true, RuntimeKubernetes, false},
		{"container=podman", &fakeSys{env: map[string]string{"container": "podman"}}, true, RuntimePodman, false},
		{"libpod cgroup", &fakeSys{files: map[string]string{"/proc/self/cgroup": "0::/machine.slice/libpod-0123.scope/container\n"}}, true, RuntimePodman, false},
		{"podman storage mount", &fakeSys{files: map[string]string{"/proc/self/mountinfo": "5 1 0:4 /containers/storage/overlay-containers/0123/userdata/hosts /etc/hosts rw - tmpfs tmpfs rw\n"}}, true, RuntimePodman, false},
		{"containerenv rootless=1", &fakeSys{files: map[string]string{"/run/.containerenv": "engine=\"podman-5.8.7\"\nrootless=1\n"}}, true, RuntimePodman, true},
		{"containerenv rootless=0", &fakeSys{files: map[string]string{"/run/.containerenv": "rootless=0\n"}}, true, RuntimePodman, false},
		{"containerenv empty (measured: unprivileged Podman 5.8)", &fakeSys{files: map[string]string{"/run/.containerenv": ""}}, true, RuntimePodman, false},
		{"container=oci", &fakeSys{env: map[string]string{"container": "oci"}}, true, RuntimeOther, false},
		{"container=lxc is a whole system", &fakeSys{env: map[string]string{"container": "lxc"}}, false, "", false},
		{"docker overlay elsewhere on a plain host", &fakeSys{files: map[string]string{"/proc/self/mountinfo": "22 1 8:1 / / rw - ext4 /dev/sda1 rw\n900 22 0:58 / /var/lib/docker/overlay2/abc/merged rw - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/X\n"}}, false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := detectContainer(tc.sys)
			require.Equal(t, tc.found, m.found)
			require.Equal(t, tc.runtime, m.runtime)
			require.Equal(t, tc.rootless, m.rootless)
		})
	}
	// /run/.containerenv that exists but cannot be read is still Podman.
	m, err := detectContainer(&fakeSys{rerrs: map[string]error{"/run/.containerenv": fs.ErrPermission}, exists: map[string]bool{"/run/.containerenv": true}})
	require.Error(t, err)
	require.True(t, m.found)
	require.Equal(t, RuntimePodman, m.runtime)
}

func TestRouteParsing(t *testing.T) {
	gws, err := parseRoute4([]byte(routeHeader+
		"eth0\t00000000\t010011AC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"+
		"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"+
		"eth1\t0000F40A\t0100F40A\t0003\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"), binary.LittleEndian)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("172.17.0.1"), netip.MustParseAddr("10.244.0.1")}, gws)

	// A big-endian host prints the same address the other way round.
	gws, err = parseRoute4([]byte(routeHeader+"eth0\t00000000\tAC110001\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"), binary.BigEndian)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("172.17.0.1")}, gws)

	_, err = parseRoute4([]byte(routeHeader+"eth0\t00000000\tZZ\t0003\n"), binary.LittleEndian)
	require.Error(t, err, "a table that does not read is an error, never an empty answer")

	gws6, err := parseRoute6([]byte("00000000000000000000000000000000 00 00000000000000000000000000000000 00 fd00deadbeef00000000000000000001 00000400 00000001 00000000 00000003     eth0\n" +
		"00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200       lo\n"))
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("fd00:dead:beef::1")}, gws6)
	_, err = parseRoute6([]byte("00000000000000000000000000000000 00 00000000000000000000000000000000 00 nothex 00000400 00000001 00000000 00000003 eth0\n"))
	require.Error(t, err)
}

func TestFirstHost(t *testing.T) {
	for _, tc := range []struct{ p, want string }{
		{"172.18.0.0/16", "172.18.0.1"}, {"10.244.1.0/24", "10.244.1.1"}, {"10.0.0.0/30", "10.0.0.1"},
		{"fd00:dead:beef::/64", "fd00:dead:beef::1"},
		{"10.1.2.3/32", ""}, {"10.0.0.0/31", ""}, {"fd00::1/128", ""}, {"fd00::/127", ""},
	} {
		g, ok := firstHost(netip.MustParsePrefix(tc.p))
		if tc.want == "" {
			require.False(t, ok, tc.p)
			continue
		}
		require.Equal(t, tc.want, g.String(), tc.p)
	}
}

// `auto` is a word of the list like the classes: on its own, next to them,
// next to addresses - and what is carved out of it stays carved out only of
// IT: an address named by hand is trusted even when it is the gateway (the
// page's "trust it" for a proxy on the host).
func TestTheAutoWord(t *testing.T) {
	restore := SetAutoResolver(func() AutoResolution {
		return AutoResolution{Environment: EnvContainer, Networks: []string{"172.18.0.0/16"}, ExcludedGateways: []string{"172.18.0.1"}, ExcludedSelf: []string{"172.18.0.5"}}
	})
	t.Cleanup(restore)

	s, err := ParseList("AUTO")
	require.NoError(t, err)
	require.Equal(t, Classes{Auto: true}, s.Classes())
	require.Equal(t, []string{"auto"}, s.Strings())
	require.Equal(t, []string{"loopback", "172.18.0.0/16"}, s.Effective())
	require.True(t, s.ContainsString("172.18.0.9"))
	require.False(t, s.ContainsString("172.18.0.1"))
	require.False(t, s.ContainsString("172.18.0.5"))

	s, err = ParseList("auto, 172.18.0.1, 203.0.113.0/24, loopback")
	require.NoError(t, err)
	require.Equal(t, []string{"auto", "loopback", "172.18.0.1", "203.0.113.0/24"}, s.Strings())
	require.Equal(t, []string{"loopback", "172.18.0.0/16", "172.18.0.1", "203.0.113.0/24"}, s.Effective(), "loopback once")
	require.True(t, s.ContainsString("172.18.0.1"), "named by hand, the gateway is trusted")
	require.True(t, s.ContainsString("203.0.113.9"))

	s, err = ParseList("auto private")
	require.NoError(t, err)
	require.Equal(t, []string{"loopback", "private", "172.18.0.0/16"}, s.Effective())

	def := DefaultSet()
	require.Equal(t, Classes{Auto: true}, def.Classes())
	require.Equal(t, []string{"auto"}, def.Strings())
	require.Equal(t, []string{"loopback", "172.18.0.0/16"}, def.Effective())

	// A list without the word takes nothing from `auto`.
	s, err = ParseList("loopback")
	require.NoError(t, err)
	require.False(t, s.ContainsString("172.18.0.9"))
	require.Equal(t, []string{"loopback"}, s.Effective())
}

// With nothing configured the trusted set is `auto`, and it follows `auto`
// as it is worked out again (a network joined at run time).
func TestTheDefaultFollowsTheAutomaticSet(t *testing.T) {
	t.Cleanup(SetSource(nil))
	answer := AutoResolution{Environment: EnvContainer, Networks: []string{"172.18.0.0/16"}, ExcludedGateways: []string{"172.18.0.1"}}
	restore := SetAutoResolver(func() AutoResolution { return answer })
	t.Cleanup(restore)

	fwd := map[string]string{"X-Forwarded-For": "198.51.100.7"}
	require.Equal(t, "198.51.100.7", FromRequest(req("172.18.0.3:1", fwd)))
	require.Equal(t, "172.19.0.3", FromRequest(req("172.19.0.3:1", fwd)))

	// docker network connect: the next resolution has the second network.
	answer.Networks = append(answer.Networks, "172.19.0.0/16")
	answer.ExcludedGateways = append(answer.ExcludedGateways, "172.19.0.1")
	RefreshAuto()
	require.Equal(t, "198.51.100.7", FromRequest(req("172.19.0.3:1", fwd)))
	require.Equal(t, "172.19.0.1", FromRequest(req("172.19.0.1:1", fwd)))
}

// A request from the gateway - what docker-proxy relays from a published
// port, what a host process dials - carries the address its sender wrote. It
// is not believed. (Before 0.50 the default trusted every private address,
// the gateway included.)
func TestTheGatewayIsNotBelieved(t *testing.T) {
	t.Cleanup(SetSource(nil))
	restore := SetAutoResolver(func() AutoResolution {
		s := dockerSys()
		s.files["/proc/net/route"] = routeHeader +
			"eth0\t00000000\t01001AAC\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
			"eth0\t00001AAC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
		s.ifaces = []Iface{lo(), ifc("eth0", "172.26.0.4/16")}
		s.kinds["eth0"] = "veth"
		return ResolveAuto(s)
	})
	t.Cleanup(restore)
	fwd := map[string]string{"X-Forwarded-For": "198.51.100.7", "X-Real-IP": "198.51.100.8"}
	require.Equal(t, "172.26.0.1", FromRequest(req("172.26.0.1:37726", fwd)), "the gateway")
	require.Equal(t, "172.26.0.4", FromRequest(req("172.26.0.4:41000", fwd)), "filex's own address")
	require.Equal(t, "192.168.1.107", FromRequest(req("192.168.1.107:51313", fwd)), "a LAN client through the published port (measured: its own address arrives)")
	require.Equal(t, "198.51.100.7", FromRequest(req("172.26.0.5:45108", fwd)), "the proxy container on the network")
	require.Equal(t, "198.51.100.7", FromRequest(req("[::ffff:172.26.0.5]:45108", fwd)), "the same, IPv4-mapped")
	require.Equal(t, "172.26.0.1", FromRequest(req("[::ffff:172.26.0.1]:45108", fwd)), "the gateway, IPv4-mapped")
}

// The first answer is logged, an unchanged one is not (it runs every minute),
// a changed one is - and a warning is said once, not every minute.
func TestRefreshAutoLogsOnlyChanges(t *testing.T) {
	logs := captureLogs(t)
	answer := AutoResolution{Environment: EnvContainer, Networks: []string{"172.18.0.0/16"}}
	restore := SetAutoResolver(func() AutoResolution { return answer })
	t.Cleanup(restore)
	autoMu.Lock()
	autoLogged = false
	autoMu.Unlock()

	RefreshAuto()
	RefreshAuto()
	RefreshAuto()
	require.Equal(t, 1, logs.count("auto) resolved"), logs.text())
	require.Zero(t, logs.count("auto) changed"))

	answer.Networks = []string{"172.18.0.0/16", "172.19.0.0/16"}
	RefreshAuto()
	RefreshAuto()
	require.Equal(t, 1, logs.count("auto) changed"))

	answer = AutoResolution{Environment: EnvUnknown, Warning: WarnUnreadable, WarningDetail: "netlink: denied"}
	RefreshAuto()
	RefreshAuto()
	RefreshAuto()
	require.Equal(t, 1, logs.count("could not tell where filex runs"), "the warning once")
}

// On the machine running the tests: the real system resolves without error
// (a developer's machine, a CI container) and never trusts a network it
// cannot justify.
func TestAutoOnThisMachine(t *testing.T) {
	res := ResolveAuto(realSystem{})
	require.NotEmpty(t, res.Environment)
	t.Logf("this machine: %+v", res)
	if res.Environment == EnvPlain || res.Environment == EnvHostNetwork {
		require.Empty(t, res.Networks)
	}
}

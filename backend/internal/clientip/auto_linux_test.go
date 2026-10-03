//go:build linux

package clientip

import (
	"encoding/binary"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// rtattr builds one netlink attribute (length, type, value, padding to 4).
func rtattr(typ uint16, val []byte) []byte {
	l := 4 + len(val)
	b := make([]byte, (l+3)&^3)
	binary.NativeEndian.PutUint16(b[0:2], uint16(l))
	binary.NativeEndian.PutUint16(b[2:4], typ)
	copy(b[4:], val)
	return b
}

// newlink builds an RTM_NEWLINK message: ifinfomsg, then IFLA_IFNAME and,
// when kind is set, IFLA_LINKINFO { IFLA_INFO_KIND } - the shape `ip -d link`
// reads.
func newlink(name, kind string) []byte {
	body := make([]byte, syscall.SizeofIfInfomsg)
	body = append(body, rtattr(iflaIfname, append([]byte(name), 0))...)
	if kind != "" {
		info := rtattr(iflaInfoKind, append([]byte(kind), 0))
		info = append(info, rtattr(2, []byte{1, 2, 3, 4})...) // IFLA_INFO_DATA, ignored
		body = append(body, rtattr(iflaLinkinfo, info)...)
	}
	hdr := make([]byte, syscall.NLMSG_HDRLEN)
	binary.NativeEndian.PutUint32(hdr[0:4], uint32(len(hdr)+len(body)))
	binary.NativeEndian.PutUint16(hdr[4:6], syscall.RTM_NEWLINK)
	return append(hdr, body...)
}

func TestParseLinkKinds(t *testing.T) {
	var raw []byte
	for _, l := range [][2]string{{"lo", ""}, {"eth0", "veth"}, {"eth1", "macvlan"}, {"eth2", "ipvlan"}, {"tap0", "tun"}, {"enp7s0", ""}} {
		raw = append(raw, newlink(l[0], l[1])...)
	}
	msgs, err := syscall.ParseNetlinkMessage(raw)
	require.NoError(t, err)
	kinds, err := parseLinkKinds(msgs)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"lo": "", "eth0": "veth", "eth1": "macvlan", "eth2": "ipvlan", "tap0": "tun", "enp7s0": ""}, kinds)

	require.Equal(t, "", nestedString([]byte{1, 0}, iflaInfoKind), "a short block reads as nothing")
	require.Equal(t, "", nestedString([]byte{200, 0, 1, 0, 'v'}, iflaInfoKind), "a length past the end reads as nothing")
}

// The real dump on the machine running the tests: every interface Go lists is
// in it, loopback without a kind.
func TestLinkKindsOnThisMachine(t *testing.T) {
	kinds, err := linkKinds()
	require.NoError(t, err)
	ifaces, err := netInterfaces()
	require.NoError(t, err)
	for _, i := range ifaces {
		_, ok := kinds[i.Name]
		require.True(t, ok, "%s is missing from the netlink dump", i.Name)
		if i.Loopback {
			require.Empty(t, kinds[i.Name])
		}
	}
}

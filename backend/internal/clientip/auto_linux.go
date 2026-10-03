//go:build linux

package clientip

import (
	"encoding/binary"
	"strings"
	"syscall"
)

// linkKinds asks the kernel for every interface's link kind (RTM_GETLINK,
// IFLA_LINKINFO -> IFLA_INFO_KIND): veth, macvlan, ipvlan, tun, bridge, ...
//
// ⚠ sysfs cannot tell them apart. Measured on Docker 29: a veth, a macvlan
// and an ipvlan interface show the same /sys/class/net/<if>/ entries and the
// same uevent (INTERFACE=, IFINDEX=, no DEVTYPE); only netlink says which is
// which. The dump needs no privilege (it is what `ip -d link` reads).
func linkKinds() (map[string]string, error) {
	tab, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	msgs, err := syscall.ParseNetlinkMessage(tab)
	if err != nil {
		return nil, err
	}
	return parseLinkKinds(msgs)
}

const (
	iflaIfname   = 3  // IFLA_IFNAME
	iflaLinkinfo = 18 // IFLA_LINKINFO
	iflaInfoKind = 1  // IFLA_INFO_KIND
	nlaTypeMask  = 0x3fff
)

// parseLinkKinds reads the name and the kind out of each RTM_NEWLINK message.
// An interface with no IFLA_LINKINFO (a physical NIC, loopback) maps to "".
func parseLinkKinds(msgs []syscall.NetlinkMessage) (map[string]string, error) {
	out := map[string]string{}
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type == syscall.NLMSG_DONE {
			break
		}
		if m.Header.Type != syscall.RTM_NEWLINK {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			return nil, err
		}
		name, kind := "", ""
		for _, a := range attrs {
			switch a.Attr.Type & nlaTypeMask {
			case iflaIfname:
				name = strings.TrimRight(string(a.Value), "\x00")
			case iflaLinkinfo:
				kind = nestedString(a.Value, iflaInfoKind)
			}
		}
		if name != "" {
			out[name] = kind
		}
	}
	return out, nil
}

// nestedString finds attribute typ inside a nested attribute block and reads
// it as a NUL-terminated string.
func nestedString(b []byte, typ uint16) string {
	for len(b) >= 4 {
		l := int(binary.NativeEndian.Uint16(b[0:2]))
		t := binary.NativeEndian.Uint16(b[2:4]) & nlaTypeMask
		if l < 4 || l > len(b) {
			return ""
		}
		if t == typ {
			return strings.TrimRight(string(b[4:l]), "\x00")
		}
		next := (l + 3) &^ 3
		if next > len(b) {
			return ""
		}
		b = b[next:]
	}
	return ""
}

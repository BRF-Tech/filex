package clientip

import (
	"net"
	"net/netip"
	"os"
)

// The portable half of realSystem (auto.go). LinkKinds is per platform:
// auto_linux.go asks the kernel over netlink; elsewhere there are no
// containers to look for and ResolveAuto never asks.

var (
	getenv   = os.Getenv
	readFile = os.ReadFile
	statFile = os.Stat
)

// netInterfaces lists the interfaces with their addresses and prefix lengths.
func netInterfaces() ([]Iface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Iface, 0, len(list))
	for _, ifc := range list {
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, err
		}
		v := Iface{Name: ifc.Name, Up: ifc.Flags&net.FlagUp != 0, Loopback: ifc.Flags&net.FlagLoopback != 0}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ones, bits := ipn.Mask.Size()
			if ip.Is4In6() {
				ip = ip.Unmap()
				if bits == 128 {
					ones -= 96
				}
			}
			if ones < 0 || ones > ip.BitLen() {
				continue
			}
			v.Addrs = append(v.Addrs, netip.PrefixFrom(ip.WithZone(""), ones))
		}
		out = append(out, v)
	}
	return out, nil
}

//go:build darwin

package sysmem

import "golang.org/x/sys/unix"

func cgroupLimit() (uint64, string) { return 0, "" }

// hostTotal is hw.memsize.
func hostTotal() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}

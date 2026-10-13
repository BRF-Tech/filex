//go:build windows

package diskfree

import (
	"strings"

	"golang.org/x/sys/windows"
)

// measure reads GetDiskFreeSpaceEx. Its first out-parameter (Free) honours
// per-user quotas, which is the number that matters to us; Used is the
// volume's own, its size minus every free byte.
func measure(dir string) (Usage, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return Usage{}, err
	}
	var avail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &totalFree); err != nil {
		return Usage{}, err
	}
	u := Usage{Total: total, Free: avail}
	if totalFree < total {
		u.Used = total - totalFree
	}
	// The volume's mount path ("c:\", or the folder a volume is mounted on).
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if windows.GetVolumePathName(p, &buf[0], uint32(len(buf))) == nil {
		u.Volume = "vol:" + strings.ToLower(windows.UTF16ToString(buf))
	}
	return u, nil
}

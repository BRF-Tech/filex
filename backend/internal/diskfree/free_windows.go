//go:build windows

package diskfree

import "golang.org/x/sys/windows"

// freeBytes returns the space available to the calling user on the volume
// holding dir (GetDiskFreeSpaceEx's first out-parameter honours per-user
// quotas, which is the number that matters to us).
func freeBytes(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &totalFree); err != nil {
		return 0, err
	}
	return avail, nil
}

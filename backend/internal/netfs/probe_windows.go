//go:build windows

package netfs

import "golang.org/x/sys/windows"

// probeFS asks GetDriveType about the volume holding dir. A UNC path is
// network without asking.
func probeFS(dir string) (Info, error) {
	if info := classifyWindows(dir, 0); info.Network {
		return info, nil
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return Info{}, err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return Info{}, err
	}
	return classifyWindows(dir, windows.GetDriveType(&buf[0])), nil
}

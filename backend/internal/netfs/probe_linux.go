//go:build linux

package netfs

import "golang.org/x/sys/unix"

// probeFS reads statfs(2)'s f_type.
func probeFS(dir string) (Info, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Info{}, err
	}
	return classifyLinux(uint32(st.Type)), nil
}

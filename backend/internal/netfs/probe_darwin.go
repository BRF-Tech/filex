//go:build darwin

package netfs

import "golang.org/x/sys/unix"

// probeFS reads statfs(2)'s f_fstypename and its MNT_LOCAL flag.
func probeFS(dir string) (Info, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Info{}, err
	}
	name := make([]byte, 0, len(st.Fstypename))
	for _, b := range st.Fstypename {
		if b == 0 {
			break
		}
		name = append(name, byte(b))
	}
	return classifyDarwin(string(name), st.Flags&unix.MNT_LOCAL != 0), nil
}

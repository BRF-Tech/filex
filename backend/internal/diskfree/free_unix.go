//go:build unix

package diskfree

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// measure reads statfs(2). Free is Bavail (not Bfree) on purpose: the
// reserved blocks root can still use are not space this process can use.
// Used is Blocks-Bfree, the column df prints.
func measure(dir string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Usage{}, err
	}
	unit := blockUnit(&st)
	blocks, bfree := uint64(st.Blocks), uint64(st.Bfree)
	// Signed on the BSDs, where root eating into its reserve drives it below
	// zero.
	avail := int64(st.Bavail)
	if avail < 0 {
		avail = 0
	}
	u := Usage{Total: blocks * unit, Free: uint64(avail) * unit}
	if bfree < blocks {
		u.Used = (blocks - bfree) * unit
	}
	// The device rather than statfs's fsid: some filesystems leave fsid zero,
	// and a disk bind-mounted at two places reports one device for both.
	var s unix.Stat_t
	if unix.Stat(dir, &s) == nil {
		u.Volume = "dev:" + strconv.FormatUint(uint64(s.Dev), 10)
	}
	return u, nil
}

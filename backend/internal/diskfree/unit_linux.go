//go:build linux

package diskfree

import "golang.org/x/sys/unix"

// blockUnit is the size Linux counts f_blocks, f_bfree and f_bavail in: the
// fragment size, or the block size when the filesystem reports no fragment
// size. It is what glibc's statvfs and therefore df use; the two are equal on
// ext4 and xfs, and not on every network filesystem.
func blockUnit(st *unix.Statfs_t) uint64 {
	if st.Frsize > 0 {
		return uint64(st.Frsize)
	}
	return uint64(st.Bsize)
}

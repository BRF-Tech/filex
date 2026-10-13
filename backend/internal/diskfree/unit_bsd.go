//go:build unix && !linux

package diskfree

import "golang.org/x/sys/unix"

// blockUnit is the size the BSDs and macOS count the block fields in: their
// statfs has no separate fragment size.
func blockUnit(st *unix.Statfs_t) uint64 { return uint64(st.Bsize) }

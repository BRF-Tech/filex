//go:build unix

package officeengine

import "golang.org/x/sys/unix"

// reserve maps n bytes of address space nothing may touch yet (PROT_NONE). A
// private mapping that is not writable counts against no commit limit, in
// any of Linux's overcommit modes.
func reserve(n int) (*reservedMemory, error) {
	b, err := unix.Mmap(-1, 0, n, unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return nil, err
	}
	return &reservedMemory{all: b}, nil
}

// commit makes part of a reservation readable and writable (counted against
// the commit limit where the system keeps one); a page is backed when it is
// first written.
func commit(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return unix.Mprotect(b, unix.PROT_READ|unix.PROT_WRITE)
}

// release unmaps a whole reservation.
func release(all []byte) error { return unix.Munmap(all) }

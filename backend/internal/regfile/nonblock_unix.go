//go:build unix

package regfile

import "syscall"

// nonBlock makes an open of a named pipe return at once instead of waiting for
// the other end: a read-only open succeeds (and is then refused by the type
// check), a write-only open fails with ENXIO when nobody is reading.
const nonBlock = syscall.O_NONBLOCK

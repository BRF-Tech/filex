//go:build windows

package local

import "syscall"

// errCrossDevice is ERROR_NOT_SAME_DEVICE, what a rename onto another volume
// answers. syscall.EXDEV exists here too but is an invented value no Windows
// call ever returns. Fixed by the Win32 API, so it cannot drift.
const errCrossDevice syscall.Errno = 17

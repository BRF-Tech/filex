//go:build !windows

package local

import "syscall"

// errCrossDevice is what a rename onto another filesystem answers. Not shared
// with the Windows spelling: 17 is EEXIST here.
const errCrossDevice = syscall.EXDEV

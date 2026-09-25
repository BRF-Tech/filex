//go:build windows

package filesync

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The locked region is one byte far beyond anything the file holds. Windows
// locks are mandatory — a locked byte cannot be READ by another process — so
// locking the note itself would hide the holder from the process that most
// needs to name it. Locking past the end of a file is allowed and blocks
// nobody's read of the first bytes.
const (
	lockOffsetLow  = 0
	lockOffsetHigh = 1 // 4 GiB
)

func lockRegion() *windows.Overlapped {
	return &windows.Overlapped{Offset: lockOffsetLow, OffsetHigh: lockOffsetHigh}
}

// lockFile takes the exclusive lock without waiting.
func lockFile(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lerr error
	if err := rc.Control(func(fd uintptr) {
		lerr = windows.LockFileEx(windows.Handle(fd),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRegion())
	}); err != nil {
		return err
	}
	switch {
	case lerr == nil:
		return nil
	case errors.Is(lerr, windows.ERROR_LOCK_VIOLATION), errors.Is(lerr, windows.ERROR_SHARING_VIOLATION):
		return ErrPairBusy
	case errors.Is(lerr, windows.ERROR_NOT_SUPPORTED), errors.Is(lerr, windows.ERROR_INVALID_FUNCTION):
		return errLockUnsupported
	}
	return lerr
}

func unlockFile(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var uerr error
	if err := rc.Control(func(fd uintptr) {
		uerr = windows.UnlockFileEx(windows.Handle(fd), 0, 1, 0, lockRegion())
	}); err != nil {
		return err
	}
	return uerr
}

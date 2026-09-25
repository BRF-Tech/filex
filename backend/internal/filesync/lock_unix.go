//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package filesync

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes the exclusive lock without waiting.
//
// flock, not fcntl (POSIX) locks: a POSIX lock belongs to the PROCESS and is
// dropped when that process closes ANY descriptor of the file — a stray
// os.ReadFile of the lock file (readHolder does exactly that) would silently
// let go of it. A flock belongs to the open file and ends with it, or with
// the process.
func lockFile(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lerr error
	if err := rc.Control(func(fd uintptr) {
		for {
			lerr = unix.Flock(int(fd), unix.LOCK_EX|unix.LOCK_NB)
			if !errors.Is(lerr, unix.EINTR) {
				return
			}
		}
	}); err != nil {
		return err
	}
	switch {
	case lerr == nil:
		return nil
	case errors.Is(lerr, unix.EWOULDBLOCK), errors.Is(lerr, unix.EAGAIN):
		return ErrPairBusy
	case errors.Is(lerr, unix.ENOLCK), errors.Is(lerr, unix.ENOTSUP), errors.Is(lerr, unix.EOPNOTSUPP):
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
		uerr = unix.Flock(int(fd), unix.LOCK_UN)
	}); err != nil {
		return err
	}
	return uerr
}

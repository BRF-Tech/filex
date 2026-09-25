//go:build !windows && !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package filesync

import "os"

// No lock primitive wired up for this platform (none of the release targets):
// the lock is held in name only, as on a file system that cannot lock.
func lockFile(*os.File) error   { return errLockUnsupported }
func unlockFile(*os.File) error { return nil }

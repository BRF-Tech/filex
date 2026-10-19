//go:build !linux

package contain

import "os"

// self is the binary to start as a child.
func self() string {
	exe, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return exe
}

// offerToOOMKiller has nothing to do here: the watch is the ceiling.
func offerToOOMKiller() {}

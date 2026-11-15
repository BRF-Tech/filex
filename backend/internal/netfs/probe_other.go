//go:build !linux && !darwin && !windows

package netfs

import "errors"

// probeFS has no implementation on this platform: the answer is unknown, and
// the caller decides what that means (the lazy catalogue places no watch).
func probeFS(string) (Info, error) {
	return Info{}, errors.New("netfs: unsupported on this platform")
}

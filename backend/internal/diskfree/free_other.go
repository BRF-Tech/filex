//go:build !unix && !windows

package diskfree

import "errors"

// freeBytes has no implementation on this platform. Callers treat a probe
// error as "cannot measure" and carry on — refusing to do the work
// because we cannot read a number would be worse than the guard being
// absent.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("diskfree: unsupported on this platform")
}

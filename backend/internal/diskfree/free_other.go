//go:build !unix && !windows

package diskfree

import "errors"

// measure has no implementation on this platform. Callers treat a probe
// error as "cannot measure" and carry on — refusing to do the work
// because we cannot read a number would be worse than the guard being
// absent.
func measure(string) (Usage, error) {
	return Usage{}, errors.New("diskfree: unsupported on this platform")
}

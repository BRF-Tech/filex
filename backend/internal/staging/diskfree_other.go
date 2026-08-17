//go:build !unix && !windows

package staging

import "errors"

// freeBytes has no implementation on this platform. EnsureFree treats a probe
// error as "cannot measure" and lets the upload through — refusing every
// upload because we cannot read a number would be worse than the guard being
// absent.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("staging: free space probe unsupported on this platform")
}

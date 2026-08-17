//go:build !windows

package local

import "errors"

// No such condition off Windows — sharingViolation is false for everything, so
// the tests that need one skip themselves rather than assert against a stub.
func errSharingViolationForTest() error { return errors.New("not a sharing violation here") }

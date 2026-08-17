//go:build windows

package local

import "io/fs"

// errSharingViolationForTest builds the error Windows actually returns when a
// rename or unlink hits an open handle — wrapped in *fs.PathError, because that
// is how os.Rename and os.RemoveAll deliver it and the retry has to see through
// the wrapper.
func errSharingViolationForTest() error {
	return &fs.PathError{Op: "rename", Path: "held.txt", Err: errSharingViolation}
}

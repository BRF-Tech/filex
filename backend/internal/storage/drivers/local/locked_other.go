//go:build !windows

package local

// Unix has no equivalent condition: a rename or an unlink succeeds while other
// processes hold the file open, and the inode simply outlives the name. So the
// retry below never engages here — it costs one comparison and nothing else.
//
// See locked_windows.go for what this is guarding against.
func sharingViolation(error) bool { return false }

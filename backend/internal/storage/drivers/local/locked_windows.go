//go:build windows

package local

import (
	"errors"
	"syscall"
)

// Windows lets a file be held in a way that blocks renaming and deleting it,
// and that is not an exotic condition here — it is the ordinary case.
//
// # Why this file exists
//
// Measured 2026-08-17: uploading a file and deleting it straight away returned
// **500** about three times in two hundred, with
//
//	trash: rename …\race-61.txt …\.filex-trash\…__race-61.txt:
//	The process cannot access the file because it is being used by another process.
//
// ⚠⚠ And it is not only antivirus. Go's own `syscall.Open` on Windows asks for
// `FILE_SHARE_READ | FILE_SHARE_WRITE` and **not** `FILE_SHARE_DELETE`
// (`syscall_windows.go`), so *any* file filex itself has open for reading —
// the thumbnailer, the content indexer, a download in flight — blocks a rename
// of that file for as long as the read lasts. filex was standing on its own
// foot, and the user saw a delete fail with an internal error.
//
// Real-time scanners (Defender opens a newly written file within milliseconds)
// do the same thing and are outside anybody's control, so the holder cannot be
// eliminated — only waited out. Every holder here releases in milliseconds.

// The two codes the standard library keeps to itself: they live in
// `internal/syscall/windows`, which is not importable, so the values are
// spelled out here. They are fixed by the Win32 API and cannot drift.
const (
	errSharingViolation syscall.Errno = 32 // another process has the file open
	errLockViolation    syscall.Errno = 33 // a byte range in the file is locked
)

// sharingViolation reports whether err is Windows refusing an operation because
// somebody else has the file open.
//
// ⚠ Narrow on purpose. A blanket "retry any error" would spend the whole budget
// on a genuine permission problem or a path that does not exist, turning a
// clean fast failure into a slow one.
func sharingViolation(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case errSharingViolation, errLockViolation,
		// ⚠ ACCESS_DENIED is included because Windows reports it for a file
		// that is already delete-pending — another handle asked for its
		// removal and the name survives until that handle closes. Waiting is
		// exactly the right response to that, and a genuinely unwritable file
		// costs one second before failing with the same error it would have
		// failed with anyway.
		syscall.ERROR_ACCESS_DENIED:
		return true
	}
	return false
}

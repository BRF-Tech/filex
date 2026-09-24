// Package regfile opens files that are REGULAR files, and nothing else.
//
// ⚠⚠ Why this exists (issue #38). A directory on disk can hold things that are
// not files at all: a named pipe (FIFO), a Unix socket, a block or character
// device. Docker's overlay directories are full of them. Opening a named pipe
// for reading BLOCKS until something opens its other end — which, for a pipe
// nobody writes to, is forever. The local storage driver treated every entry
// that was not a folder or a symlink as a file and opened it to sniff its type,
// so one `mkfifo` anywhere under a local storage root hung the storage scan in
// `running` with nothing processed, and every scan after it queued behind the
// hung one. A socket or a device is no better: a socket cannot be opened at
// all, and opening a device can have side effects (a tape rewinds).
//
// filex serves regular files and folders. Everything else is skipped by the
// walks (Special says which) and refused by every open that goes through
// here — and the open refuses WITHOUT blocking: an entry that turns into a
// pipe between the check and the open is opened non-blocking, looked at, and
// closed.
package regfile

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"sync"
)

// ErrNotRegular is what an open of anything but a regular file answers. It is
// wrapped in an *fs.PathError, so errors.Is(err, ErrNotRegular) finds it.
var ErrNotRegular = errors.New("not a regular file")

// special is every type bit that makes an entry something a file server must
// never open: named pipes, sockets, block and character devices, and whatever
// the platform calls "irregular".
const special = fs.ModeNamedPipe | fs.ModeSocket | fs.ModeDevice | fs.ModeCharDevice | fs.ModeIrregular

// Special reports whether m is a named pipe, socket, device node or irregular
// file. Folders, regular files and symlinks are NOT special — a symlink is
// judged by its target, which the caller has to Stat.
func Special(m fs.FileMode) bool { return m&special != 0 }

// KindOf names what a special entry is, for a log line an operator reads.
func KindOf(m fs.FileMode) string {
	switch {
	case m&fs.ModeNamedPipe != 0:
		return "named pipe"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m&fs.ModeCharDevice != 0:
		return "character device"
	case m&fs.ModeDevice != 0:
		return "block device"
	case m&fs.ModeIrregular != 0:
		return "irregular file"
	case m.IsDir():
		return "folder"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	default:
		return "file"
	}
}

// Open opens name for reading if it is a regular file.
func Open(name string) (*os.File, error) { return OpenFile(name, os.O_RDONLY, 0) }

// OpenFile is os.OpenFile for regular files only. A path that already exists
// as anything else — a folder included — is refused before it is opened: an
// open of a device node is itself an action. What is left is the race where a
// pipe appears between that look and the open; the open is therefore made
// non-blocking where the platform has pipes (see nonBlock), and the opened
// descriptor is checked again before it is handed out.
//
// ⚠ The flag stays on the returned file. On a regular file O_NONBLOCK changes
// nothing — disk reads and writes block the way they always do — so there is
// no second syscall to clear it.
func OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	if fi, err := os.Stat(name); err == nil && !fi.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "open", Path: name, Err: ErrNotRegular}
	}
	return openChecked(name, flag, perm)
}

// openChecked is OpenFile without the look first: the half that has to hold on
// its own when a pipe appears after the look.
func openChecked(name string, flag int, perm fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(name, flag|nonBlock, perm)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: ErrNotRegular}
	}
	return f, nil
}

// Skipped says, once per path, that a walk turned a special entry away.
//
// ⚠ Once per path per process, not once per walk: a storage is rescanned
// every few minutes, and the same named pipe reported on every pass buries
// everything else in the log. The zero value is ready to use; keep one per
// driver so a second storage with its own pipes still gets its own lines.
type Skipped struct{ seen sync.Map }

// Report logs that `path` under the storage rooted at `root` was skipped.
func (s *Skipped) Report(driver, root, path string, mode fs.FileMode) {
	if _, dup := s.seen.LoadOrStore(path, true); dup {
		return
	}
	slog.Warn("storage scan: skipped an entry that is not a regular file or folder — filex never opens named pipes, sockets or device nodes",
		slog.String("driver", driver),
		slog.String("root", root),
		slog.String("path", path),
		slog.String("kind", KindOf(mode)))
}

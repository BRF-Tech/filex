package nfssrv

import (
	"os"
	"time"

	billy "github.com/go-git/go-billy/v5"
)

// deniedFS is what a REFUSED mount is answered with.
//
// ⚠⚠ It exists because of a crash, not for tidiness. go-nfs calls
// `ToHandle(handle, …)` before it inspects the mount status (mount.go:42), so
// returning a nil filesystem from a refusal is dereferenced inside the
// library's caching handler and panics — and a panic in a connection goroutine
// takes the whole process down. Probing one wrong export path would stop filex
// for every other user of every other protocol.
//
// Every method refuses. Nothing is reachable through it, and the client never
// sees the handle anyway: the status it receives is the refusal.
type deniedFS struct{}

func (*deniedFS) Create(string) (billy.File, error) { return nil, os.ErrPermission }
func (*deniedFS) Open(string) (billy.File, error)   { return nil, os.ErrPermission }
func (*deniedFS) OpenFile(string, int, os.FileMode) (billy.File, error) {
	return nil, os.ErrPermission
}
func (*deniedFS) Stat(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
func (*deniedFS) Rename(string, string) error      { return os.ErrPermission }
func (*deniedFS) Remove(string) error              { return os.ErrPermission }
func (*deniedFS) Join(...string) string            { return "" }
func (*deniedFS) TempFile(string, string) (billy.File, error) {
	return nil, os.ErrPermission
}
func (*deniedFS) ReadDir(string) ([]os.FileInfo, error)   { return nil, os.ErrNotExist }
func (*deniedFS) MkdirAll(string, os.FileMode) error      { return os.ErrPermission }
func (*deniedFS) Lstat(string) (os.FileInfo, error)       { return nil, os.ErrNotExist }
func (*deniedFS) Symlink(string, string) error            { return os.ErrPermission }
func (*deniedFS) Readlink(string) (string, error)         { return "", os.ErrPermission }
func (*deniedFS) Chroot(string) (billy.Filesystem, error) { return nil, os.ErrPermission }
func (*deniedFS) Root() string                            { return "/" }

// Unused, but kept next to the type so a future billy version that adds them to
// the interface fails to compile here rather than at a call site.
var _ = time.Time{}

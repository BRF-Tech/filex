//go:build !linux && !windows

package mountfs

import (
	"fmt"
	"runtime"
)

// Everything that is not Linux.
//
// ⚠⚠ This is a REFUSAL, not a stub that pretends. `filex mount` needs a kernel
// filesystem driver, and outside Linux that is a separate installation with its
// own licence:
//
//   - macOS: macFUSE, whose licence since 4.0 forbids automated download or
//     installation "in the context of commercial software" — so filex cannot
//     ship or fetch it, the user has to install it themselves. FUSE-T is the
//     kext-less alternative and works by running a local NFSv4 server.
//   - Windows: WinFsp, GPLv3 with a FLOSS exception. That exception covers
//     filex today because filex is MIT; the day filex ships closed-source it
//     becomes a paid commercial licence. Worth knowing before it matters.
//
// Both are buildable from here — the filesystem layer in mountfs.go is already
// platform-independent, and only this file is missing. What is NOT acceptable
// is a `filex mount` that appears to work and silently does nothing, so on
// these platforms the command says what it needs and points at the folder sync,
// which already works everywhere.

// Server is the platform-independent shape; nothing constructs one here.
type Server struct{}

func (s *Server) Wait()              {}
func (s *Server) Unmount() error     { return nil }
func (s *Server) Mountpoint() string { return "" }

// Mount refuses, with the platform named so the message is actionable.
func Mount(_ *FS, _ string, _ bool) (*Server, error) {
	return nil, fmt.Errorf("%w (running on %s)", ErrUnsupported, runtime.GOOS)
}

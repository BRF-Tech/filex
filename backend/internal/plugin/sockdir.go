package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// sockNameRoom is what the SDK puts after the socket directory: "/p<pid>.sock",
// with room for a pid of ten digits.
const sockNameRoom = len("/p") + 10 + len(".sock")

// sockPathMax is the longest path a unix socket can be bound or dialled at:
// sun_path holds 108 bytes on Linux and 104 on macOS and the BSDs, the
// terminating NUL included.
func sockPathMax() int {
	if runtime.GOOS == "linux" {
		return 107
	}
	return 103
}

// shortSocketDir answers the directory a plugin is told to create its socket
// in. That is dir itself whenever a socket in it fits the platform's limit.
//
// ⚠ It does not always. dir is <data dir>/plugins/<name>/run, and a data
// directory of fifty-odd characters (a container volume's host path, say)
// with a long plugin name leaves no room for the socket: the plugin's bind
// fails with "invalid argument" and filex only sees that it never printed its
// handshake. Then the answer is a link to dir in a fresh private directory
// under the system's temp directory: the plugin binds and filex dials through
// the link, and the socket itself is still created in dir, with dir's owner
// and permissions - which the store's validator hands to the plugin's own
// user (cmd/filex/plugin_validator.go), so moving the directory instead would
// have broken it. The handshake is still checked against dir with every link
// resolved (parseHandshake).
//
// The private directory has a random name and lets others through but not
// in: nobody can plant a link of their own there. done removes it, and only
// it, once the plugin has exited.
func shortSocketDir(dir string) (string, func(), error) {
	keep := func() {}
	// Windows plugins listen on a loopback port (pkg/pluginsdk).
	if runtime.GOOS == "windows" || len(dir)+sockNameRoom <= sockPathMax() {
		return dir, keep, nil
	}
	tmp, err := os.MkdirTemp("", "fxp-")
	if err != nil {
		return "", nil, err
	}
	done := func() { _ = os.RemoveAll(tmp) }
	link := filepath.Join(tmp, "s")
	if len(link)+sockNameRoom > sockPathMax() {
		// A temp directory this long cannot help either.
		done()
		return dir, keep, nil
	}
	if err := os.Chmod(tmp, 0o711); err != nil {
		done()
		return "", nil, err
	}
	if err := os.Symlink(dir, link); err != nil {
		done()
		return "", nil, err
	}
	return link, done, nil
}

// dialThrough is addr, with a unix socket reached through linkDir, the short
// link shortSocketDir gave the plugin, when linkDir is not realDir itself.
//
// parseHandshake answers the socket's real path, links resolved, so that
// filex dials exactly the socket it checked; but that path is the one too
// long to connect to. The same socket through the link is the same file: the
// link sits in filex's own private directory, where nobody else can replace
// it, and leads to the directory the check was made against.
func dialThrough(addr Address, realDir, linkDir string) Address {
	if addr.Network != "unix" || linkDir == realDir {
		return addr
	}
	base, err := absReal(realDir)
	if err != nil {
		return addr
	}
	rel, err := filepath.Rel(base, addr.Target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return addr
	}
	addr.Target = filepath.Join(linkDir, rel)
	return addr
}

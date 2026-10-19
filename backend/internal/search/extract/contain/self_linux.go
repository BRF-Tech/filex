//go:build linux

package contain

import "os"

// self is the binary to start as a child. /proc/self/exe, not os.Executable:
// after a self-update has replaced the file on disk, the path names the new
// binary (or nothing) while this process is still the old one.
func self() string { return "/proc/self/exe" }

// offerToOOMKiller makes the child the first process the kernel's OOM killer
// picks, in the machine and in the container: should its memory outrun the
// watch, the child goes, not the server beside it. Raising one's own score
// needs no privilege.
func offerToOOMKiller() {
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("1000"), 0)
}

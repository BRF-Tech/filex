//go:build linux

package pam

import (
	"os/exec"
	"syscall"
)

// detach runs the command in a session of its own. pamtester reads the
// password from the terminal when it has one (glibc's getpass opens
// /dev/tty); filex started from a shell would then wait on the operator's
// terminal instead of the pipe. With no controlling terminal it reads stdin.
//
// The timeout asks politely (SIGTERM, which sudo relays to the command)
// before Go's WaitDelay kills what is left: a process that runs as root
// cannot be signalled by filex directly.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
}

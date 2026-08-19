//go:build linux || darwin

package plugin

import (
	"os/exec"
	"syscall"
)

// What filex does — and does not — do about a plugin process's footprint.
//
// A plugin is somebody else's program running as filex's user. filex is NOT
// a sandbox and this file does not pretend otherwise: real isolation means
// namespaces, seccomp or a container runtime, none of which a Go supervisor
// can honestly claim to provide.
//
// What it does provide is the one thing that stops a plugin from outliving
// its own supervision: a process GROUP. A plugin that forks a helper (an
// rclone, a mount, an ssh) would otherwise leave it running after filex
// kills the parent — the plugin looks stopped, and the next start meets a
// socket that is already in use with nothing on screen to explain it.
//
// ⚠⚠ Memory and file-descriptor ceilings are deliberately NOT set here. Go
// cannot apply an rlimit to a child between fork and exec (there is no hook),
// and setting one in the parent would cap filex itself. Doing it properly
// needs a helper binary or a container; until then the honest statement is:
// a plugin can allocate as much as the machine allows. What IS bounded is
// how much of filex a plugin can occupy — see limits.go for the per-plugin
// concurrency ceiling, which is what keeps one slow plugin from becoming one
// slow filex.

// applyLimits sets the process attributes filex wants on every plugin it
// launches. Called before Start.
func applyLimits(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// adoptChild has nothing to do here: the process group set above is what binds
// a plugin's descendants to it.
//
// ⚠ The obvious next step — Pdeathsig, so a plugin dies when filex is KILLED
// rather than stopped — is deliberately not taken. In Go it fires when the OS
// THREAD that forked exits, and the runtime retires idle threads, so a healthy
// plugin can be killed for no reason at all. An orphan is a nuisance; a plugin
// that dies at random is a bug report nobody can reproduce. Windows gets the
// guarantee because job objects give it without that catch.
func adoptChild(cmd *exec.Cmd) error { return nil }

// killGroup kills the plugin AND anything it started.
func killGroup(pid int) {
	// Negative pid = the whole process group (Setpgid above made one).
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

//go:build !windows

package wasmplugin

import (
	"os/exec"
	"syscall"
)

// setProcAttr puts the engine in its own process group so killTree can end
// every helper it spawned (soffice forks, ffmpeg does not — but the rule is
// the same for both).
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

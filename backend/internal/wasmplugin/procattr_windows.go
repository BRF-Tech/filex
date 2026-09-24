//go:build windows

package wasmplugin

import (
	"os/exec"
	"strconv"
)

func setProcAttr(cmd *exec.Cmd) {}

// killTree ends the engine and its children; taskkill /T walks the tree,
// which Process.Kill alone would not.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	return cmd.Process.Kill()
}

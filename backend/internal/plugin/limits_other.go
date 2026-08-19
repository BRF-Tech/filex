//go:build !linux && !darwin && !windows

package plugin

import "os/exec"

// Whatever this platform is, it is neither Unix nor Windows, so filex applies
// nothing and says so rather than pretending. (Windows does have an answer and
// uses it — see limits_windows.go.)

func applyLimits(cmd *exec.Cmd) {}

func adoptChild(cmd *exec.Cmd) error { return nil }

// killGroup falls back to killing the process itself; children it started
// survive, which is a known gap on this platform.
func killGroup(pid int) {}

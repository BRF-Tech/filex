//go:build !linux && !darwin

package plugin

import "os/exec"

// Windows has no rlimits and no process groups in the POSIX sense, so the
// ceilings the unix build applies simply do not exist here. Saying that in
// code beats pretending: a plugin on Windows is bounded by filex's
// concurrency limit and by nothing else.
//
// (Job objects could do it. They are a bigger piece of work than this
// release, and a Windows deployment of filex is not where plugins are run
// today.)

func applyLimits(cmd *exec.Cmd) {}

// killGroup falls back to killing the process itself; children it started
// survive, which is a known gap on this platform.
func killGroup(pid int) {}

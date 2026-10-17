//go:build !linux && !windows

package officeengine

import "os/exec"

// setChildAttr: nothing here (an engine process ends when its stdin closes,
// which happens when filex does).
func setChildAttr(*exec.Cmd) {}

// processRSS: this system's resident memory is not read (macOS needs cgo for
// it); the per-instance cap still holds every module.
func processRSS(int) (int64, bool) { return 0, false }

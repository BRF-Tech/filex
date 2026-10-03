//go:build !linux

package pam

import "os/exec"

// detach is a no-op off Linux; the provider needs PAM and is Linux-only in
// practice (its test fails with the missing pamtester elsewhere).
func detach(_ *exec.Cmd) {}

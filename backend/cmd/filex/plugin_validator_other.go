//go:build !linux

package main

import (
	"errors"
	"os"
	"os/exec"
)

// pvCredentialsSupported: elsewhere the plugin under test runs as this
// command's own user, so the command starts only with --insecure-dev
// (development only; the store's validator is Linux).
const pvCredentialsSupported = false

var errNotLinux = errors.New("handing files to another user works on Linux only")

func pvRunAs(int, int) func(*exec.Cmd) { return nil }

func pvChown(string, int, int) error { return errNotLinux }

func pvOwner(os.FileInfo) (int, bool) { return 0, false }

// pvWritableBy: there is no other user to hold apart.
func pvWritableBy(os.FileInfo, int, int) bool { return false }

// pvSweep: no other user, nothing of its own to sweep.
func pvSweep(pvOptions) error { return nil }

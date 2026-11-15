//go:build !windows

package main

import (
	"context"
	"errors"
)

// isWindowsService is false anywhere but Windows: systemd and launchd start
// filex-watch as an ordinary process (packaging/).
func isWindowsService() bool { return false }

func runService(func(ctx context.Context) error) error {
	return errors.New("not a Windows service")
}

//go:build !windows && !darwin && !linux

package main

import (
	"context"
	"errors"
	"io"
)

// mountVaultOS has no system mount to ask for here: the address is printed
// for a WebDAV client.
func mountVaultOS(ctx context.Context, url, mountpoint, label string, w io.Writer) (*osMount, error) {
	return nil, errors.New("filex does not know how to ask this system for a WebDAV mount")
}

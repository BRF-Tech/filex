//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// Linux has no WebDAV mount every desktop shares. With a folder named, filex
// asks davfs2 (`mount -t davfs`: root, or a `user` line for the address in
// /etc/fstab); without one, GNOME's gio, whose mounts appear under
// /run/user/<uid>/gvfs and in the file manager's "Other Locations". Neither
// there: the address is printed for a WebDAV client.

func mountVaultOS(ctx context.Context, addr, mountpoint, label string, w io.Writer) (*osMount, error) {
	if mountpoint != "" {
		if _, err := exec.LookPath("mount.davfs"); err != nil {
			return nil, errors.New("mounting at a folder needs davfs2 (mount.davfs); without a folder, filex uses gio (GNOME)")
		}
		cmd := exec.CommandContext(ctx, "mount", "-t", "davfs", "-o", fmt.Sprintf("uid=%d,gid=%d", os.Getuid(), os.Getgid()), addr, mountpoint)
		// davfs2 asks for a user name and a password; the address is the
		// only credential, so both are empty.
		cmd.Stdin = strings.NewReader("\n\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("mount -t davfs: %v: %s (davfs2 needs root, or a user line for this address in /etc/fstab)", err, strings.TrimSpace(string(out)))
		}
		return &osMount{
			where:   mountpoint,
			unmount: func() error { return exec.Command("umount", mountpoint).Run() },
		}, nil
	}
	if _, err := exec.LookPath("gio"); err != nil {
		return nil, errors.New("neither gio (GNOME) nor davfs2 is installed - install one, or open the address in a WebDAV client")
	}
	dav := "dav" + strings.TrimPrefix(addr, "http")
	out, err := exec.CommandContext(ctx, "gio", "mount", dav).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gio mount: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return &osMount{
		where:   gvfsPath(addr) + " (" + label + ", also under Other Locations in the file manager)",
		unmount: func() error { return exec.Command("gio", "mount", "-u", dav).Run() },
	}, nil
}

// gvfsPath is where gvfs shows a dav mount of addr.
func gvfsPath(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return "/run/user/" + fmt.Sprint(os.Getuid()) + "/gvfs"
	}
	prefix := url.QueryEscape(strings.TrimSuffix(u.Path, "/"))
	return fmt.Sprintf("/run/user/%d/gvfs/dav:host=%s,port=%s,ssl=false,prefix=%s", os.Getuid(), u.Hostname(), u.Port(), prefix)
}

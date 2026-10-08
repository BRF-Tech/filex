//go:build darwin

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS mounts the vault's WebDAV server with mount_webdav at a folder: the
// one named, or ~/filex-vaults/<vault>, made for the mount and removed after
// it. -S keeps mount_webdav from showing dialogs of its own.

func mountVaultOS(ctx context.Context, url, mountpoint, label string, w io.Writer) (*osMount, error) {
	dir := mountpoint
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, "filex-vaults", vaultVolumeName(label))
	}
	created := false
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		created = true
	}
	out, err := exec.CommandContext(ctx, "/sbin/mount_webdav", "-S", "-v", vaultVolumeName(label), url, dir).CombinedOutput()
	if err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return nil, fmt.Errorf("mount_webdav: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return &osMount{
		where: dir,
		unmount: func() error {
			err := exec.Command("/sbin/umount", dir).Run()
			if err != nil {
				err = exec.Command("/usr/sbin/diskutil", "unmount", "force", dir).Run()
			}
			if err == nil && created {
				_ = os.Remove(dir)
			}
			return err
		},
	}, nil
}

// vaultVolumeName is a vault's name as a volume or folder name.
func vaultVolumeName(label string) string {
	s := strings.Map(func(r rune) rune {
		if r == '/' || r == ':' || r < 0x20 {
			return '_'
		}
		return r
	}, label)
	if s == "" || s == "." || s == ".." {
		return "vault"
	}
	return s
}

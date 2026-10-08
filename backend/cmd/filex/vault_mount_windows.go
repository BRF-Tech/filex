//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows mounts the vault's WebDAV server as a drive letter with `net use`,
// through its own WebDAV client - the WebClient service. Two things about it
// are said before the mount, with their fix:
//
//   - the service may be disabled (Windows Server has no WebDAV client until
//     the "WebDAV Redirector" feature is installed);
//   - FileSizeLimitInBytes: by default the client refuses to download a file
//     over 50 000 000 bytes, and it can be raised to 4 GB at most.

const webClientParams = `SYSTEM\CurrentControlSet\Services\WebClient\Parameters`

var scField = regexp.MustCompile(`(?m)^\s*(STATE|START_TYPE)\s*:\s*(\d+)`)

func scValues(args ...string) map[string]string {
	out, err := exec.Command("sc.exe", args...).CombinedOutput()
	if err != nil {
		return nil
	}
	vals := map[string]string{}
	for _, m := range scField.FindAllStringSubmatch(string(out), -1) {
		vals[m[1]] = m[2]
	}
	return vals
}

// warnWebClient says what would stop Windows from mounting the vault, or
// from opening large files on it.
func warnWebClient(w io.Writer) {
	if cfg := scValues("qc", "WebClient"); cfg == nil {
		fmt.Fprintln(w, "warning: this Windows has no WebDAV client (the WebClient service). On Windows Server, install the")
		fmt.Fprintln(w, "  \"WebDAV Redirector\" feature: Install-WindowsFeature WebDAV-Redirector (then restart).")
	} else if cfg["START_TYPE"] == "4" {
		fmt.Fprintln(w, "warning: the Windows WebDAV client (the WebClient service) is disabled, so Windows cannot mount the")
		fmt.Fprintln(w, "  vault. As administrator:  sc config WebClient start= demand  &&  net start WebClient")
	}
	limit := uint64(50000000)
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, webClientParams, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("FileSizeLimitInBytes"); err == nil {
			limit = v
		}
		_ = k.Close()
	}
	if limit < 0xFFFFFFFF {
		fmt.Fprintf(w, "warning: Windows' WebDAV client refuses files over %d MB (FileSizeLimitInBytes). To raise it to\n", limit/1000000)
		fmt.Fprintln(w, "  the 4 GB it allows, as administrator, then restart the service:")
		fmt.Fprintln(w, `    reg add HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters /v FileSizeLimitInBytes /t REG_DWORD /d 4294967295 /f`)
		fmt.Fprintln(w, "    net stop WebClient && net start WebClient")
		fmt.Fprintln(w, "  Files over 4 GB cannot be opened through a Windows WebDAV drive at all: use filex decrypt for them.")
	}
}

// freeDriveLetter is the last free letter from Z: down to D:.
func freeDriveLetter() string {
	for c := 'Z'; c >= 'D'; c-- {
		d := string(c) + ":"
		if _, err := os.Stat(d + `\`); err != nil {
			return d
		}
	}
	return ""
}

func mountVaultOS(ctx context.Context, url, mountpoint, label string, w io.Writer) (*osMount, error) {
	warnWebClient(w)
	drive := mountpoint
	if drive == "" {
		if drive = freeDriveLetter(); drive == "" {
			return nil, errors.New("no free drive letter")
		}
	}
	if !driveLetter(drive) {
		return nil, fmt.Errorf("on Windows the mountpoint is a free drive letter, like Z: (not %q)", drive)
	}
	out, err := exec.CommandContext(ctx, "net", "use", drive, url, "/persistent:no").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("net use %s: %v: %s", drive, err, strings.TrimSpace(string(out)))
	}
	return &osMount{
		where: drive + ` (` + label + `)`,
		unmount: func() error {
			out, err := exec.Command("net", "use", drive, "/delete", "/y").CombinedOutput()
			if err != nil {
				return fmt.Errorf("net use %s /delete: %v: %s", drive, err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}, nil
}

// mount.go wires the `filex mount` subcommand: a remote filex server, attached
// to a folder on this machine.
//
// It is the third answer to "let me use filex as a drive", and the one that
// works from anywhere: NFS needs a LAN, SFTP needs sshfs or WinFsp installed
// and configured, and this needs the same HTTPS and the same session token the
// web app already uses. Authentication, ACLs, the tenant scope and reach
// through whatever proxy sits in between all come for free, because underneath
// it is the REST API.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/brf-tech/filex/backend/internal/mountfs"
)

type mountOpts struct {
	client      *clientOpts
	remote      string
	readOnly    bool
	blockSize   int64
	cacheBlocks int
	attrTTL     time.Duration
	spoolDir    string
	debug       bool
}

func mountCmd() *cobra.Command {
	o := &mountOpts{client: &clientOpts{}}

	cmd := &cobra.Command{
		Use:   "mount <mountpoint>",
		Short: "Mount a remote filex server as a folder on this machine",
		Long: `Mount a remote filex server as a folder.

Nothing is copied to this machine except a bounded read cache, so a mount opens
one file out of a hundred thousand without downloading the rest. That is the
difference from ` + "`filex sync`" + `, which keeps a folder on disk and is still the
right answer when you want the files when you are offline.

Examples:
  filex mount ~/filex                       # every storage you can see
  filex mount --remote docs:// ~/docs       # one storage
  filex mount --remote 'docs://projects/acme' --read-only ~/acme
  filex mount Z:                            # Windows: a drive letter

Linux needs nothing; Windows needs WinFsp (free — https://winfsp.dev).
macOS is not supported — use ` + "`filex sync`" + ` or the desktop app there.

⚠ Stop the mount with Ctrl-C or by unmounting it, not by killing the process:
  fusermount -u ~/filex      (Linux)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(cmd.Context(), args[0])
		},
	}

	f := cmd.Flags()
	// The same connection resolution `filex client` uses — flags, then
	// FILEX_URL/FILEX_TOKEN, then ~/.filex/cli.yaml. A mount that needed its
	// own login would be a second place for a token to go stale.
	f.StringVar(&o.client.url, "url", "", "filex server URL (default: $FILEX_URL or ~/.filex/cli.yaml)")
	f.StringVar(&o.client.token, "token", "", "API or session token (default: $FILEX_TOKEN or ~/.filex/cli.yaml)")
	f.StringVar(&o.remote, "remote", "", "what to mount: empty for every storage, `docs://` for one, `docs://sub/dir` for a subtree")
	f.BoolVar(&o.readOnly, "read-only", false, "refuse every write through this mount")
	f.Int64Var(&o.blockSize, "block-size", 4<<20, "read granularity in bytes")
	f.IntVar(&o.cacheBlocks, "cache-blocks", 64, "how many blocks to keep in memory")
	f.DurationVar(&o.attrTTL, "attr-ttl", 5*time.Second, "how long a listing is trusted before it is re-fetched")
	f.StringVar(&o.spoolDir, "spool-dir", "", "where in-flight writes are spooled (default: the system temp dir)")
	f.BoolVar(&o.debug, "debug", false, "log every filesystem call")
	return quiet(cmd)
}

func (o *mountOpts) run(ctx context.Context, mountpoint string) error {
	api, err := o.client.api(true)
	if err != nil {
		return err
	}

	// ⚠ Checked BEFORE the mount rather than after: the driver's own error for
	// a bad mountpoint ("no such file or directory") reads as though the SERVER
	// were missing.
	//
	// ⚠⚠ And the two platforms want OPPOSITE things. On Linux the mountpoint is
	// a directory that must already exist. On Windows it is usually a drive
	// letter, which must NOT exist — `filex mount Z:` is the whole point, and
	// requiring it to be there first would reject every normal invocation.
	if driveLetter(mountpoint) {
		if _, err := os.Stat(mountpoint + `\`); err == nil {
			return fmt.Errorf("drive %s is already in use — pick a free letter", mountpoint)
		}
	} else {
		info, err := os.Stat(mountpoint)
		if err != nil {
			return fmt.Errorf("mountpoint %s: %w (create it first: mkdir -p %s)", mountpoint, err, mountpoint)
		}
		if !info.IsDir() {
			return fmt.Errorf("mountpoint %s is not a directory", mountpoint)
		}
	}

	fsys, err := mountfs.New(mountfs.Config{
		Client:      api,
		Remote:      o.remote,
		ReadOnly:    o.readOnly,
		BlockSize:   o.blockSize,
		CacheBlocks: o.cacheBlocks,
		AttrTTL:     o.attrTTL,
		SpoolDir:    o.spoolDir,
	})
	if err != nil {
		return err
	}

	// One request before mounting, so a wrong URL or an expired token is an
	// error message here rather than an unexplained I/O failure inside a
	// mounted directory a minute later.
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	_, probeErr := fsys.ReadDir(probeCtx, "/")
	cancel()
	if probeErr != nil {
		return authHint(fmt.Errorf("cannot read %s: %w", fsys.Describe(), probeErr))
	}

	srv, err := mountfs.Mount(fsys, mountpoint, o.debug)
	if err != nil {
		if errors.Is(err, mountfs.ErrUnsupported) {
			return err
		}
		return fmt.Errorf("mount %s: %w", mountpoint, err)
	}

	fmt.Fprintf(os.Stdout, "mounted %s at %s\n", fsys.Describe(), mountpoint)
	fmt.Fprintf(os.Stdout, "%s\n", unmountHint(mountpoint))

	// ⚠⚠ The unmount on the way out is not optional. A mount whose process died
	// without detaching leaves a directory where every `ls` hangs until somebody
	// runs fusermount by hand — and the person who hits that is rarely the
	// person who knows the command.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		srv.Wait()
		close(done)
	}()

	select {
	case <-stop:
		fmt.Fprintln(os.Stdout, "unmounting…")
		if err := srv.Unmount(); err != nil {
			return fmt.Errorf("unmount %s: %w (%s)", mountpoint, err, unmountHint(mountpoint))
		}
		<-done
	case <-ctx.Done():
		_ = srv.Unmount()
		<-done
	case <-done:
		// Somebody unmounted it from outside; that is a normal way to stop.
	}
	return nil
}

// unmountHint is what to tell the user, in their platform's own words.
//
// ⚠ It used to say `fusermount -u` unconditionally. On Windows that names a
// command the machine does not have, which is worse than saying nothing: the
// person reads it, it fails, and they conclude the mount is stuck.
func unmountHint(mountpoint string) string {
	if runtime.GOOS == "windows" {
		return "stop it with Ctrl-C in this window, or `net use " + mountpoint + " /delete`"
	}
	return "unmount with: fusermount -u " + mountpoint
}

// driveLetter reports whether the mountpoint is a bare Windows drive letter
// like `Z:`. Only meaningful on Windows; elsewhere a two-character path ending
// in a colon is a perfectly ordinary (if odd) directory name.
func driveLetter(p string) bool {
	if runtime.GOOS != "windows" || len(p) != 2 || p[1] != ':' {
		return false
	}
	c := p[0] | 0x20
	return c >= 'a' && c <= 'z'
}

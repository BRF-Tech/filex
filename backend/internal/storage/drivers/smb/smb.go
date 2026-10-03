// Package smb is a Storage Driver fronting an SMB2/3 share — a NAS, a Windows
// file server, a Samba box.
//
// # Where this sits in the matrix
//
// filex speaks a protocol in two directions: it can be reached AS one, and it
// can connect TO one. This is the inbound half of SMB. The outbound half — a
// real SMB SERVER inside filex — is a separate and far larger piece of work
// (MS-SMB2 from NEGOTIATE upward, with signing, encryption, oplocks and
// CHANGE_NOTIFY before Windows considers it working), and the shortest honest
// answer to "I want a drive letter" is `filex mount` or NFS rather than a
// half-built SMB server.
//
// # Why hirochachacha/go-smb2 and not the maintained fork
//
// The obvious choice is `cloudsoda/go-smb2`, which is the actively maintained
// fork and adds SMB 3.1.1 fixes and Windows security descriptors. ⚠ It pulls
// `cloudsoda/sddl`, which is **LGPL-3.0**, into the same package as its client
// — so linking it puts LGPL code inside filex's statically linked binary. filex
// is MIT and publishes its source, so the obligation is satisfiable today; it
// would stop being satisfiable the day filex ships closed-source.
//
// That is the same class of constraint filex already turned down once:
// `macos-fuse-t/go-smb2` was rejected for being AGPL, on the reasoning that a
// dependency must not relicense the product. The upstream `hirochachacha` v1.1.0
// is **BSD-2** and depends only on `geoffgarside/ber` and `x/crypto`, so it
// keeps filex's licence story unchanged.
//
// ⚠ The cost of that choice, stated rather than hidden: upstream has been quiet
// since 2021. What it does not have is Windows ACL reading and some newer
// dialect handling — neither of which this driver uses, because filex's
// permission model is its own ACL and a NAS share is reached with one account.
// If a NAS ever refuses this client for a dialect reason, the fork is the
// fallback and the licence question has to be answered first.
//
// # Every wait is bounded
//
// One session is shared; a session whose connection broke, or that a silence
// limit cut, is dropped and the next operation dials a new one (issue #75,
// timeout.go). ⚠ go-smb2 does not support multiple sessions on one TCP
// connection, so a second session means a second dial - worth knowing before
// anybody "improves" this into a pool.
package smb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	smb2 "github.com/hirochachacha/go-smb2"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

func init() {
	storage.Register("smb", func() storage.Driver { return &Driver{} })
}

// Driver is the SMB storage driver.
type Driver struct {
	host     string
	port     int
	share    string
	user     string
	password string
	domain   string
	root     string

	// policy bounds how long a server that does not answer is waited for
	// (timeout.go); what names the server in the error that says so.
	policy stall.Policy
	what   string

	// sessions holds the shared session; one caller dials at a time
	// (current).
	sessions stall.One[*session]
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "smb" }

// Init configures the driver. Required: host, share, user. The password may be
// empty for a guest share, which some NAS boxes still ship.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	d.host = strings.TrimSpace(storage.ConfigString(cfg, "host"))
	if v, ok := storage.ConfigInt(cfg["port"]); ok {
		d.port = v
	}
	if d.port == 0 {
		d.port = 445
	}
	d.share = strings.Trim(strings.TrimSpace(storage.ConfigString(cfg, "share", "share_name")), `\/`)
	d.user = storage.ConfigString(cfg, "user", "username")
	d.password = storage.ConfigString(cfg, "password")
	d.domain = storage.ConfigString(cfg, "domain", "workgroup")
	// ⚠ Backslashes are what a Windows user will type, and every path this
	// driver builds uses forward slashes. Normalised once, here, rather than in
	// each of the eleven places a path is joined.
	d.root = strings.Trim(strings.ReplaceAll(storage.ConfigString(cfg, "root", "base_path", "path"), `\`, "/"), "/")
	// The three timeout settings (timeout.go). `dial_timeout_s` is the
	// attempt timeout's old name, from when only the connect was bounded.
	attempt, _ := storage.ConfigLookup(cfg, "attempt_timeout_s", "dial_timeout_s")
	d.policy = stall.Settings{
		AttemptTimeout: attempt,
		MaxAttempts:    cfg["max_attempts"],
		TotalTimeout:   cfg["total_timeout_s"],
	}.Policy(defaults)
	d.what = "smb server " + d.addr()

	if d.host == "" || d.share == "" {
		return errors.New("smb: host and share required")
	}
	// ⚠ The library refuses an empty user outright ("Anonymous account is not
	// supported yet"), and its error says nothing about what to type. Caught
	// here so the operator reads the fix instead of the library's internals.
	if d.user == "" {
		return errors.New("smb: user required (use `guest` for a share with no account)")
	}
	return nil
}

// Capabilities — SMB does everything except presigned URLs, which are an
// HTTP idea with no equivalent on this wire.
func (d *Driver) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Read:   true,
		Range:  true,
		Write:  true,
		Move:   true,
		Copy:   true,
		Delete: true,
		Mkdir:  true,
	}
}

// join maps a filex-relative path onto the share.
//
// ⚠ Backslash-separated, because that is what SMB expects on the wire, and
// go-smb2 passes the name through. A forward-slash path reaches some servers
// and is rejected by others with a name-invalid error that reads like the file
// is missing.
func (d *Driver) join(p string) string {
	rel := strings.Trim(path.Clean("/"+strings.ReplaceAll(p, `\`, "/")), "/")
	if d.root != "" {
		rel = path.Join(d.root, rel)
	}
	rel = strings.Trim(rel, "/")
	if rel == "" {
		// The share root. go-smb2 wants "." rather than an empty string.
		return "."
	}
	return strings.ReplaceAll(rel, "/", `\`)
}

// slashed is a joined name with forward slashes, for path.Dir.
func slashed(name string) string { return strings.ReplaceAll(name, `\`, "/") }

// mapErr turns an SMB error into filex's vocabulary.
//
// ⚠ os.IsNotExist alone is not enough: go-smb2 wraps STATUS_OBJECT_NAME_NOT_FOUND
// and STATUS_OBJECT_PATH_NOT_FOUND in its own error type, and a missing PARENT
// directory produces the second one — which without this reads as an unexplained
// failure rather than "not there".
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
		return storage.ErrNotFound
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "OBJECT_NAME_NOT_FOUND"),
		strings.Contains(msg, "OBJECT_PATH_NOT_FOUND"),
		strings.Contains(msg, "NO_SUCH_FILE"):
		return storage.ErrNotFound
	case strings.Contains(msg, "ACCESS_DENIED"), strings.Contains(msg, "CANNOT_DELETE"):
		return storage.ErrReadOnly
	}
	return err
}

func objectOf(rel string, info os.FileInfo) storage.Object {
	obj := storage.Object{
		Path:  rel,
		Name:  info.Name(),
		Size:  info.Size(),
		Mtime: info.ModTime(),
		Kind:  storage.KindFile,
	}
	if info.IsDir() {
		obj.Kind = storage.KindDirectory
	}
	return obj
}

// List implements storage.Driver.
func (d *Driver) List(ctx context.Context, p string) ([]storage.Object, error) {
	var entries []os.FileInfo
	if err := d.run(ctx, readWork, func(fs *smb2.Share) error {
		var err error
		entries, err = fs.ReadDir(d.join(p))
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]storage.Object, 0, len(entries))
	for _, e := range entries {
		// SMB reports "." and ".." as real directory entries, and a folder that
		// contains itself is followed by every tree walker in filex until it
		// runs out of something.
		//
		// ⚠ go-smb2 already drops them (client.go:1977) — measured, this guard
		// never fires against Samba 4.12. Kept because it costs a string
		// compare and the day somebody swaps in the cloudsoda fork nobody will
		// re-check this; it is a guard, not the fix, and the comment says so
		// rather than claiming a bug it does not prevent.
		if e.Name() == "." || e.Name() == ".." {
			continue
		}
		out = append(out, objectOf(path.Join(p, e.Name()), e))
	}
	return out, nil
}

// Stat implements storage.Driver.
func (d *Driver) Stat(ctx context.Context, p string) (storage.Object, error) {
	var info os.FileInfo
	if err := d.run(ctx, readWork, func(fs *smb2.Share) error {
		var err error
		info, err = fs.Stat(d.join(p))
		return err
	}); err != nil {
		return storage.Object{}, err
	}
	obj := objectOf(p, info)
	obj.Name = path.Base("/" + strings.Trim(p, "/"))
	if obj.Name == "/" || obj.Name == "." {
		obj.Name = d.share
	}
	return obj, nil
}

// Read implements storage.Driver.
func (d *Driver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	return d.open(ctx, p, 0, -1)
}

// ReadRange implements storage.RangeReader.
//
// smb2.File is seekable — the protocol reads at an explicit offset — so nothing
// before off crosses the network. That is what makes a NAS usable as a backing
// store for video seeking and for `filex mount`.
func (d *Driver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, fmt.Errorf("smb: negative range offset %d", off)
	}
	rc, err := d.open(ctx, p, off, length)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		_ = rc.Close()
		return storage.EmptyReadCloser(), nil
	}
	if length > 0 {
		return storage.LimitReadCloser(rc, length), nil
	}
	return rc, nil
}

// readBuffer is how much one Read asks the server for: one READ request per
// 32 KB is a round trip each, which a slow link pays for in every byte.
const readBuffer = 1 << 20

// open opens p for reading at off (opening is repeatable). length is how much
// the caller will read, -1 for all of it.
//
// ⚠ The download is a fileReader: every Read is watched, and only while it
// waits on the network. The *smb2.File is not handed out whole, because its
// WriteTo (what io.Copy picks) moves the entire file in one call - including
// while it waits on the caller's slow writer, which a watch would read as a
// silent server.
func (d *Driver) open(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	var (
		f     *smb2.File
		owner *session
	)
	err := d.runOn(ctx, readWork, func(fs *smb2.Share, s *session) error {
		file, err := fs.Open(d.join(p))
		if err != nil {
			return err
		}
		if err := storage.SeekOpened(file, off); err != nil {
			return err
		}
		f, owner = file, s
		return nil
	})
	if err != nil {
		return nil, err
	}
	return storage.ReadAhead(&fileReader{d: d, s: owner, f: f, limit: d.policy.AttemptTimeout}, readBuffer, length), nil
}

// Write implements storage.Writer. Not sent again once it reached the server:
// r is the caller's stream.
func (d *Driver) Write(ctx context.Context, p string, r io.Reader, _ int64) error {
	return d.run(ctx, sendWork, func(fs *smb2.Share) error {
		abs := d.join(p)
		if err := d.mkdirAll(fs, path.Dir(slashed(abs))); err != nil {
			return err
		}
		f, err := fs.Create(abs)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

// mkdirAll creates a directory chain. go-smb2's Mkdir is one level only —
// unlike the SFTP client's MkdirAll — so a write two folders deep into an empty
// share fails with a path-not-found that reads as a permission problem.
func (d *Driver) mkdirAll(fs *smb2.Share, dir string) error {
	dir = strings.Trim(slashed(dir), "/")
	if dir == "" || dir == "." {
		return nil
	}
	built := ""
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if built == "" {
			built = seg
		} else {
			built += "/" + seg
		}
		name := strings.ReplaceAll(built, "/", `\`)
		if info, err := fs.Stat(name); err == nil {
			if !info.IsDir() {
				return fmt.Errorf("smb: %s exists and is not a directory", built)
			}
			continue
		}
		if err := fs.Mkdir(name, 0o755); err != nil {
			// A racing writer may have created it between the Stat and here.
			if info, serr := fs.Stat(name); serr == nil && info.IsDir() {
				continue
			}
			return err
		}
	}
	return nil
}

// Move implements storage.Mover.
func (d *Driver) Move(ctx context.Context, src, dst string) error {
	return d.run(ctx, changeWork, func(fs *smb2.Share) error {
		to := d.join(dst)
		if err := d.mkdirAll(fs, path.Dir(slashed(to))); err != nil {
			return err
		}
		return fs.Rename(d.join(src), to)
	})
}

// Copy implements storage.Copier — read and write back, the same as SFTP does.
//
// ⚠ SMB has a server-side copy (FSCTL_SRV_COPYCHUNK); go-smb2 uses it when
// io.Copy hands one of its files to another on the same share, and streams
// through this host when the server refuses. Either way it is watched like an
// upload.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	return d.run(ctx, sendWork, func(fs *smb2.Share) error {
		in, err := fs.Open(d.join(src))
		if err != nil {
			return err
		}
		defer in.Close()
		to := d.join(dst)
		if err := d.mkdirAll(fs, path.Dir(slashed(to))); err != nil {
			return err
		}
		out, err := fs.Create(to)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

// Delete implements storage.Deleter.
//
// ⚠ RemoveAll, not Remove. SMB refuses to delete a non-empty directory, and
// filex's trash moves a folder wholesale — a Delete that only worked on files
// would leave the folder behind on every purge.
func (d *Driver) Delete(ctx context.Context, p string) error {
	return d.run(ctx, changeWork, func(fs *smb2.Share) error {
		abs := d.join(p)
		if info, err := fs.Stat(abs); err == nil && info.IsDir() {
			return fs.RemoveAll(abs)
		}
		return fs.Remove(abs)
	})
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(ctx context.Context, p string) error {
	return d.run(ctx, readWork, func(fs *smb2.Share) error {
		return d.mkdirAll(fs, slashed(d.join(p)))
	})
}

// SetMtime implements storage.Toucher.
func (d *Driver) SetMtime(ctx context.Context, p string, mtime time.Time) error {
	return d.run(ctx, readWork, func(fs *smb2.Share) error {
		return fs.Chtimes(d.join(p), mtime, mtime)
	})
}

// Close releases the session - called on shutdown. It logs off within the
// attempt timeout and then closes the connection, so a silent server cannot
// hold the shutdown.
func (d *Driver) Close() error {
	s, held := d.sessions.Take()
	if !held {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.policy.AttemptTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, s.act.Cut)
	_ = s.sess.WithContext(ctx).Logoff()
	stop()
	s.close()
	return nil
}

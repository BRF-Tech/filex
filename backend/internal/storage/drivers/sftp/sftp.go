// Package sftp is a Storage Driver fronting an SSH/SFTP server.
//
// Connection lazy: the underlying SSH session is established on first
// operation. A single shared session is reused; a session whose connection
// broke, or that a silence limit cut, is dropped and the next operation
// dials a new one (timeout.go). Before issue #75 nothing dropped it: a dead
// session was handed out again for good.
//
// Every wait on the server is bounded (issue #75, timeout.go).
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/brf-tech/filex/backend/internal/regfile"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

func init() {
	storage.Register("sftp", func() storage.Driver { return &Driver{} })
}

// Driver is the SFTP storage driver.
type Driver struct {
	host     string
	port     int
	user     string
	password string
	keyPEM   string
	root     string

	// Host-key verification config (see Init):
	//   known_hosts            — path to an OpenSSH known_hosts file (strict).
	//   host_key               — a single pinned public key (authorized_keys
	//                            or known_hosts line form) → FixedHostKey.
	//   insecure_skip_host_key — explicit opt-out (legacy behaviour).
	// When none are set the driver defaults to trust-on-first-use against
	// ~/.filex/known_hosts.
	knownHostsPath  string
	hostKeyPin      string
	insecureHostKey bool

	// skipped: the remote named pipes, sockets and devices already reported
	// (issue #38 — the SFTP server opens a pipe for us and waits on it just
	// the same, holding our request with it).
	skipped regfile.Skipped

	// policy bounds how long a server that does not answer is waited for
	// (timeout.go); what names the server in the error that says so.
	policy stall.Policy
	what   string

	// sessions holds the shared session; one caller dials at a time
	// (current).
	sessions stall.One[*session]
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "sftp" }

// Init configures the driver. Required: host, user, root; one of password,
// private_key or key_path must be set. Optional: port (default 22).
//
// The config keys are declared in descriptor.go — keep the two in step,
// there is a test that fails when they drift. Legacy spellings are read
// through the descriptor's aliases ("username" for user, "base_path" /
// "remote_path" for root) so rows written by older surfaces keep working.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	d.host = storage.ConfigString(cfg, "host")
	if v, ok := storage.ConfigInt(cfg["port"]); ok {
		d.port = v
	}
	if d.port == 0 {
		d.port = 22
	}
	d.user = storage.ConfigString(cfg, "user", "username")
	d.password = storage.ConfigString(cfg, "password")
	d.keyPEM = storage.ConfigString(cfg, "private_key")
	d.root = storage.ConfigString(cfg, "root", "base_path", "remote_path")
	d.knownHostsPath = storage.ConfigString(cfg, "known_hosts")
	d.hostKeyPin = storage.ConfigString(cfg, "host_key")
	d.insecureHostKey, _ = cfg["insecure_skip_host_key"].(bool)
	d.policy = stall.Settings{
		AttemptTimeout: cfg["attempt_timeout_s"],
		MaxAttempts:    cfg["max_attempts"],
		TotalTimeout:   cfg["total_timeout_s"],
	}.Policy(defaults)
	d.what = "sftp server " + d.addr()
	if d.root == "" {
		d.root = "/"
	}
	if d.host == "" || d.user == "" {
		return errors.New("sftp: host and user required")
	}
	// key_path points at a key file on the server; private_key carries the
	// PEM itself. The file is read once here so a bad path fails loudly at
	// configure time instead of on the first listing.
	if keyPath := storage.ConfigString(cfg, "key_path"); d.keyPEM == "" && keyPath != "" {
		pem, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("sftp: read key_path: %w", err)
		}
		d.keyPEM = string(pem)
	}
	if d.password == "" && d.keyPEM == "" {
		return errors.New("sftp: either password, private_key or key_path required")
	}
	return nil
}

// Capabilities — SFTP supports everything except Presign.
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

// hostKeyCallback picks the SSH host-key verification strategy from config.
//
// Precedence: explicit insecure opt-out → pinned single key → known_hosts
// file (strict) → trust-on-first-use against ~/.filex/known_hosts. The TOFU
// default means a brand-new storage records the server's key on the first
// connection and rejects any later key change (the classic MITM signal),
// replacing the previous blanket ssh.InsecureIgnoreHostKey().
func (d *Driver) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if d.insecureHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if strings.TrimSpace(d.hostKeyPin) != "" {
		pk, err := parsePinnedKey(d.hostKeyPin)
		if err != nil {
			return nil, fmt.Errorf("parse host_key: %w", err)
		}
		return ssh.FixedHostKey(pk), nil
	}
	khPath := d.knownHostsPath
	if khPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home for known_hosts: %w", err)
		}
		khPath = filepath.Join(home, ".filex", "known_hosts")
	}
	return tofuHostKeyCallback(khPath)
}

// parsePinnedKey accepts either an authorized_keys line ("ssh-ed25519 AAAA…")
// or a known_hosts line ("host ssh-ed25519 AAAA…") and returns the key.
func parsePinnedKey(s string) (ssh.PublicKey, error) {
	if pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s)); err == nil {
		return pk, nil
	}
	_, _, pk, _, _, err := ssh.ParseKnownHosts([]byte(s))
	if err != nil {
		return nil, err
	}
	return pk, nil
}

// tofuHostKeyCallback verifies against khPath, learning unknown hosts on
// first contact and persisting them. A key that exists but differs is
// rejected (the file is never silently overwritten).
func tofuHostKeyCallback(khPath string) (ssh.HostKeyCallback, error) {
	if err := os.MkdirAll(filepath.Dir(khPath), 0o700); err != nil {
		return nil, err
	}
	// Ensure the file exists so knownhosts.New can parse it.
	if f, err := os.OpenFile(khPath, os.O_CREATE, 0o600); err == nil {
		_ = f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	verify, err := knownhosts.New(khPath)
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		// len(Want)==0 → host not in the file yet → trust on first use.
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			return appendKnownHost(khPath, hostname, remote, key)
		}
		// Mismatch (possible MITM) or other error → reject the connection.
		return err
	}, nil
}

// appendKnownHost writes a learned host key to the known_hosts file.
func appendKnownHost(khPath, hostname string, remote net.Addr, key ssh.PublicKey) error {
	f, err := os.OpenFile(khPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	addrs := []string{knownhosts.Normalize(hostname)}
	if remote != nil {
		if rn := knownhosts.Normalize(remote.String()); rn != addrs[0] {
			addrs = append(addrs, rn)
		}
	}
	if _, err := f.WriteString(knownhosts.Line(addrs, key) + "\n"); err != nil {
		return err
	}
	return nil
}

func (d *Driver) join(p string) string {
	return path.Join(d.root, strings.TrimLeft(path.Clean("/"+p), "/"))
}

// List implements storage.Driver.
func (d *Driver) List(ctx context.Context, p string) ([]storage.Object, error) {
	abs := d.join(p)
	var entries []os.FileInfo
	if err := d.run(ctx, readWork, func(cl *sftp.Client) error {
		var err error
		entries, err = cl.ReadDir(abs)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]storage.Object, 0, len(entries))
	for _, e := range entries {
		if regfile.Special(e.Mode()) {
			d.skipped.Report("sftp", d.root, path.Join(abs, e.Name()), e.Mode())
			continue
		}
		obj := storage.Object{
			Path:  path.Join(p, e.Name()),
			Name:  e.Name(),
			Size:  e.Size(),
			Mtime: e.ModTime(),
		}
		obj.Kind = kindOf(e.Mode())
		if obj.Kind == storage.KindSymlink {
			obj.Size = 0
			obj.Metadata = map[string]string{storage.MetaLinkState: storage.LinkUnresolved}
		}
		out = append(out, obj)
	}
	return out, nil
}

// kindOf classifies one SFTP directory entry.
//
// ⚠⚠ There used to be no symlink branch here at all: the code asked IsDir and
// called everything else a file, so a remote DIRECTORY symlink came back as
// storage.KindFile. That is not an approximation, it is a flat lie — the
// explorer offered it as a downloadable file, the download opened a directory
// and failed, and the catalogue walk wrote a file row for something that has
// no bytes.
//
// ⚠ SFTP READDIR returns LSTAT attributes, so a link is visible here and is
// reported as one. It is NOT resolved to its target the way the local driver
// resolves an in-root link: doing so would cost a round trip per link, and —
// the deciding reason — this driver has no containment story. filex's boundary
// on a remote host is the SSH account's own permissions, so relabelling a
// remote directory link as KindDirectory would make the catalogue walk descend
// through it, outside the configured root, with nothing to stop it. That is
// precisely the escape being closed in the local driver, and it is not worth
// opening here to save a click. See storage.LinkUnresolved.
func kindOf(mode fs.FileMode) storage.ObjectKind {
	switch {
	case mode&fs.ModeSymlink != 0:
		return storage.KindSymlink
	case mode.IsDir():
		return storage.KindDirectory
	default:
		return storage.KindFile
	}
}

// Stat implements storage.Driver.
func (d *Driver) Stat(ctx context.Context, p string) (storage.Object, error) {
	var info os.FileInfo
	if err := d.run(ctx, readWork, func(cl *sftp.Client) error {
		var err error
		info, err = cl.Stat(d.join(p))
		return err
	}); err != nil {
		return storage.Object{}, err
	}
	// List does not show a named pipe, socket or device, so Stat does not
	// know one either (issue #38).
	if regfile.Special(info.Mode()) {
		return storage.Object{}, storage.ErrNotFound
	}
	obj := storage.Object{
		Path:  p,
		Name:  path.Base(p),
		Size:  info.Size(),
		Mtime: info.ModTime(),
	}
	if info.IsDir() {
		obj.Kind = storage.KindDirectory
	} else {
		obj.Kind = storage.KindFile
	}
	return obj, nil
}

// Read implements storage.Driver.
func (d *Driver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	return d.open(ctx, p, 0, -1)
}

// ReadRange implements storage.RangeReader. sftp.File is seekable (the
// protocol reads at an explicit offset), so nothing before off is
// transferred. A seek past EOF is accepted and the first Read reports
// io.EOF, per the contract.
func (d *Driver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, fmt.Errorf("sftp: negative range offset %d", off)
	}
	if length == 0 {
		// Asked for nothing: the file is still opened once, so a missing one
		// answers ErrNotFound as it always did.
		rc, err := d.open(ctx, p, 0, 0)
		if err != nil {
			return nil, err
		}
		_ = rc.Close()
		return storage.EmptyReadCloser(), nil
	}
	rc, err := d.open(ctx, p, off, length)
	if err != nil {
		return nil, err
	}
	if length > 0 {
		return storage.LimitReadCloser(rc, length), nil
	}
	return rc, nil
}

// readBuffer is how much one Read asks the server for: the SFTP client splits
// a large Read into concurrent requests, which is what keeps a download over
// a slow link from paying one round trip per 32 KB.
const readBuffer = 1 << 20

// open opens p for reading at off, through the policy (opening is
// repeatable). length is how much the caller will read, -1 for all of it.
//
// ⚠ The download itself is a fileReader: every Read is watched, and only
// while it waits on the network. The *sftp.File it wraps is never handed out
// whole, because its WriteTo (what io.Copy picks) moves the entire file in one
// call - including while it waits on the caller's slow writer, which a watch
// would read as a silent server.
func (d *Driver) open(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	var (
		f     *sftp.File
		owner *session
	)
	err := d.runOn(ctx, readWork, func(s *session) error {
		file, err := s.client.Open(d.join(p))
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

// Write implements storage.Writer. Not sent again once it reached the
// server: r is the caller's stream (see run). The caller's stream is read
// only until Write returns, even when it returns early on its context.
func (d *Driver) Write(ctx context.Context, p string, r io.Reader, _ int64) error {
	g := &gateReader{r: r}
	defer g.shut()
	return d.run(ctx, sendWork, func(cl *sftp.Client) error {
		abs := d.join(p)
		_ = cl.MkdirAll(path.Dir(abs))
		f, err := cl.Create(abs)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, g)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

// SetMtime implements storage.Toucher.
func (d *Driver) SetMtime(ctx context.Context, p string, mtime time.Time) error {
	return d.run(ctx, readWork, func(cl *sftp.Client) error {
		return cl.Chtimes(d.join(p), mtime, mtime)
	})
}

// Move implements storage.Mover.
func (d *Driver) Move(ctx context.Context, src, dst string) error {
	return d.run(ctx, changeWork, func(cl *sftp.Client) error {
		a := d.join(src)
		b := d.join(dst)
		_ = cl.MkdirAll(path.Dir(b))
		return cl.Rename(a, b)
	})
}

// Copy implements storage.Copier - naive download/upload, both directions
// through this host, so it is watched like an upload.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	return d.run(ctx, sendWork, func(cl *sftp.Client) error {
		in, err := cl.Open(d.join(src))
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := cl.Create(d.join(dst))
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
func (d *Driver) Delete(ctx context.Context, p string) error {
	return d.run(ctx, changeWork, func(cl *sftp.Client) error {
		abs := d.join(p)
		if err := cl.Remove(abs); err != nil {
			// Maybe a directory.
			return cl.RemoveDirectory(abs)
		}
		return nil
	})
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(ctx context.Context, p string) error {
	return d.run(ctx, readWork, func(cl *sftp.Client) error {
		return cl.MkdirAll(d.join(p))
	})
}

// Close releases the underlying SSH session - called on shutdown. The
// connection is cut first, so a silent server cannot hold the shutdown.
func (d *Driver) Close() error {
	s, held := d.sessions.Take()
	if held {
		s.close()
	}
	return nil
}

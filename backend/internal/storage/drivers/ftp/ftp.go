// Package ftp is a Storage Driver fronting a plain FTP (or FTPS) server.
//
// Connection lazy: the underlying control connection is established on
// first operation. A single shared connection is reused; if a server-side
// error indicates a dead session, the next operation will redial. FTP is
// not safe for concurrent use on a single connection so all calls go
// through one lock — which a caller whose context ends stops waiting for.
//
// Every wait on the server is bounded (issue #73, timeout.go): before, only
// the TCP connect was, and a server that stopped answering held the lock —
// and so the whole storage — forever.
package ftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"path"
	"strings"
	"sync"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

func init() {
	storage.Register("ftp", func() storage.Driver { return &Driver{} })
}

// Driver is the FTP / FTPS storage driver.
type Driver struct {
	host     string
	port     int
	user     string
	password string
	root     string
	tls      bool // FTPS (explicit AUTH TLS)
	passive  bool // PASV (default true)

	// policy bounds how long a server that does not answer is waited for
	// (timeout.go); what names the server in the error that says so.
	policy stall.Policy
	what   string

	// sem is the lock (see lock); conn the session, ctrl its control
	// connection's silence guard. All three under the lock.
	semOnce sync.Once
	sem     chan struct{}
	conn    *goftp.ServerConn
	ctrl    *stall.Conn
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "ftp" }

// Init configures the driver. Required: host, user, password, root.
// Optional: port (default 21), tls (default false), passive (default true),
// and the three timeout settings (timeout.go).
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
		d.port = 21
	}
	d.user = storage.ConfigString(cfg, "user", "username")
	d.password = storage.ConfigString(cfg, "password")
	d.root = storage.ConfigString(cfg, "root", "base_path", "remote_path")
	if d.root == "" {
		d.root = "/"
	}
	if v, ok := cfg["tls"].(bool); ok {
		d.tls = v
	}
	// passive defaults to true unless explicitly set false.
	d.passive = true
	if v, ok := cfg["passive"].(bool); ok {
		d.passive = v
	}
	d.policy = stall.Settings{
		AttemptTimeout: cfg["attempt_timeout_s"],
		MaxAttempts:    cfg["max_attempts"],
		TotalTimeout:   cfg["total_timeout_s"],
	}.Policy(defaults)
	d.what = "ftp server " + d.addr()
	if d.host == "" || d.user == "" || d.password == "" {
		return errors.New("ftp: host, user and password required")
	}
	return nil
}

// Capabilities — FTP supports read/write/move/copy/delete/mkdir.
// Presign, multipart and watcher are not available.
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

func (d *Driver) join(p string) string {
	return path.Join(d.root, strings.TrimLeft(path.Clean("/"+p), "/"))
}

// translateErr maps server-side FTP errors to storage sentinel errors.
// FTP 550 "File unavailable" is the canonical not-found code.
//
// ⚠ Only for the server's ANSWERS. A transport error's text carries addresses
// ("127.0.0.1:35501"), and the substring sniff below would read a port with
// 550 in it as "not found"; fail sends those to "unavailable" first.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	var pe *textproto.Error
	if errors.As(err, &pe) {
		if pe.Code == 550 {
			return storage.ErrNotFound
		}
	}
	// Some servers wrap the response in a plain string — fall back to
	// substring sniff for the common phrasings.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "550") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist") {
		return storage.ErrNotFound
	}
	if errors.Is(err, os.ErrNotExist) {
		return storage.ErrNotFound
	}
	return err
}

// dropConnLocked evicts the cached session so the next call redials.
// Caller MUST hold the lock.
func (d *Driver) dropConnLocked() {
	if d.conn != nil {
		_ = d.conn.Quit()
		d.conn, d.ctrl = nil, nil
	}
}

// isTransport returns true when the error looks like a dead control connection
// — in that case we evict the cached session so the next op redials.
func isTransport(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed")
}

// List implements storage.Driver.
func (d *Driver) List(ctx context.Context, p string) ([]storage.Object, error) {
	var entries []*goftp.Entry
	err := d.run(ctx, true, func(c *goftp.ServerConn) error {
		var err error
		entries, err = c.List(d.join(p))
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]storage.Object, 0, len(entries))
	for _, e := range entries {
		// Skip the "." and ".." pseudo-entries some servers return.
		if e.Name == "." || e.Name == ".." || e.Name == "" {
			continue
		}
		obj := storage.Object{
			Path:  path.Join(p, e.Name),
			Name:  e.Name,
			Size:  int64(e.Size),
			Mtime: e.Time,
		}
		switch e.Type {
		case goftp.EntryTypeFolder:
			obj.Kind = storage.KindDirectory
		case goftp.EntryTypeLink:
			obj.Kind = storage.KindSymlink
		default:
			obj.Kind = storage.KindFile
		}
		out = append(out, obj)
	}
	return out, nil
}

// Stat implements storage.Driver.
//
// FTP has no portable stat operation. We try GetEntry first (RFC 3659
// MLST — supported by modern servers) and fall back to listing the parent
// directory and matching the basename.
func (d *Driver) Stat(ctx context.Context, p string) (storage.Object, error) {
	var found *goftp.Entry
	err := d.run(ctx, true, func(c *goftp.ServerConn) error {
		abs := d.join(p)
		if e, err := c.GetEntry(abs); err == nil && e != nil {
			found = e
			return nil
		}
		// Fallback: list parent and find self.
		base := path.Base(abs)
		entries, err := c.List(path.Dir(abs))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Name == base {
				found = e
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return storage.Object{}, err
	}
	if found == nil {
		return storage.Object{}, storage.ErrNotFound
	}
	obj := storage.Object{
		Path:  p,
		Name:  path.Base(p),
		Size:  int64(found.Size),
		Mtime: found.Time,
	}
	switch found.Type {
	case goftp.EntryTypeFolder:
		obj.Kind = storage.KindDirectory
	case goftp.EntryTypeLink:
		obj.Kind = storage.KindSymlink
	default:
		obj.Kind = storage.KindFile
	}
	return obj, nil
}

// ftpReadCloser couples a ftp.Response (which is io.ReadCloser) to the
// driver lock. The data connection runs in parallel with the control
// channel, but FTP serializes data transfers so the lock must be held
// for the lifetime of the read.
type ftpReadCloser struct {
	r      io.ReadCloser
	d      *Driver
	closed bool
}

func (rc *ftpReadCloser) Read(p []byte) (int, error) { return rc.r.Read(p) }

func (rc *ftpReadCloser) Close() error {
	if rc.closed {
		return nil
	}
	rc.closed = true
	err := rc.r.Close()
	if err != nil {
		// Closing before the server finished sending (a bounded range read,
		// or a client that hung up) leaves the transfer-complete reply
		// unread on the control channel — the next command would read it
		// as its own answer. Drop the session so the next op redials.
		rc.d.dropConnLocked()
	}
	rc.d.unlock()
	return err
}

// Read implements storage.Driver. The returned ReadCloser holds the
// driver lock until Close — keep reads short or buffer the body if
// long-lived locks are a concern.
func (d *Driver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	return d.retr(ctx, p, 0)
}

// ReadRange implements storage.RangeReader via the REST command
// (`RetrFrom`), so the server skips the first off bytes instead of
// filex reading and discarding them. RetrFrom requires the server to
// answer 350 to REST — a server without REST support errors here rather
// than quietly restarting at byte 0.
//
// ⚠ Unlike local/S3, an offset past EOF is server-dependent (many answer
// 550 → ErrNotFound instead of an empty transfer). Callers that know the
// size should not ask for it; the download seeker never does.
func (d *Driver) ReadRange(ctx context.Context, p string, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, fmt.Errorf("ftp: negative range offset %d", off)
	}
	if length == 0 {
		return storage.EmptyReadCloser(), nil
	}
	rc, err := d.retr(ctx, p, uint64(off))
	if err != nil {
		return nil, err
	}
	return storage.LimitReadCloser(rc, length), nil
}

// retr opens a download and returns it holding the lock.
func (d *Driver) retr(ctx context.Context, p string, off uint64) (io.ReadCloser, error) {
	if err := d.lock(ctx); err != nil {
		return nil, err
	}
	var resp *goftp.Response
	err := d.runLocked(ctx, true, func(c *goftp.ServerConn) error {
		var err error
		resp, err = c.RetrFrom(d.join(p), off)
		return err
	})
	if err != nil {
		d.unlock()
		return nil, err
	}
	return &ftpReadCloser{r: resp, d: d}, nil
}

// mkdirAll creates each missing path component. FTP MKD does not have
// the recursive flag.
func (d *Driver) mkdirAll(c *goftp.ServerConn, abs string) error {
	abs = path.Clean(abs)
	if abs == "" || abs == "/" || abs == "." {
		return nil
	}
	// MakeDir on an existing directory typically returns 550 — that's fine.
	parts := strings.Split(strings.Trim(abs, "/"), "/")
	cur := ""
	if strings.HasPrefix(abs, "/") {
		cur = "/"
	}
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		cur = path.Join(cur, seg)
		if !strings.HasPrefix(cur, "/") && strings.HasPrefix(abs, "/") {
			cur = "/" + cur
		}
		_ = c.MakeDir(cur) // ignore "already exists" failures
	}
	return nil
}

// Write implements storage.Writer. Not sent again once it reached the
// server: r is the caller's stream (see run).
func (d *Driver) Write(ctx context.Context, p string, r io.Reader, _ int64) error {
	return d.run(ctx, false, func(c *goftp.ServerConn) error {
		abs := d.join(p)
		_ = d.mkdirAll(c, path.Dir(abs))
		return d.stor(c, abs, r)
	})
}

// Move implements storage.Mover via RNFR + RNTO (the library's Rename
// wraps both commands).
func (d *Driver) Move(ctx context.Context, src, dst string) error {
	return d.run(ctx, false, func(c *goftp.ServerConn) error {
		b := d.join(dst)
		_ = d.mkdirAll(c, path.Dir(b))
		return c.Rename(d.join(src), b)
	})
}

// Copy implements storage.Copier. FTP has no native server-side copy
// command, so we stream the file through the control host: download the
// source on one data connection, upload to the destination on the next.
func (d *Driver) Copy(ctx context.Context, src, dst string) error {
	return d.run(ctx, false, func(c *goftp.ServerConn) error {
		resp, err := c.Retr(d.join(src))
		if err != nil {
			return err
		}
		// Drain the data channel into a temp file so we can release it before
		// opening the upload data connection — a single FTP control session
		// only allows one data transfer at a time.
		tmp, err := os.CreateTemp("", "ftpcopy-*")
		if err != nil {
			_ = resp.Close()
			return err
		}
		defer func() {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}()
		if _, err := io.Copy(tmp, resp); err != nil {
			_ = resp.Close()
			return err
		}
		if err := resp.Close(); err != nil {
			return err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		abs := d.join(dst)
		_ = d.mkdirAll(c, path.Dir(abs))
		return d.stor(c, abs, tmp)
	})
}

// Delete implements storage.Deleter — tries DELE first, falls back to RMD.
func (d *Driver) Delete(ctx context.Context, p string) error {
	return d.run(ctx, false, func(c *goftp.ServerConn) error {
		abs := d.join(p)
		err := c.Delete(abs)
		if err == nil {
			return nil
		}
		// Maybe a directory.
		if rmErr := c.RemoveDir(abs); rmErr == nil {
			return nil
		}
		return err
	})
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(ctx context.Context, p string) error {
	return d.run(ctx, true, func(c *goftp.ServerConn) error {
		return d.mkdirAll(c, d.join(p))
	})
}

// Close releases the underlying FTP session — called on shutdown. It waits
// for a download that holds the session to be closed.
func (d *Driver) Close() error {
	if err := d.lock(context.Background()); err != nil {
		return err
	}
	defer d.unlock()
	d.dropConnLocked()
	return nil
}

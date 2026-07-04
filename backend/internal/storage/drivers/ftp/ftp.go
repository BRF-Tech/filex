// Package ftp is a Storage Driver fronting a plain FTP (or FTPS) server.
//
// Connection lazy: the underlying control connection is established on
// first operation. A single shared connection is reused; if a server-side
// error indicates a dead session, the next operation will redial. FTP is
// not safe for concurrent use on a single connection so all calls go
// through a mutex.
package ftp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/storage"
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

	mu   sync.Mutex
	conn *goftp.ServerConn
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "ftp" }

// Init configures the driver. Required: host, user, password.
// Optional: port (default 21), root (default "/"), tls (default false),
// passive (default true).
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	d.host, _ = cfg["host"].(string)
	if v, ok := storage.ConfigInt(cfg["port"]); ok {
		d.port = v
	}
	if d.port == 0 {
		d.port = 21
	}
	d.user, _ = cfg["user"].(string)
	d.password, _ = cfg["password"].(string)
	d.root, _ = cfg["root"].(string)
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
		Write:  true,
		Move:   true,
		Copy:   true,
		Delete: true,
		Mkdir:  true,
	}
}

// connect lazily dials the FTP server, logs in and caches the session.
// Caller MUST hold d.mu.
func (d *Driver) connectLocked() (*goftp.ServerConn, error) {
	if d.conn != nil {
		// Cheap liveness probe — NoOp is one round trip.
		if err := d.conn.NoOp(); err == nil {
			return d.conn, nil
		}
		_ = d.conn.Quit()
		d.conn = nil
	}
	addr := net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port))
	opts := []goftp.DialOption{
		goftp.DialWithTimeout(15 * time.Second),
		goftp.DialWithDisabledEPSV(!d.passive),
	}
	if d.tls {
		opts = append(opts, goftp.DialWithExplicitTLS(&tls.Config{
			ServerName:         d.host,
			InsecureSkipVerify: false,
		}))
	}
	c, err := goftp.Dial(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("ftp: dial: %w", err)
	}
	if err := c.Login(d.user, d.password); err != nil {
		_ = c.Quit()
		return nil, fmt.Errorf("ftp: login: %w", err)
	}
	d.conn = c
	return c, nil
}

func (d *Driver) join(p string) string {
	return path.Join(d.root, strings.TrimLeft(path.Clean("/"+p), "/"))
}

// translateErr maps server-side FTP errors to storage sentinel errors.
// FTP 550 "File unavailable" is the canonical not-found code.
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

// dropConn evicts the cached session so the next call redials.
// Caller MUST hold d.mu.
func (d *Driver) dropConnLocked() {
	if d.conn != nil {
		_ = d.conn.Quit()
		d.conn = nil
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
func (d *Driver) List(_ context.Context, p string) ([]storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return nil, err
	}
	abs := d.join(p)
	entries, err := c.List(abs)
	if err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		return nil, translateErr(err)
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
func (d *Driver) Stat(_ context.Context, p string) (storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return storage.Object{}, err
	}
	abs := d.join(p)

	if e, err := c.GetEntry(abs); err == nil && e != nil {
		obj := storage.Object{
			Path:  p,
			Name:  path.Base(p),
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
		return obj, nil
	}

	// Fallback: list parent and find self.
	parent := path.Dir(abs)
	base := path.Base(abs)
	entries, err := c.List(parent)
	if err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		return storage.Object{}, translateErr(err)
	}
	for _, e := range entries {
		if e.Name == base {
			obj := storage.Object{
				Path:  p,
				Name:  base,
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
			return obj, nil
		}
	}
	return storage.Object{}, storage.ErrNotFound
}

// ftpReadCloser couples a ftp.Response (which is io.ReadCloser) to the
// driver mutex. The data connection runs in parallel with the control
// channel, but FTP serializes data transfers so the mutex must be held
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
	rc.d.mu.Unlock()
	return err
}

// Read implements storage.Driver. The returned ReadCloser holds the
// driver mutex until Close — keep reads short or buffer the body if
// long-lived locks are a concern.
func (d *Driver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	d.mu.Lock()
	c, err := d.connectLocked()
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	resp, err := c.Retr(d.join(p))
	if err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		d.mu.Unlock()
		return nil, translateErr(err)
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

// Write implements storage.Writer.
func (d *Driver) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return err
	}
	abs := d.join(p)
	_ = d.mkdirAll(c, path.Dir(abs))
	if err := c.Stor(abs, r); err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		return translateErr(err)
	}
	return nil
}

// Move implements storage.Mover via RNFR + RNTO (the library's Rename
// wraps both commands).
func (d *Driver) Move(_ context.Context, src, dst string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return err
	}
	a := d.join(src)
	b := d.join(dst)
	_ = d.mkdirAll(c, path.Dir(b))
	if err := c.Rename(a, b); err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		return translateErr(err)
	}
	return nil
}

// Copy implements storage.Copier. FTP has no native server-side copy
// command, so we stream the file through the control host: download the
// source on one data connection, upload to the destination on the next.
func (d *Driver) Copy(_ context.Context, src, dst string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return err
	}
	resp, err := c.Retr(d.join(src))
	if err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		return translateErr(err)
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
		return translateErr(err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	abs := d.join(dst)
	_ = d.mkdirAll(c, path.Dir(abs))
	if err := c.Stor(abs, tmp); err != nil {
		if isTransport(err) {
			d.dropConnLocked()
		}
		return translateErr(err)
	}
	return nil
}

// Delete implements storage.Deleter — tries DELE first, falls back to RMD.
func (d *Driver) Delete(_ context.Context, p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return err
	}
	abs := d.join(p)
	if err := c.Delete(abs); err != nil {
		// Maybe a directory.
		if rmErr := c.RemoveDir(abs); rmErr == nil {
			return nil
		}
		if isTransport(err) {
			d.dropConnLocked()
		}
		return translateErr(err)
	}
	return nil
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(_ context.Context, p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.connectLocked()
	if err != nil {
		return err
	}
	return d.mkdirAll(c, d.join(p))
}

// Close releases the underlying FTP session — called on shutdown.
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		_ = d.conn.Quit()
		d.conn = nil
	}
	return nil
}

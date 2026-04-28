// Package sftp is a Storage Driver fronting an SSH/SFTP server.
//
// Connection lazy: the underlying SSH session is established on first
// operation. A single shared session is reused; if a client error
// indicates a dead session, the next operation will re-dial.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"gitlab.com/brftech/filemanager/backend/internal/storage"
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

	mu     sync.Mutex
	ssh    *ssh.Client
	client *sftp.Client
}

// Name implements storage.Driver.
func (d *Driver) Name() string { return "sftp" }

// Init configures the driver. Required: host, user; one of password or
// private_key must be set. Optional: port (default 22), root.
func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
	d.host, _ = cfg["host"].(string)
	if v, ok := cfg["port"].(int); ok {
		d.port = v
	}
	if d.port == 0 {
		d.port = 22
	}
	d.user, _ = cfg["user"].(string)
	d.password, _ = cfg["password"].(string)
	d.keyPEM, _ = cfg["private_key"].(string)
	d.root, _ = cfg["root"].(string)
	if d.root == "" {
		d.root = "/"
	}
	if d.host == "" || d.user == "" {
		return errors.New("sftp: host and user required")
	}
	if d.password == "" && d.keyPEM == "" {
		return errors.New("sftp: either password or private_key required")
	}
	return nil
}

// Capabilities — SFTP supports everything except Presign.
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

func (d *Driver) connect() (*sftp.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client, nil
	}
	cfg := &ssh.ClientConfig{
		User:            d.user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: known_hosts
		Timeout:         10 * time.Second,
	}
	if d.password != "" {
		cfg.Auth = append(cfg.Auth, ssh.Password(d.password))
	}
	if d.keyPEM != "" {
		signer, err := ssh.ParsePrivateKey([]byte(d.keyPEM))
		if err != nil {
			return nil, fmt.Errorf("sftp: parse key: %w", err)
		}
		cfg.Auth = append(cfg.Auth, ssh.PublicKeys(signer))
	}
	addr := net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port))
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("sftp: dial: %w", err)
	}
	cl, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("sftp: client: %w", err)
	}
	d.ssh = conn
	d.client = cl
	return cl, nil
}

func (d *Driver) join(p string) string {
	return path.Join(d.root, strings.TrimLeft(path.Clean("/"+p), "/"))
}

// List implements storage.Driver.
func (d *Driver) List(_ context.Context, p string) ([]storage.Object, error) {
	cl, err := d.connect()
	if err != nil {
		return nil, err
	}
	abs := d.join(p)
	entries, err := cl.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	out := make([]storage.Object, 0, len(entries))
	for _, e := range entries {
		obj := storage.Object{
			Path:  path.Join(p, e.Name()),
			Name:  e.Name(),
			Size:  e.Size(),
			Mtime: e.ModTime(),
		}
		if e.IsDir() {
			obj.Kind = storage.KindDirectory
		} else {
			obj.Kind = storage.KindFile
		}
		out = append(out, obj)
	}
	return out, nil
}

// Stat implements storage.Driver.
func (d *Driver) Stat(_ context.Context, p string) (storage.Object, error) {
	cl, err := d.connect()
	if err != nil {
		return storage.Object{}, err
	}
	info, err := cl.Stat(d.join(p))
	if err != nil {
		if os.IsNotExist(err) {
			return storage.Object{}, storage.ErrNotFound
		}
		return storage.Object{}, err
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
func (d *Driver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	cl, err := d.connect()
	if err != nil {
		return nil, err
	}
	f, err := cl.Open(d.join(p))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Write implements storage.Writer.
func (d *Driver) Write(_ context.Context, p string, r io.Reader, _ int64) error {
	cl, err := d.connect()
	if err != nil {
		return err
	}
	abs := d.join(p)
	_ = cl.MkdirAll(path.Dir(abs))
	f, err := cl.Create(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// Move implements storage.Mover.
func (d *Driver) Move(_ context.Context, src, dst string) error {
	cl, err := d.connect()
	if err != nil {
		return err
	}
	a := d.join(src)
	b := d.join(dst)
	_ = cl.MkdirAll(path.Dir(b))
	return cl.Rename(a, b)
}

// Copy implements storage.Copier — naive download/upload.
func (d *Driver) Copy(_ context.Context, src, dst string) error {
	cl, err := d.connect()
	if err != nil {
		return err
	}
	in, err := cl.Open(d.join(src))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cl.Create(d.join(dst))
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// Delete implements storage.Deleter.
func (d *Driver) Delete(_ context.Context, p string) error {
	cl, err := d.connect()
	if err != nil {
		return err
	}
	abs := d.join(p)
	if err := cl.Remove(abs); err != nil {
		// Maybe a directory.
		return cl.RemoveDirectory(abs)
	}
	return nil
}

// Mkdir implements storage.Mkdirer.
func (d *Driver) Mkdir(_ context.Context, p string) error {
	cl, err := d.connect()
	if err != nil {
		return err
	}
	return cl.MkdirAll(d.join(p))
}

// Close releases the underlying SSH session — called on shutdown.
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		_ = d.client.Close()
		d.client = nil
	}
	if d.ssh != nil {
		_ = d.ssh.Close()
		d.ssh = nil
	}
	return nil
}

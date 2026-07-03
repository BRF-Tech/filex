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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/brf-tech/filex/backend/internal/storage"
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
	d.knownHostsPath, _ = cfg["known_hosts"].(string)
	d.hostKeyPin, _ = cfg["host_key"].(string)
	d.insecureHostKey, _ = cfg["insecure_skip_host_key"].(bool)
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
	hostKeyCB, err := d.hostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("sftp: host key: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            d.user,
		HostKeyCallback: hostKeyCB,
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

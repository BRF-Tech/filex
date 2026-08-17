// Package sftpsrv serves SFTP: filex, seen by every client that already knows
// how to talk to a file server over SSH.
//
// # Why this exists
//
// The rule for this whole family of work is symmetry: filex can CONNECT to an
// SFTP server (internal/storage/drivers/sftp), so it must be connectable AS
// one. Somebody with a scanner, a backup job or a WinSCP habit should be able
// to point it at filex and have it work — with the same identity, the same
// permissions, the same trash and the same audit trail as the web app.
//
// # The fact that shapes the design
//
// `sftp.NewRequestServer` fans READ and WRITE packets out to eight concurrent
// workers that SHARE one handle, with no per-handle lock in the library.
// Multiply that by OpenSSH's own defaults (64 outstanding requests, 32 KiB
// each) and ReadAt/WriteAt are called concurrently, at unordered offsets, for
// a perfectly sequential `put`. Since OpenSSH 9.0 `scp` speaks SFTP too, so
// this is every transfer rather than an edge case. The handles in file.go are
// written for that from the first line; the failure mode of not doing so is
// "works on a 1 MB test file, corrupts a 2 GB upload".
//
// # What it does not own
//
// Identity (internal/protocolauth), post-write bookkeeping
// (internal/protocolsync), the trash (internal/trash) and the read door
// (internal/filebody) are shared with every other protocol. This package owns
// the SSH transport, the SFTP verbs, and the mapping between them.
package sftpsrv

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Config wires the server to the shared services.
type Config struct {
	// Enabled — FILEX_SFTP kill switch. OFF by default, unlike /dav and S3:
	// this one opens a TCP port of its own, and a port nobody asked for is not
	// something to switch on for them.
	Enabled bool
	// Addr is the listen address. 2022 by convention (sftpgo and
	// `rclone serve sftp` both use it; 2222 means "SSH in a container").
	Addr string
	// HostKeyDir is where the host keys live.
	//
	// ⚠ It must be a PERSISTENT directory. Regenerating host keys on a
	// container rebuild gives every user REMOTE HOST IDENTIFICATION HAS
	// CHANGED, which is indistinguishable from an attack and which most people
	// resolve by deleting their known_hosts entry — training them to ignore the
	// one warning that matters.
	HostKeyDir string
	// Banner is shown before authentication. Empty sends none.
	Banner string

	Store db.Store
	// Auth is the shared credential door. Required.
	Auth *protocolauth.Resolver
	ACL  *acl.Resolver
	// Resolver returns the live driver for a storage id.
	Resolver func(int64) (storage.Driver, error)
	// Body is the one read door — it knows whether the bytes are on the driver
	// or still in the staging area of an upload that has not landed.
	Body *filebody.Resolver
	// Quota is the per-user ceiling, checked before bytes land.
	Quota *quota.Service
	// Index and Thumbs feed the shared post-write bookkeeping.
	Index  *search.Index
	Thumbs *thumb.Pipeline
	// SpoolDir is where uploads are spooled before they are committed. Empty
	// uses the OS temp dir.
	SpoolDir string
	// MaxSpool caps one upload's spool file. 0 uses defaultMaxSpool.
	//
	// ⚠ An uncapped spool on a 40 GB object fills the disk, and the disk it
	// fills is the one the storages are on.
	MaxSpool int64
	// MultiTenant mirrors config.MultiTenant.
	MultiTenant bool
}

// Server is a running SFTP endpoint.
type Server struct {
	cfg    Config
	ssh    *ssh.ServerConfig
	syncer *protocolsync.Syncer

	ln     net.Listener
	closed chan struct{}
	wg     sync.WaitGroup

	// bans throttles repeated failures per source address.
	bans *banList
	// sessions bridges the auth callback and the connection handler — see
	// session.go for why the identity cannot travel in ssh.Permissions itself.
	sessions *sessionStore
}

const (
	// handshakeTimeout bounds the pre-auth phase.
	//
	// ⚠ Not optional: x/crypto/ssh will let a connection sit in the handshake
	// forever, so without this a handful of open sockets that never speak is a
	// denial of service that costs the other side nothing.
	handshakeTimeout = 2 * time.Minute
	// defaultMaxSpool caps a single upload spool file.
	defaultMaxSpool = 64 << 30 // 64 GiB
	// defaultAddr is the conventional SFTP-for-an-app port.
	defaultAddr = ":2022"
)

// New builds a server. It does not listen yet.
func New(cfg Config) (*Server, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Auth == nil || cfg.Store == nil || cfg.Resolver == nil || cfg.Body == nil {
		return nil, errors.New("sftpsrv: Store, Auth, Resolver and Body are required")
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.MaxSpool <= 0 {
		cfg.MaxSpool = defaultMaxSpool
	}

	s := &Server{
		cfg:      cfg,
		syncer:   protocolsync.New(cfg.Store, cfg.Index, cfg.Thumbs, writehook.OriginSFTP),
		closed:   make(chan struct{}),
		bans:     newBanList(),
		sessions: newSessionStore(),
	}
	sc, err := s.sshConfig()
	if err != nil {
		return nil, err
	}
	s.ssh = sc
	return s, nil
}

// ListenAndServe starts accepting connections and blocks until Close.
func (s *Server) ListenAndServe() error {
	if s == nil {
		return nil
	}
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("sftpsrv: listen %s: %w", s.cfg.Addr, err)
	}
	s.ln = ln
	slog.Info("sftp: listening", slog.String("addr", s.cfg.Addr))

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return nil
			default:
			}
			// A transient accept error must not end the listener: one bad
			// connection would otherwise take the service down until a restart.
			slog.Warn("sftp: accept", slog.Any("err", err))
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Close stops the listener and waits for the sessions in flight.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	s.wg.Wait()
	return nil
}

// Addr is the address actually bound, for tests and for logging.
func (s *Server) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// handleConn takes one TCP connection all the way to an SFTP session.
func (s *Server) handleConn(nc net.Conn) {
	defer nc.Close()

	remote := hostOf(nc.RemoteAddr())
	if s.bans.banned(remote) {
		// Refused before the handshake, so a password-guessing loop costs the
		// other side a TCP connection and costs us nothing.
		slog.Debug("sftp: refused a banned source", slog.String("remote", remote))
		return
	}

	_ = nc.SetDeadline(time.Now().Add(handshakeTimeout))
	sconn, chans, reqs, err := ssh.NewServerConn(nc, s.ssh)
	if err != nil {
		s.bans.fail(remote)
		slog.Debug("sftp: handshake failed", slog.String("remote", remote), slog.Any("err", err))
		return
	}
	// ⚠ Cleared, not extended. Leaving the handshake deadline on would kill a
	// legitimate transfer of anything large the moment it passed two minutes.
	_ = nc.SetDeadline(time.Time{})
	defer sconn.Close()

	s.bans.ok(remote)
	sess, ok := s.sessionFrom(sconn)
	if !ok {
		// The permissions blob is set by the auth callbacks; its absence means
		// something authenticated without going through them, which must not
		// reach a filesystem.
		slog.Error("sftp: authenticated session without a principal", slog.String("remote", remote))
		return
	}
	slog.Info("sftp: session",
		slog.String("user", sess.login),
		slog.String("remote", remote),
		slog.String("client", string(sconn.ClientVersion())))

	// ⚠ Registered so revoking the credential reaches THIS connection. An SFTP
	// session authenticates once and then serves files for hours; without this,
	// deleting the token it logged in with did exactly nothing to the session
	// that token had already opened. Closing the ssh.ServerConn is the hang-up:
	// it tears down every channel on it, so a transfer in flight stops too.
	sess.live = s.cfg.Auth.Enter(sess.principal, "sftp", remote, sess.login, func() {
		_ = sconn.Close()
	})
	defer sess.live.Leave()

	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			slog.Debug("sftp: accept channel", slog.Any("err", err))
			continue
		}
		s.wg.Add(1)
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			defer s.wg.Done()
			s.serveChannel(sess, ch, reqs)
		}(ch, chReqs)
	}
}

// hostOf strips the port so bans are per address rather than per connection.
func hostOf(addr net.Addr) string {
	h, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return h
}

// ─────────────────────────── host keys ───────────────────────────

// hostKeys loads the host keys, generating them on first run.
//
// Two algorithms: ed25519 because it is what every current client prefers, and
// RSA because a surprising amount of embedded equipment (scanners, older Java)
// still cannot do anything else — and that equipment is precisely the audience
// for an SFTP endpoint.
func (s *Server) hostKeys() ([]ssh.Signer, error) {
	dir := s.cfg.HostKeyDir
	if dir == "" {
		return nil, errors.New("sftpsrv: HostKeyDir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("sftpsrv: host key dir: %w", err)
	}

	var signers []ssh.Signer
	for _, k := range []struct {
		name string
		gen  func() ([]byte, error)
	}{
		{"ssh_host_ed25519_key", genEd25519},
		{"ssh_host_rsa_key", genRSA},
	} {
		path := filepath.Join(dir, k.name)
		pemBytes, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			pemBytes, err = k.gen()
			if err != nil {
				return nil, fmt.Errorf("sftpsrv: generate %s: %w", k.name, err)
			}
			// 0600: a host key readable by other accounts on the box is a host
			// key somebody else can impersonate the server with.
			if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
				return nil, fmt.Errorf("sftpsrv: write %s: %w", k.name, err)
			}
			slog.Info("sftp: generated a host key", slog.String("file", path))
		} else if err != nil {
			return nil, fmt.Errorf("sftpsrv: read %s: %w", k.name, err)
		}
		signer, err := ssh.ParsePrivateKey(pemBytes)
		if err != nil {
			return nil, fmt.Errorf("sftpsrv: parse %s: %w", k.name, err)
		}
		signers = append(signers, signer)
	}
	return signers, nil
}

func genEd25519() ([]byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func genRSA() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), nil
}

// ─────────────────────────── ban list ───────────────────────────

// banList is a score-based throttle in front of the handshake.
//
// It is deliberately crude: SSH on a public port attracts constant credential
// stuffing, and the cost of answering each attempt is a bcrypt comparison
// (~100 ms). Refusing a source that has just failed repeatedly keeps that cost
// off the box without ever locking out an ACCOUNT — locking accounts is how a
// stranger denies service to a real user by guessing at their name.
type banList struct {
	mu    sync.Mutex
	fails map[string]*banEntry
}

type banEntry struct {
	n     int
	until time.Time
}

const (
	banAfter = 8
	banFor   = 10 * time.Minute
)

func newBanList() *banList { return &banList{fails: map[string]*banEntry{}} }

func (b *banList) banned(host string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.fails[host]
	if e == nil {
		return false
	}
	if time.Now().After(e.until) {
		delete(b.fails, host)
		return false
	}
	return e.n >= banAfter
}

func (b *banList) fail(host string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.fails[host]
	if e == nil || time.Now().After(e.until) {
		e = &banEntry{}
		b.fails[host] = e
	}
	e.n++
	e.until = time.Now().Add(banFor)
	if len(b.fails) > 4096 {
		// A crude bound. The map is keyed by source address, so an attacker
		// with a large address pool could otherwise grow it without limit.
		for k, v := range b.fails {
			if time.Now().After(v.until) {
				delete(b.fails, k)
			}
		}
	}
}

func (b *banList) ok(host string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fails, host)
}

// ctxFor returns a context carrying the session's identity and tenant scope,
// exactly as an HTTP handler that went through the middleware chain would have.
func (s *Server) ctxFor(sess *session) context.Context {
	return sess.principal.WithContext(context.Background())
}

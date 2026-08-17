// Package nfssrv serves NFSv3: filex, mounted as a network drive by anything
// on the LAN that speaks the NAS protocol.
//
// # The identity problem, and what is done about it
//
// NFSv3 cannot authenticate a caller in any way filex can use. Real per-request
// identity means RPCSEC_GSS → Kerberos → a KDC and a machine keytab on every
// client, which nobody self-hosting a file server is going to run. AUTH_SYS,
// what every NAS actually ships, is the client asserting its own uid with
// nothing to check it against.
//
// So the binding moves: identity belongs to the EXPORT, not the request. Each
// export path carries 32 bytes of entropy (`/x/<64 hex>`), the mount handshake
// pins it to one principal, and every operation inside runs under that
// principal's scope. The uid/gid on each request is discarded rather than
// trusted, and the permission bits going out are synthesised from the ACL —
// the same rule the SFTP and FTPS surfaces follow.
//
// ⚠⚠ NFSv3 is unencrypted and unauthenticated on the wire. Anyone who can read
// the traffic, or who already knows the path, can mount it. That is why this
// listener is OFF by default, why exports carry an optional CIDR allow-list,
// and why `filex mount` (FUSE over HTTPS) — not this — is the answer for
// anything off-LAN. NFS is the NAS-on-the-LAN protocol; pretending otherwise
// would be the dishonest part.
//
// # What is borrowed and what is written here
//
// `willscott/go-nfs` (Apache-2.0, the same library `rclone serve nfs` uses)
// carries XDR, portmap and the RPC loop. What this package writes is the
// adapter onto filex's storage, the identity model above, and the gates.
package nfssrv

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	nfs "github.com/willscott/go-nfs"

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
	// Enabled — FILEX_NFS kill switch. OFF by default, and more emphatically
	// than the others: NFSv3 has no transport security at all, so switching it
	// on is a decision about the network it sits on.
	Enabled bool
	// Addr is the listen address. 2049 is the registered NFS port and what
	// clients assume; a different one needs `-o port=` on every mount.
	Addr string

	Store    db.Store
	Auth     *protocolauth.Resolver
	ACL      *acl.Resolver
	Resolver func(int64) (storage.Driver, error)
	Body     *filebody.Resolver
	Quota    *quota.Service
	Index    *search.Index
	Thumbs   *thumb.Pipeline
	// SpoolDir is where in-flight writes are spooled.
	SpoolDir string
	// MaxSpool caps one file's spool. 0 uses the package default.
	MaxSpool    int64
	MultiTenant bool
}

// Server is a running NFSv3 endpoint.
type Server struct {
	cfg    Config
	syncer *protocolsync.Syncer
	// started is the mtime reported for anything filex synthesises rather than
	// stores — the virtual root, today. It must not move: see fs.Stat.
	started time.Time

	ln     net.Listener
	closed chan struct{}
	wg     sync.WaitGroup
}

const (
	defaultAddr     = ":2049"
	defaultMaxSpool = 16 << 30
)

// New builds a server. It does not listen yet.
func New(cfg Config) (*Server, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Auth == nil || cfg.Store == nil || cfg.Resolver == nil || cfg.Body == nil {
		return nil, errors.New("nfssrv: Store, Auth, Resolver and Body are required")
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.MaxSpool <= 0 {
		cfg.MaxSpool = defaultMaxSpool
	}
	return &Server{
		cfg:     cfg,
		syncer:  protocolsync.New(cfg.Store, cfg.Index, cfg.Thumbs, writehook.OriginNFS),
		started: time.Now(),
		closed:  make(chan struct{}),
	}, nil
}

// ListenAndServe blocks until Close.
func (s *Server) ListenAndServe() error {
	if s == nil {
		return nil
	}
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("nfssrv: listen %s: %w", s.cfg.Addr, err)
	}
	s.ln = ln
	slog.Info("nfs: listening",
		slog.String("addr", ln.Addr().String()),
		slog.String("note", "NFSv3 is unencrypted; keep this on a LAN or a VPN"))

	handler := newHandler(s)
	if err := nfs.Serve(ln, handler); err != nil {
		select {
		case <-s.closed:
			return nil
		default:
		}
		return err
	}
	return nil
}

// Close stops the listener.
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

// Addr is the address actually bound.
func (s *Server) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

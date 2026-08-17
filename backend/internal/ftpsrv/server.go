// Package ftpsrv serves FTPS: filex, reached by the equipment that only ever
// learned one protocol.
//
// # Why, when SFTP already exists
//
// The plan that scoped this work said "defer FTPS, revisit when a named
// counterparty cannot speak SFTP". The symmetry rule overrode that: filex has
// an FTP storage driver, so it can CONNECT to an FTP server — and a product
// that can connect to something it refuses to be is asymmetric by design. The
// audience is also real and cannot be argued with: scan-to-FTP multifunction
// printers, EDI counterparties, and lab equipment whose firmware was finished
// a decade ago.
//
// # The one non-negotiable
//
// ⚠⚠ TLS IS MANDATORY. Plain FTP sends the password in the clear and then
// sends the file in the clear after it. filex is a file server people keep
// their own things in; offering a protocol that publishes their credentials to
// anything on the path would be a defect no configuration flag makes
// acceptable. This server speaks FTPS (explicit TLS, `AUTH TLS`) and refuses
// everything else — a client that will not negotiate TLS is refused before it
// can send a password, not after.
//
// # The part that fails as a hang rather than an error
//
// Passive mode is where FTP deployments break, and they break silently: the
// server replies with an address the client cannot reach and the client waits
// until it gives up. Neither side logs anything useful. So the passive port
// range and the advertised address are explicit configuration, and both are
// stated in the connection guide rather than left to be discovered.
package ftpsrv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"

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
	// Enabled — FILEX_FTPS kill switch. OFF by default: it opens a control
	// port AND a range of data ports, which is not something to do for
	// somebody who did not ask.
	Enabled bool
	// Addr is the control channel's listen address (2121 by convention for an
	// application FTP server; 21 needs root on Linux).
	Addr string
	// PublicHost is the address passive-mode replies advertise.
	//
	// ⚠⚠ This is THE setting FTP deployments get wrong, and it fails as a hang
	// rather than an error: the server answers PASV with an address the client
	// cannot route to, and the client waits until it times out. Behind NAT or
	// in Docker it must be the address the CLIENT would use, which is not the
	// address the process sees. Empty means "answer with the control
	// connection's local address", which is right for a direct connection and
	// wrong behind NAT.
	PublicHost string
	// PassivePortMin/Max bound the data ports. They must be published in the
	// firewall and, in Docker, mapped one-for-one — a range that is open on the
	// server and closed at the edge is the same hang as a wrong PublicHost.
	PassivePortMin int
	PassivePortMax int
	// CertFile/KeyFile are the TLS material. Empty generates a self-signed pair
	// into CertDir — usable, and honestly labelled as such in the guide, since
	// most FTPS clients will not verify a certificate they were not given.
	CertFile string
	KeyFile  string
	CertDir  string
	// Banner is the greeting. Some equipment logs it, nothing parses it.
	Banner string
	// IdleTimeout in seconds. FTP holds a control connection open between
	// commands, so an unbounded one is a socket leak with a login attached.
	IdleTimeout int

	Store    db.Store
	Auth     *protocolauth.Resolver
	ACL      *acl.Resolver
	Resolver func(int64) (storage.Driver, error)
	Body     *filebody.Resolver
	Quota    *quota.Service
	Index    *search.Index
	Thumbs   *thumb.Pipeline
	// SpoolDir is where uploads are spooled before they are committed.
	SpoolDir string
	// MaxSpool caps one upload. 0 uses defaultMaxSpool.
	MaxSpool    int64
	MultiTenant bool
}

// Server is a running FTPS endpoint.
type Server struct {
	cfg    Config
	ftp    *ftpserver.FtpServer
	syncer *protocolsync.Syncer
	tls    *tls.Config

	mu      sync.Mutex
	started bool
}

const (
	defaultAddr     = ":2121"
	defaultMaxSpool = 64 << 30
	// defaultIdle is generous: an FTP client legitimately sits idle between a
	// listing and the transfer a human then asks for.
	defaultIdle = 900
	// The conventional passive range for an application FTP server.
	defaultPasvMin = 30000
	defaultPasvMax = 30100
)

// New builds a server. It does not listen yet.
func New(cfg Config) (*Server, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Auth == nil || cfg.Store == nil || cfg.Resolver == nil || cfg.Body == nil {
		return nil, errors.New("ftpsrv: Store, Auth, Resolver and Body are required")
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.MaxSpool <= 0 {
		cfg.MaxSpool = defaultMaxSpool
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdle
	}
	if cfg.PassivePortMin <= 0 || cfg.PassivePortMax <= 0 || cfg.PassivePortMax < cfg.PassivePortMin {
		cfg.PassivePortMin, cfg.PassivePortMax = defaultPasvMin, defaultPasvMax
	}

	s := &Server{
		cfg:    cfg,
		syncer: protocolsync.New(cfg.Store, cfg.Index, cfg.Thumbs, writehook.OriginFTP),
	}
	tlsCfg, err := s.tlsConfig()
	if err != nil {
		return nil, err
	}
	s.tls = tlsCfg
	s.ftp = ftpserver.NewFtpServer(&driver{srv: s})
	return s, nil
}

// ListenAndServe blocks until Close.
func (s *Server) ListenAndServe() error {
	if s == nil {
		return nil
	}
	if err := s.ftp.Listen(); err != nil {
		return fmt.Errorf("ftpsrv: listen %s: %w", s.cfg.Addr, err)
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	slog.Info("ftps: listening",
		slog.String("addr", s.Addr()),
		slog.String("passive", fmt.Sprintf("%d-%d", s.cfg.PassivePortMin, s.cfg.PassivePortMax)),
		slog.String("public_host", s.cfg.PublicHost))
	return s.ftp.Serve()
}

// Close stops the listener.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return nil
	}
	return s.ftp.Stop()
}

// Addr is the address actually bound.
func (s *Server) Addr() string {
	if s == nil || s.ftp == nil {
		return ""
	}
	return s.ftp.Addr()
}

// ─────────────────────────── TLS ───────────────────────────

// tlsConfig loads the certificate, generating a self-signed one when none was
// supplied.
//
// ⚠ A generated certificate is not a substitute for a real one, and the guide
// says so: it encrypts the channel but proves nothing about who is on the other
// end. It exists because the alternative — refusing to start without a
// certificate — would push people towards plain FTP, and an unverified
// encrypted channel is strictly better than a plaintext one. Most FTPS clients
// do not verify by default anyway, which is a fact about the ecosystem rather
// than an endorsement.
func (s *Server) tlsConfig() (*tls.Config, error) {
	if s.cfg.CertFile != "" && s.cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("ftpsrv: load certificate: %w", err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
	}

	dir := s.cfg.CertDir
	if dir == "" {
		return nil, errors.New("ftpsrv: CertDir is required when no certificate is configured")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ftpsrv: cert dir: %w", err)
	}
	certPath := filepath.Join(dir, "ftps.crt")
	keyPath := filepath.Join(dir, "ftps.key")

	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		if err := generateSelfSigned(certPath, keyPath, s.cfg.PublicHost); err != nil {
			return nil, err
		}
		slog.Warn("ftps: generated a self-signed certificate",
			slog.String("file", certPath),
			slog.String("note", "it encrypts the channel but proves nothing about the server; supply a real certificate for anything that matters"))
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("ftpsrv: load generated certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

func generateSelfSigned(certPath, keyPath, host string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"filex"}, CommonName: hostOrDefault(host)},
		NotBefore:    time.Now().Add(-time.Hour),
		// Ten years: a certificate nobody is watching that expires in a year is
		// an outage nobody is expecting.
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{hostOrDefault(host)},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
}

func hostOrDefault(host string) string {
	if host == "" {
		return "localhost"
	}
	return host
}

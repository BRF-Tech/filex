package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
)

// ── Tenants' addresses: own domains and who certifies them ──────────────────
//
// docs/TENANT-ADMIN.md. A tenant's own domain is proven by a CNAME to its
// platform subdomain and checked again every few hours (tenantdomain); the
// certificates of every tenant address are the reverse proxy's
// (FILEX_TLS_MODE=proxy, the default: filex answers its /api/tls/* questions)
// or filex's own (FILEX_TLS_MODE=acme: serveOwnTLS below). A tenant's own
// certificate is served first either way.

// domainCheckEvery is how often every own domain is looked at again.
const domainCheckEvery = 6 * time.Hour

// platformHosts are the platform's own addresses: FILEX_PUBLIC_URL's host.
func platformHosts(cfg config.Config) []string {
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Hostname() != "" {
		return []string{strings.ToLower(u.Hostname())}
	}
	return nil
}

// newDomainService builds the own-domain service. A domain that stops (or
// starts again) routing is told to every enabled administrator of its tenant,
// in their bell, when notifications are on.
func newDomainService(cfg config.Config, store db.Store, n notify.Service) *tenantdomain.Service {
	svc := &tenantdomain.Service{Store: store, PlatformHosts: platformHosts(cfg)}
	if n == nil {
		return svc
	}
	svc.Notify = func(ctx context.Context, p *model.Provider, d *model.ProviderDomain, status string) {
		users, err := store.ListUsersByProvider(ctx, p.ID)
		if err != nil {
			slog.Warn("tenant domain: could not tell the tenant's administrators", slog.String("domain", d.Domain), slog.Any("err", err))
			return
		}
		for _, u := range users {
			if u.Role != model.RoleAdmin || !u.Enabled {
				continue
			}
			_, _ = n.Send(context.WithoutCancel(ctx), notify.TenantDomainChanged(u.ID, d, status == model.DomainSuspended))
		}
	}
	return svc
}

// servesPlatform reports whether a name is one the platform serves at all:
// its own address, or a tenant's (tenantdomain.Serves).
func (s *Server) servesPlatform(ctx context.Context, name string) bool {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	for _, h := range platformHosts(s.cfg) {
		if name == h {
			return true
		}
	}
	return tenantdomain.Serves(ctx, s.store, name)
}

// acmeManager is the certificate manager of filex's own TLS: the host policy
// is /api/tls/ask's question, the directory FILEX_TLS_ACME_DIRECTORY (Let's
// Encrypt when empty), and the client's transport logs what the authority
// says when it cannot validate an address (acmeProblems: autocert itself
// reports only "no viable challenge type found") and gives a finalize answer
// without a Location header its order's address (acmeOrderLocation: the
// client would poll the empty address and never fetch the certificate).
//
// status (nil: none) is told the authority's reasons as they arrive, for the
// screen (tenantdomain.ACMEStatus).
func (s *Server) acmeManager(cacheDir string, status *tenantdomain.ACMEStatus) *autocert.Manager {
	dir := strings.TrimSpace(s.cfg.TLS.ACMEDirectory)
	if dir == "" {
		dir = autocert.DefaultACMEDirectory
	}
	var onProblem func(domain, reason string)
	if status != nil {
		onProblem = status.Problem
	}
	return &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cacheDir),
		Email:  s.cfg.TLS.ACMEEmail,
		HostPolicy: func(ctx context.Context, host string) error {
			// The HTTP-01 handler asks with the request's Host header, which
			// carries the port when the authority dials another one than 80
			// (Pebble: :5002); a TLS handshake asks with the bare SNI name.
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if s.servesPlatform(ctx, host) {
				return nil
			}
			return fmt.Errorf("acme: %s is %w", host, errNotServed)
		},
		Client: &acme.Client{
			DirectoryURL: dir,
			HTTPClient:   &http.Client{Transport: newACMEProblems(newACMEOrderLocation(http.DefaultTransport), onProblem)},
		},
	}
}

// errNotServed is the host policy's refusal: a name the platform does not
// serve gets no certificate, and is nothing the screen reports on.
var errNotServed = errors.New("not an address this platform serves")

// certIssuer hands filex's own TLS a certificate (autocert.Manager).
type certIssuer interface {
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// ownCertificate answers a handshake on filex's own TLS: the tenant's own
// certificate first, else the ACME one. It records what ACME did for the
// address (status: obtained until when, or not obtained and why) - never
// for the authority's own TLS-ALPN-01 challenge handshake, which is answered
// with a challenge certificate, nor for a name the platform does not serve.
func ownCertificate(own func(ctx context.Context, name string) *tls.Certificate, issuer certIssuer, status *tenantdomain.ACMEStatus) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if c := own(hello.Context(), hello.ServerName); c != nil {
			return c, nil
		}
		c, err := issuer.GetCertificate(hello)
		if hello.ServerName == "" || slices.Contains(hello.SupportedProtos, acme.ALPNProto) {
			return c, err
		}
		switch {
		case err == nil && c != nil:
			if leaf := leafOf(c); leaf != nil {
				status.Obtained(hello.ServerName, leaf.NotAfter)
			}
		case err != nil && !errors.Is(err, errNotServed):
			status.Failed(hello.ServerName, err)
		}
		return c, err
	}
}

func leafOf(c *tls.Certificate) *x509.Certificate {
	if c.Leaf != nil {
		return c.Leaf
	}
	if len(c.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return nil
	}
	return leaf
}

// serveOwnTLS is filex terminating TLS itself (FILEX_TLS_MODE=acme): the same
// router on FILEX_TLS_LISTEN (default :443), certificates issued by an ACME
// authority for every address the platform serves and nothing else (the host
// policy is /api/tls/ask's question), a tenant's own certificate first, and a
// plain listener (FILEX_TLS_HTTP_LISTEN, default :80) for the HTTP-01
// challenge that sends everything else to HTTPS.
func (s *Server) serveOwnTLS(ctx context.Context) {
	cacheDir := filepath.Join(s.cfg.DataDir, "acme")
	var status *tenantdomain.ACMEStatus
	if s.domains != nil {
		status = s.domains.ACME
	}
	m := s.acmeManager(cacheDir, status)
	own := &tenantdomain.Certs{Store: s.store, Box: s.box}
	tlsCfg := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
		GetCertificate: ownCertificate(own.For, m, status),
	}
	listen := s.cfg.TLS.Listen
	if listen == "" {
		listen = ":443"
	}
	srv := &http.Server{Addr: listen, Handler: s.srv.Handler, TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second}
	plain := s.cfg.TLS.HTTPListen
	if plain == "" {
		plain = ":80"
	}
	var redirect *http.Server
	if plain != "off" {
		redirect = &http.Server{Addr: plain, Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
		go serveListener(ctx, "acme-http", redirect.ListenAndServe)
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		if redirect != nil {
			_ = redirect.Shutdown(sctx)
		}
	}()
	slog.Info("tls: filex issues its own certificates (FILEX_TLS_MODE=acme)",
		slog.String("listen", listen), slog.String("challenge_listen", plain), slog.String("cache", cacheDir))
	go serveListener(ctx, "https", func() error { return srv.ListenAndServeTLS("", "") })
}

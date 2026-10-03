package tenantdomain

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/secretbox"
)

// ── A tenant's own certificate (bring your own) ─────────────────────────────
//
// The owner's decision (2026-10-01): besides the installation's way of issuing
// certificates (the proxy in front, or filex's own ACME), a tenant may bring
// its own certificate for its domain: the chain and the key, in PEM. The key
// is stored sealed (FILEX_SECRET_KEY) and leaves filex only to the trusted
// proxy that serves it (/api/tls/certificate) or into filex's own TLS server.

// Why a brought certificate is refused.
var (
	ErrCertInvalid   = errors.New("certificate_invalid")
	ErrCertMismatch  = errors.New("certificate_key_mismatch")
	ErrCertWrongName = errors.New("certificate_wrong_name")
	ErrCertExpired   = errors.New("certificate_expired")
	// ErrCertNeedsKey: no FILEX_SECRET_KEY to seal the private key with; a
	// key is never stored in the clear.
	ErrCertNeedsKey = errors.New("certificate_needs_secret_key")
)

// CheckCertificate reads a brought certificate: the key must match the
// certificate, the certificate must name the domain, and it must not have
// expired. It answers the certificate's expiry.
func CheckCertificate(certPEM, keyPEM, domain string, now time.Time) (time.Time, error) {
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		if strings.Contains(err.Error(), "does not match") {
			return time.Time{}, ErrCertMismatch
		}
		return time.Time{}, ErrCertInvalid
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return time.Time{}, ErrCertInvalid
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return time.Time{}, ErrCertWrongName
	}
	if !now.Before(leaf.NotAfter) {
		return time.Time{}, ErrCertExpired
	}
	return leaf.NotAfter.UTC(), nil
}

// SetCertificate stores a brought certificate on a domain's row, the key
// sealed. Empty certPEM removes it (the installation's way issues one again).
func SetCertificate(ctx context.Context, store db.Store, box *secretbox.Box, d *model.ProviderDomain, certPEM, keyPEM string, now time.Time) error {
	if strings.TrimSpace(certPEM) == "" {
		return store.SetProviderDomainCert(ctx, d.ID, "", "", nil)
	}
	if box == nil || !box.Enabled() {
		return ErrCertNeedsKey
	}
	notAfter, err := CheckCertificate(certPEM, keyPEM, d.Domain, now)
	if err != nil {
		return err
	}
	sealed, err := box.Seal(keyPEM)
	if err != nil {
		return err
	}
	return store.SetProviderDomainCert(ctx, d.ID, certPEM, sealed, &notAfter)
}

// OwnCertificate is the certificate a tenant brought for a name it serves (an
// ACTIVE own domain of an enabled tenant), as PEM (chain, then key) and as a
// tls.Certificate; ok false when there is none.
func OwnCertificate(ctx context.Context, store db.Store, box *secretbox.Box, serverName string) (pemBundle []byte, cert *tls.Certificate, ok bool) {
	name, err := Normalize(serverName)
	if err != nil {
		return nil, nil, false
	}
	d, err := store.GetProviderDomain(ctx, name)
	if err != nil || d == nil || d.Status != model.DomainActive || !d.HasOwnCertificate() || box == nil {
		return nil, nil, false
	}
	if p, err := store.GetProvider(ctx, d.ProviderID); err != nil || p == nil || !p.Enabled {
		return nil, nil, false
	}
	key, err := box.Open(d.TLSKeySealed)
	if err != nil {
		return nil, nil, false
	}
	pair, err := tls.X509KeyPair([]byte(d.TLSCertPEM), []byte(key))
	if err != nil {
		return nil, nil, false
	}
	bundle := strings.TrimRight(d.TLSCertPEM, "\n") + "\n" + strings.TrimRight(key, "\n") + "\n"
	return []byte(bundle), &pair, true
}

// Serves reports whether a name is one the platform serves for a tenant and
// may be certified (the /api/tls/ask question and filex's own ACME host
// policy): an enabled tenant's `host`, its platform subdomain, or an ACTIVE
// own domain. The platform's own addresses are the caller's to add.
func Serves(ctx context.Context, store db.Store, serverName string) bool {
	name, err := Normalize(serverName)
	if err != nil {
		return false
	}
	p, err := store.GetProviderByHost(ctx, name)
	return err == nil && p != nil
}

// Certs serves tenants' own certificates to a TLS handshake (filex's own TLS
// server, FILEX_TLS_MODE=acme): OwnCertificate, remembered for a few minutes
// so a handshake does not read the database and open a sealed key each time.
type Certs struct {
	Store db.Store
	Box   *secretbox.Box
	// TTL is how long an answer (a certificate, or "none") is kept; 0 = 5m.
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]certEntry
}

type certEntry struct {
	cert    *tls.Certificate
	expires time.Time
}

// For answers the tenant's own certificate for a server name, or nil.
func (c *Certs) For(ctx context.Context, serverName string) *tls.Certificate {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(serverName), "."))
	if name == "" {
		return nil
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.cache[name]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.cert
	}
	c.mu.Unlock()
	_, cert, ok := OwnCertificate(ctx, c.Store, c.Box, name)
	if !ok {
		cert = nil
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]certEntry{}
	}
	c.cache[name] = certEntry{cert: cert, expires: now.Add(ttl)}
	c.mu.Unlock()
	return cert
}

// Forget drops what is remembered for a name (a certificate changed).
func (c *Certs) Forget(serverName string) {
	c.mu.Lock()
	delete(c.cache, strings.ToLower(strings.TrimSpace(serverName)))
	c.mu.Unlock()
}

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"

	"github.com/brf-tech/filex/backend/internal/tenantdomain"
)

type stubIssuer struct {
	cert *tls.Certificate
	err  error
}

func (s stubIssuer) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return s.cert, s.err
}

func issuedFor(t *testing.T, name string, notAfter time.Time) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func noOwn(context.Context, string) *tls.Certificate { return nil }

// TestOwnCertificate_RecordsWhatACMEDid: the screen's three states come from
// the handshakes filex's own TLS answers - an issued certificate is
// "obtained until", a failure is "not obtained" with the authority's reason,
// and neither the authority's TLS-ALPN-01 handshake nor a name the platform
// does not serve changes anything.
func TestOwnCertificate_RecordsWhatACMEDid(t *testing.T) {
	status := &tenantdomain.ACMEStatus{}
	notAfter := time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second).UTC()

	// The authority's validation handshake: a challenge certificate, not
	// the address's certificate.
	alpn := &tls.ClientHelloInfo{ServerName: "files.acme.example", SupportedProtos: []string{acme.ALPNProto}}
	_, err := ownCertificate(noOwn, stubIssuer{cert: issuedFor(t, "files.acme.example", time.Now().Add(time.Hour))}, status)(alpn)
	require.NoError(t, err)
	assert.Equal(t, tenantdomain.ACMENone, status.For("files.acme.example").State)

	hello := &tls.ClientHelloInfo{ServerName: "files.acme.example", SupportedProtos: []string{"h2", "http/1.1"}}
	c, err := ownCertificate(noOwn, stubIssuer{cert: issuedFor(t, "files.acme.example", notAfter)}, status)(hello)
	require.NoError(t, err)
	require.NotNil(t, c)
	r := status.For("files.acme.example")
	require.Equal(t, tenantdomain.ACMEObtained, r.State)
	assert.Equal(t, notAfter, *r.NotAfter)

	// Not obtained: the authority's reason (told by the ACME transport) is
	// what the screen shows, not autocert's last error.
	status.Problem("beta.tenants.files.example", "DNS problem: NXDOMAIN looking up A for beta.tenants.files.example")
	beta := &tls.ClientHelloInfo{ServerName: "beta.tenants.files.example"}
	_, err = ownCertificate(noOwn, stubIssuer{err: errors.New("acme/autocert: unable to satisfy ...: no viable challenge type found")}, status)(beta)
	require.Error(t, err)
	r = status.For("beta.tenants.files.example")
	require.Equal(t, tenantdomain.ACMEFailed, r.State)
	assert.Contains(t, r.Reason, "NXDOMAIN")

	// A stranger's name: refused by the host policy, nothing recorded.
	stranger := &tls.ClientHelloInfo{ServerName: "stranger.example"}
	_, err = ownCertificate(noOwn, stubIssuer{err: fmt.Errorf("acme: stranger.example is %w", errNotServed)}, status)(stranger)
	require.Error(t, err)
	assert.Equal(t, tenantdomain.ACMENone, status.For("stranger.example").State)
}

// TestOwnCertificate_TenantsOwnFirst: a tenant's own certificate is served
// before ACME is asked, and is not ACME's to report.
func TestOwnCertificate_TenantsOwnFirst(t *testing.T) {
	status := &tenantdomain.ACMEStatus{}
	mine := issuedFor(t, "files.acme.example", time.Now().Add(24*time.Hour))
	own := func(context.Context, string) *tls.Certificate { return mine }
	c, err := ownCertificate(own, stubIssuer{err: errors.New("must not be asked")}, status)(&tls.ClientHelloInfo{ServerName: "files.acme.example"})
	require.NoError(t, err)
	assert.Same(t, mine, c)
	assert.Equal(t, tenantdomain.ACMENone, status.For("files.acme.example").State)
}

package tenantdomain_test

// A tenant's own certificate (bring your own): the key matches, the name is
// the domain's, it has not expired; the key is stored sealed, never in the
// clear; and it is served only for an ACTIVE domain of an enabled tenant.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
)

func selfSigned(t *testing.T, name string, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	kder, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
}

func TestCheckCertificate(t *testing.T) {
	now := time.Now()
	cert, key := selfSigned(t, "files.acme.example", now.Add(90*24*time.Hour))
	notAfter, err := tenantdomain.CheckCertificate(cert, key, "files.acme.example", now)
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(90*24*time.Hour), notAfter, time.Second)

	_, err = tenantdomain.CheckCertificate(cert, key, "other.acme.example", now)
	assert.ErrorIs(t, err, tenantdomain.ErrCertWrongName)
	_, otherKey := selfSigned(t, "files.acme.example", now.Add(time.Hour))
	_, err = tenantdomain.CheckCertificate(cert, otherKey, "files.acme.example", now)
	assert.ErrorIs(t, err, tenantdomain.ErrCertMismatch)
	_, err = tenantdomain.CheckCertificate("not a pem", key, "files.acme.example", now)
	assert.ErrorIs(t, err, tenantdomain.ErrCertInvalid)
	_, err = tenantdomain.CheckCertificate(cert, key, "files.acme.example", now.Add(91*24*time.Hour))
	assert.ErrorIs(t, err, tenantdomain.ErrCertExpired)
}

func TestOwnCertificate_SealedAndServedForAnActiveDomainOnly(t *testing.T) {
	store, svc, dns, acme, _, _ := setup(t)
	ctx := context.Background()
	d, err := svc.Add(ctx, acme, "files.acme.example", nil)
	require.NoError(t, err)
	cert, key := selfSigned(t, "files.acme.example", time.Now().Add(30*24*time.Hour))

	err = tenantdomain.SetCertificate(ctx, store, nil, d, cert, key, time.Now())
	assert.ErrorIs(t, err, tenantdomain.ErrCertNeedsKey, "a key is never stored in the clear")

	box, err := secretbox.New("tenant-domain-test-key")
	require.NoError(t, err)
	require.NoError(t, tenantdomain.SetCertificate(ctx, store, box, d, cert, key, time.Now()))
	row, err := store.GetProviderDomainByID(ctx, d.ID)
	require.NoError(t, err)
	assert.True(t, secretbox.IsSealed(row.TLSKeySealed), "the key is sealed at rest")
	assert.NotContains(t, row.TLSKeySealed, "PRIVATE KEY")
	require.NotNil(t, row.TLSNotAfter)

	// Pending: not served.
	_, _, ok := tenantdomain.OwnCertificate(ctx, store, box, "files.acme.example")
	assert.False(t, ok)

	dns.set("files.acme.example", "acme.tenants.files.example")
	_, _, err = svc.Check(ctx, row)
	require.NoError(t, err)
	bundle, c, ok := tenantdomain.OwnCertificate(ctx, store, box, "FILES.acme.example")
	require.True(t, ok)
	require.NotNil(t, c)
	assert.True(t, strings.HasPrefix(string(bundle), "-----BEGIN CERTIFICATE-----"))
	assert.Contains(t, string(bundle), "PRIVATE KEY", "the proxy gets the chain then the key")

	certs := &tenantdomain.Certs{Store: store, Box: box}
	assert.NotNil(t, certs.For(ctx, "files.acme.example"))
	assert.Nil(t, certs.For(ctx, "nobody.example"))

	// Taken away: the installation's way issues one again.
	require.NoError(t, tenantdomain.SetCertificate(ctx, store, box, row, "", "", time.Now()))
	_, _, ok = tenantdomain.OwnCertificate(ctx, store, box, "files.acme.example")
	assert.False(t, ok)
}

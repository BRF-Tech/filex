package wasmplugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// makeCA builds a certificate authority the way a real PKI would hand one
// over: a certificate and its key, in PEM. `rsaKey` picks the key type,
// because an operator's own authority is usually RSA while ours is ECDSA.
func makeCA(t *testing.T, cn string, rsaKey bool, life time.Duration) (certPEM, keyPEM []byte, cert *x509.Certificate) {
	t.Helper()
	var (
		pub  any
		priv any
	)
	if rsaKey {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		pub, priv = &k.PublicKey, k
	} else {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		pub, priv = &k.PublicKey, k
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"Acme"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(life),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	require.NoError(t, err)
	cert, err = x509.ParseCertificate(der)
	require.NoError(t, err)
	pk, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}),
		cert
}

// An operator brings their own authority: from then on signatures are issued
// under it, the authority filex generated is retired rather than deleted,
// and everything it ever signed still verifies.
func TestSigningCA_ImportKeepsEveryAuthority(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	ctx := context.Background()
	h.writeFile(t, "docs/nda.txt", "I agree")

	// A first signature, under the authority filex makes for itself.
	job, err := h.runJob(t, p, "sign", []string{"docs/nda.txt"}, "en")
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	var first struct{ Cert, Chain, CA, Sig string }
	require.NoError(t, json.Unmarshal([]byte(job.Message), &first))
	generated := parseCert(t, first.CA)
	assert.Contains(t, generated.Subject.CommonName, "filex signing CA")

	// The operator's own authority takes over. RSA on purpose: most
	// in-house authorities are, and the leaves we issue stay ECDSA.
	certPEM, keyPEM, mine := makeCA(t, "Acme Corporate CA", true, 5*365*24*time.Hour)
	row, err := h.reg.ImportCA(ctx, 0, certPEM, keyPEM)
	require.NoError(t, err)
	assert.Equal(t, "Acme Corporate CA", row.Subject)

	// A second signature is issued under it, and the chain checks out.
	job, err = h.runJob(t, p, "sign", []string{"docs/nda.txt"}, "en")
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	var second struct{ Cert, Chain, CA, Sig string }
	require.NoError(t, json.Unmarshal([]byte(job.Message), &second))
	leaf := parseCert(t, second.Cert)
	assert.Equal(t, "Acme Corporate CA", parseCert(t, second.CA).Subject.CommonName)
	require.NoError(t, leaf.CheckSignatureFrom(mine), "the leaf is signed by the imported authority")
	_, isECDSA := leaf.PublicKey.(*ecdsa.PublicKey)
	assert.True(t, isECDSA, "leaves stay ECDSA P-256 whatever the authority is")

	// Nothing was deleted: both authorities are listed, the imported one is
	// live, and the root bundle carries both — so the FIRST signature, made
	// under the retired authority, still has a root to verify against.
	list, err := h.reg.CAs(ctx, 0)
	require.NoError(t, err)
	require.Len(t, list, 2)
	live, retired := 0, 0
	for _, ca := range list {
		if ca.Live {
			live++
			assert.Equal(t, "Acme Corporate CA", parseName(ca.Subject))
			assert.True(t, ca.Imported)
		} else {
			retired++
			assert.NotNil(t, ca.RetiredAt)
		}
		assert.Len(t, ca.Fingerprint, 64, "a sha-256 fingerprint, for reading out loud")
	}
	assert.Equal(t, 1, live)
	assert.Equal(t, 1, retired)

	bundle, err := h.reg.CACertsPEM(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(bundle, "BEGIN CERTIFICATE"))
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(bundle)))
	oldLeaf := parseCert(t, first.Cert)
	_, err = oldLeaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	require.NoError(t, err, "a signature made under the retired authority still verifies")
}

// An authority filex cannot sign with is refused at the door, with a reason
// the person reading it can act on.
func TestSigningCA_ImportRefusesWhatCannotSign(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	good, goodKey, _ := makeCA(t, "Acme CA", false, time.Hour*24)

	t.Run("not an authority", func(t *testing.T) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "plain leaf"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true}
		der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
		require.NoError(t, err)
		pk, _ := x509.MarshalPKCS8PrivateKey(k)
		_, err = h.reg.ImportCA(ctx, 0,
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a certificate authority")
	})

	t.Run("expired", func(t *testing.T) {
		certPEM, keyPEM, _ := makeCA(t, "Old CA", false, -time.Hour)
		_, err := h.reg.ImportCA(ctx, 0, certPEM, keyPEM)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("key belongs to another certificate", func(t *testing.T) {
		_, otherKey, _ := makeCA(t, "Other CA", false, time.Hour*24)
		_, err := h.reg.ImportCA(ctx, 0, good, otherKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not belong")
	})

	t.Run("encrypted key says what to do about it", func(t *testing.T) {
		enc := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("nope")})
		_, err := h.reg.ImportCA(ctx, 0, good, enc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "openssl pkcs12")
	})

	t.Run("a good one is accepted", func(t *testing.T) {
		_, err := h.reg.ImportCA(ctx, 0, good, goodKey)
		require.NoError(t, err)
	})
}

// parseName pulls the common name out of an RFC 2253 subject string.
func parseName(subject string) string {
	for _, part := range strings.Split(subject, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "CN=") {
			return strings.TrimPrefix(part, "CN=")
		}
	}
	return subject
}

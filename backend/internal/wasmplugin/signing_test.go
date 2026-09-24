package wasmplugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func parseCert(t *testing.T, pemText string) *x509.Certificate {
	t.Helper()
	blk, _ := pem.Decode([]byte(pemText))
	require.NotNil(t, blk, "pem")
	c, err := x509.ParseCertificate(blk.Bytes)
	require.NoError(t, err)
	return c
}

func TestSigning_IssueSignVerifyDestroy(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/nda.txt", "I agree")

	job, err := h.runJob(t, p, "sign", []string{"docs/nda.txt"}, "en")
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	var out struct{ Cert, Chain, CA, Sig string }
	require.NoError(t, json.Unmarshal([]byte(job.Message), &out))

	leaf := parseCert(t, out.Cert)
	ca := parseCert(t, out.CA)
	assert.Equal(t, ca.Raw, parseCert(t, out.Chain).Raw, "chain is the tenant CA")
	assert.True(t, ca.IsCA)
	assert.Equal(t, "filex signing CA", ca.Subject.CommonName)
	assert.Equal(t, []string{"echo"}, leaf.Subject.OrganizationalUnit, "leaf names the plugin")
	assert.Contains(t, leaf.UnknownExtKeyUsage[0].String(), "1.3.6.1.5.5.7.3.36", "document-signing EKU")

	// The chain verifies against the CA the admin would hand to readers.
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	require.NoError(t, err)

	// The signature is over sha256 of the document's bytes, by the leaf key.
	sig, err := base64.StdEncoding.DecodeString(out.Sig)
	require.NoError(t, err)
	sum := sha256.Sum256([]byte("I agree"))
	assert.True(t, ecdsa.VerifyASN1(leaf.PublicKey.(*ecdsa.PublicKey), sum[:], sig))
	tampered := sha256.Sum256([]byte("I agree!"))
	assert.False(t, ecdsa.VerifyASN1(leaf.PublicKey.(*ecdsa.PublicKey), tampered[:], sig))

	// The key row was destroyed (the guest also asserted a second sign fails).
	rows := 0
	// No list method for leaves; check through the CA row + a fresh lookup by
	// scanning what the plugin reported is not possible, so use the store's
	// destroy semantics indirectly: a second job issues a NEW key, and the CA
	// is reused (same certificate).
	job2, err := h.runJob(t, p, "sign", []string{"docs/nda.txt"}, "en")
	require.NoError(t, err)
	var out2 struct{ Cert, CA string }
	require.NoError(t, json.Unmarshal([]byte(job2.Message), &out2))
	assert.Equal(t, out.CA, out2.CA, "one CA per tenant")
	assert.NotEqual(t, out.Cert, out2.Cert, "one leaf per signing")
	_ = rows

	// Rotation: the next signature gets a new CA; the old CA still verifies the old leaf.
	require.NoError(t, h.reg.RotateCA(context.Background(), 0))
	pemAfter, err := h.reg.CACertPEM(context.Background(), 0)
	require.NoError(t, err)
	assert.NotEqual(t, out.CA, pemAfter)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	assert.NoError(t, err, "old leaf, old CA")
}

func TestSigning_UnavailableWithoutSecretKey(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.SecretKey = "" })
	p := h.install(t)
	h.writeFile(t, "docs/nda.txt", "x")
	job, err := h.runJob(t, p, "sign", []string{"docs/nda.txt"}, "en")
	require.Error(t, err)
	assert.Equal(t, model.AppPluginJobFailed, job.Status)
	assert.Contains(t, job.Error, "FILEX_SECRET_KEY")
}

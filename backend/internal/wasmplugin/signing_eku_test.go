package wasmplugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// The two extended key usages a signing certificate carries: RFC 9336's
// id-kp-documentSigning, and Adobe's Authentic Documents Trust, which is the
// one of Acrobat's accepted signing usages that says "documents" and nothing
// else.
var (
	oidDocumentSigning = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 36}
	oidAdobeDocuments  = asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 5}
)

// assertDocumentsOnly is what every certificate the host issues for signing
// must be: usable to sign a document, and for nothing else. emailProtection
// in particular made an app's signer certificate - in any name and any email
// address the app asked for - an S/MIME certificate under the tenant's
// authority, which is an organisation-wide one once an operator imports their
// own.
func assertDocumentsOnly(t *testing.T, cert *x509.Certificate, roots *x509.CertPool) {
	t.Helper()
	assert.Empty(t, cert.ExtKeyUsage, "no general-purpose usage (emailProtection, clientAuth, any)")
	assert.ElementsMatch(t, []string{oidDocumentSigning.String(), oidAdobeDocuments.String()}, oidStrings(cert.UnknownExtKeyUsage))
	_, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}})
	assert.Error(t, err, "the certificate must not verify for e-mail protection")
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	assert.Error(t, err, "nor for TLS client authentication")
}

func oidStrings(in []asn1.ObjectIdentifier) []string {
	out := make([]string, 0, len(in))
	for _, o := range in {
		out = append(out, o.String())
	}
	return out
}

func TestSigning_TheLeafIsForDocumentsOnly(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/nda.txt", "I agree")

	job, err := h.runJob(t, p, "sign", []string{"docs/nda.txt"}, "en")
	require.NoError(t, err)
	require.Equal(t, model.AppPluginJobOK, job.Status, job.Error)
	var out struct{ Cert, CA string }
	require.NoError(t, json.Unmarshal([]byte(job.Message), &out))
	roots := x509.NewCertPool()
	roots.AddCert(parseCert(t, out.CA))
	assertDocumentsOnly(t, parseCert(t, out.Cert), roots)
}

func TestSeal_IsForDocumentsOnlyAndAnOlderOneIsRenewed(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/x.txt", "x")
	s := h.jobScope(t, p, nil, "docs/x.txt")
	ctx := context.Background()

	// A seal issued before this release: the same subject, from the same
	// authority, still valid - but with emailProtection. It is replaced at
	// its next use rather than kept for ten years.
	_, caKey, caCert, err := h.reg.caFor(ctx, 0)
	require.NoError(t, err)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(424242),
		Subject:      pkix.Name{CommonName: sealCN, Organization: []string{"filex"}, OrganizationalUnit: []string{p.Row.Name}},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{oidDocumentSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &priv.PublicKey, caKey)
	require.NoError(t, err)
	old, err := h.reg.storeKey(ctx, 0, p.Row.ID, sealPurpose, sealCN, der, priv, tpl.NotAfter)
	require.NoError(t, err)

	got, err := sealCall(t, s, hfCertIssue, map[string]any{"purpose": "platform"})
	require.NoError(t, err)
	assert.NotEqual(t, old.ID, got["key_ref"], "the seal with the old usages is replaced")
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	assertDocumentsOnly(t, parseCert(t, got["cert_pem"].(string)), roots)

	again, err := sealCall(t, s, hfCertIssue, map[string]any{"purpose": "platform"})
	require.NoError(t, err)
	assert.Equal(t, got["key_ref"], again["key_ref"], "and the new one is kept")
}

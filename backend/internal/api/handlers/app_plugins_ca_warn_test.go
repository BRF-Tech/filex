package handlers_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An operator who imports their organisation's general-purpose authority is
// told, at import, what that means: filex now holds a key that everything
// trusting the authority for e-mail, websites or code trusts, and it issues
// certificates in any name an app with `sign` asks for. An authority made for
// document signing alone is imported without a word.
func TestAppPlugins_SigningCA_ImportWarnsAboutAGeneralPurposeAuthority(t *testing.T) {
	f := newAppFixture(t, nil)
	url := f.srv.URL + "/api/admin/app-plugins/signing/ca/import"

	type answer struct {
		Imported bool `json:"imported"`
		Warnings []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"warnings"`
	}

	certPEM, keyPEM := ownCA(t, "Acme Corporate CA")
	status, raw := doReq(t, f.admin, http.MethodPost, url, map[string]any{"cert_pem": string(certPEM), "key_pem": string(keyPEM)})
	require.Equal(t, http.StatusOK, status, string(raw))
	var general answer
	require.NoError(t, json.Unmarshal(raw, &general))
	assert.True(t, general.Imported, "a warning does not refuse the import")
	require.Len(t, general.Warnings, 1, string(raw))
	assert.Equal(t, "ca_not_limited_to_document_signing", general.Warnings[0].Code)
	assert.Contains(t, general.Warnings[0].Message, "document signing")

	certPEM, keyPEM = documentSigningCA(t, "Acme Document Signing CA")
	status, raw = doReq(t, f.admin, http.MethodPost, url, map[string]any{"cert_pem": string(certPEM), "key_pem": string(keyPEM)})
	require.Equal(t, http.StatusOK, status, string(raw))
	var dedicated answer
	require.NoError(t, json.Unmarshal(raw, &dedicated))
	assert.True(t, dedicated.Imported)
	assert.Empty(t, dedicated.Warnings, string(raw))
}

// documentSigningCA is an intermediate-shaped authority restricted to
// document signing: id-kp-documentSigning and Adobe's Authentic Documents
// Trust in its extendedKeyUsage.
func documentSigningCA(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(3, 0, 0),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:     true, BasicConstraintsValid: true,
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{{1, 3, 6, 1, 5, 5, 7, 3, 36}, {1, 2, 840, 113583, 1, 1, 5}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	require.NoError(t, err)
	pk, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
}

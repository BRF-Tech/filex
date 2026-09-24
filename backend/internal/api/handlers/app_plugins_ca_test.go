package handlers_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An operator imports the authority they already have, and everything the
// old one signed keeps verifying because nothing is deleted.
func TestAppPlugins_SigningCA_ImportRetiresTheOldOneAndKeepsIt(t *testing.T) {
	f := newAppFixture(t, nil)

	// Asking for the certificate creates the authority filex makes itself.
	status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/signing/ca.pem", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "BEGIN CERTIFICATE")

	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/signing/cas", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var before struct {
		Authorities []struct {
			Subject     string `json:"subject"`
			Fingerprint string `json:"fingerprint"`
			Live        bool   `json:"live"`
			Imported    bool   `json:"imported"`
		} `json:"authorities"`
	}
	require.NoError(t, json.Unmarshal(raw, &before))
	require.Len(t, before.Authorities, 1)
	assert.True(t, before.Authorities[0].Live)
	assert.Len(t, before.Authorities[0].Fingerprint, 64)

	certPEM, keyPEM := ownCA(t, "Acme Corporate CA")
	body, ct := caUpload(t, certPEM, keyPEM)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins/signing/ca/import", body)
	req.Header.Set("Content-Type", ct)
	resp, err := f.admin.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/signing/cas", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var after struct {
		Authorities []struct {
			Subject  string `json:"subject"`
			Live     bool   `json:"live"`
			Imported bool   `json:"imported"`
		} `json:"authorities"`
	}
	require.NoError(t, json.Unmarshal(raw, &after))
	require.Len(t, after.Authorities, 2, "the old authority is retired, not deleted")
	live := 0
	for _, a := range after.Authorities {
		if a.Live {
			live++
			assert.Contains(t, a.Subject, "Acme Corporate CA")
			assert.True(t, a.Imported)
		}
	}
	assert.Equal(t, 1, live)

	// The certificate endpoint now answers with the imported authority.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/signing/ca.pem", nil)
	require.Equal(t, http.StatusOK, status)
	blk, _ := pem.Decode(raw)
	require.NotNil(t, blk)
	cert, err := x509.ParseCertificate(blk.Bytes)
	require.NoError(t, err)
	assert.Equal(t, "Acme Corporate CA", cert.Subject.CommonName)
}

// What cannot sign is refused, and the refusal says what to do about it.
func TestAppPlugins_SigningCA_ImportRefusalsExplainThemselves(t *testing.T) {
	f := newAppFixture(t, nil)
	certPEM, keyPEM := ownCA(t, "Acme CA")

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins/signing/ca/import",
		map[string]any{"cert_pem": string(certPEM)})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "private key")

	enc := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("locked")})
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins/signing/ca/import",
		map[string]any{"cert_pem": string(certPEM), "key_pem": string(enc)})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "openssl pkcs12", "the refusal names the command that fixes it")

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins/signing/ca/import",
		map[string]any{"cert_pem": string(certPEM), "key_pem": string(keyPEM)})
	assert.Equal(t, http.StatusOK, status, string(raw))
}

func ownCA(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(3, 0, 0),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:     true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	require.NoError(t, err)
	pk, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
}

func caUpload(t *testing.T, certPEM, keyPEM []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	cf, err := w.CreateFormFile("cert", "ca.crt")
	require.NoError(t, err)
	_, _ = cf.Write(certPEM)
	kf, err := w.CreateFormFile("key", "ca.key")
	require.NoError(t, err)
	_, _ = kf.Write(keyPEM)
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

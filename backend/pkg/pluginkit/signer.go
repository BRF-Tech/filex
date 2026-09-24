package pluginkit

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
)

// ── Signing (permission `sign`) ────────────────────────────────────────
//
// The private key never enters the sandbox. The host issues a certificate
// from its per-tenant CA, signs sha256 digests with the matching key, and
// destroys the key when told. HostSigner wraps that as a crypto.Signer so a
// PDF library that takes one (pdfsign, pkcs7) works unchanged.

// SignInfo says whether signing is available and hands out the CA cert.
type SignInfo struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	CACertPEM string `json:"ca_cert_pem,omitempty"`
	// CACertsPEM is every authority this tenant has signed with, retired
	// ones included, as one PEM bundle. Build a verifier's root pool from
	// THIS, not from CACertPEM: trusting only the live authority calls
	// every signature made before the last rotation untrusted.
	CACertsPEM string `json:"ca_certs_pem,omitempty"`
	Algorithm  string `json:"algorithm,omitempty"`
}

// HostSignInfo asks the host whether it can sign for this call's tenant.
func HostSignInfo() (*SignInfo, error) {
	var out SignInfo
	if err := callJSON(hostSignInfo, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IssuedCert is a leaf certificate the host minted for one signer.
type IssuedCert struct {
	KeyRef   string `json:"key_ref"`
	CertPEM  string `json:"cert_pem"`
	ChainPEM string `json:"chain_pem"`
	NotAfter string `json:"not_after"`
}

// CertIssue mints a certificate for commonName (the signer's name), with an
// optional e-mail SAN, valid for days (host-clamped). Action jobs only.
func CertIssue(commonName, email string, days int) (*IssuedCert, error) {
	var out IssuedCert
	if err := callJSON(hostCertIssue, map[string]any{"common_name": commonName, "email": email, "days": days}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PlatformSeal hands out the installation's SEAL for this app: a
// certificate "filex document seal" (OU = the app's name) from the tenant's
// authority, with a key the host keeps. Unlike CertIssue it is the same key
// on every call — until the authority is rotated — and KeyDestroy refuses
// it. Sign with it through NewHostSigner, exactly like a signer's
// certificate. Action jobs only; permission `sign`.
//
// It exists for one thing: an app that finishes a document on the
// installation's behalf (the signing app seals every completed request, so
// the bytes every party is sent a hash of were closed by filex itself).
func PlatformSeal() (*IssuedCert, error) {
	var out IssuedCert
	if err := callJSON(hostCertIssue, map[string]any{"purpose": "platform"}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HostSign signs a sha256 digest with the key behind keyRef; the answer is
// a DER-encoded ECDSA signature (ecdsa-p256-sha256).
func HostSign(keyRef string, digest []byte) ([]byte, error) {
	var out struct {
		SignatureB64 string `json:"signature_b64"`
	}
	if err := callJSON(hostSign, map[string]any{"key_ref": keyRef, "hash": "sha256", "digest_b64": base64.StdEncoding.EncodeToString(digest)}, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.SignatureB64)
}

// KeyDestroy discards the private key behind keyRef; the certificate stays.
func KeyDestroy(keyRef string) error {
	return callJSON(hostKeyDestroy, map[string]string{"key_ref": keyRef}, nil)
}

// HostSigner is a crypto.Signer over a host-held key.
type HostSigner struct {
	KeyRef string
	Cert   *x509.Certificate
	pub    crypto.PublicKey
}

// NewHostSigner parses the issued certificate and binds the key ref.
func NewHostSigner(issued *IssuedCert) (*HostSigner, error) {
	blk, _ := pem.Decode([]byte(issued.CertPEM))
	if blk == nil {
		return nil, errors.New("pluginkit: certificate unreadable")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, err
	}
	if _, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok {
		return nil, errors.New("pluginkit: unexpected key type")
	}
	return &HostSigner{KeyRef: issued.KeyRef, Cert: cert, pub: cert.PublicKey}, nil
}

// Public returns the certificate's public key.
func (h *HostSigner) Public() crypto.PublicKey { return h.pub }

// Sign asks the host to sign digest (must be a sha256 digest).
func (h *HostSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts != nil && opts.HashFunc() != crypto.SHA256 {
		return nil, errors.New("pluginkit: host signs sha256 digests only")
	}
	return HostSign(h.KeyRef, digest)
}

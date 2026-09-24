package wasmplugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"time"
)

// ── The platform's seal ────────────────────────────────────────────────
//
// ⚠⚠ The owner, 2026-09-22: when the last signature of a request lands,
// "filex adds one more signature over the whole document with the instance
// key — so the final bytes are sealed by filex itself", and every party is
// sent the SHA-256 of exactly those bytes. A per-signer leaf (cert_issue)
// cannot be that: it names a person, and its key is destroyed the moment the
// person's signature is written. The seal names the installation, and its
// key stays with the host.
//
// One seal key per (tenant, app), made on first use and kept sealed with
// FILEX_SECRET_KEY like every other key here. The certificate is issued by
// the tenant's LIVE authority, carries the document-signing EKU like a
// signer's, and says whose seal it is: CN "filex document seal", O "filex",
// OU the app's name — an app with `sign` can seal only as ITSELF, never as
// another app. When the authority is rotated, the next seal request issues a
// new seal certificate from the new authority and the old key is destroyed
// (its certificate stays, inside every document it sealed).
//
// Why not the CA key itself: a certificate authority's key signs
// certificates. Signing documents with it would make every document a
// possible forged certificate and every leaf a possible forged document.

const (
	sealPurpose = "platform"
	sealCN      = "filex document seal"
	sealYears   = 10
)

// sealFor returns the tenant's seal key for plugin p, issuing it when there
// is none (or when the one there was issued by a retired authority).
func (r *Registry) sealFor(ctx context.Context, tenantID int64, p *Installed) (*signingKey, *x509.Certificate, error) {
	_, caKey, caCert, err := r.caFor(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	r.signMu.Lock()
	defer r.signMu.Unlock()
	row, err := r.opts.Store.GetAppPluginSealKey(ctx, tenantID, p.Row.ID)
	if err != nil {
		return nil, nil, err
	}
	if row != nil {
		cert, cerr := firstCertOf(row.CertPEM)
		if cerr == nil && cert.CheckSignatureFrom(caCert) == nil && time.Now().Before(cert.NotAfter) {
			return row, caCert, nil
		}
		// Issued by an authority that has since been retired, or run out:
		// the key goes, the certificate stays in the documents it sealed.
		if derr := r.opts.Store.DestroyAppPluginSigningKey(ctx, row.ID); derr != nil {
			return nil, nil, derr
		}
		p.log("info", "the platform seal was renewed (the signing authority changed or the seal expired)")
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	now := time.Now()
	notAfter := now.AddDate(sealYears, 0, 0)
	if caCert.NotAfter.Before(notAfter) {
		notAfter = caCert.NotAfter
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{CommonName: sealCN, Organization: []string{"filex"},
			OrganizationalUnit: []string{p.Row.Name}, SerialNumber: "tenant-" + itoa(tenantID)},
		NotBefore:          now.Add(-5 * time.Minute),
		NotAfter:           notAfter,
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{{1, 3, 6, 1, 5, 5, 7, 3, 36}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	row, err = r.storeKey(ctx, tenantID, p.Row.ID, sealPurpose, sealCN, der, priv, notAfter)
	if err != nil {
		return nil, nil, err
	}
	p.log("info", "platform seal issued ("+row.ID[:8]+"…)")
	return row, caCert, nil
}

// errSealKept is key_destroy's answer for the seal: it is the host's.
var errSealKept = errors.New("the platform seal key is kept by the host; only a signer's key is destroyed")

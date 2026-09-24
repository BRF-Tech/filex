package wasmplugin

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Signing: the host holds the keys, the plugin holds the document ────
//
// A signing plugin (filex-sign) computes the PDF byte ranges and the digest
// itself; the private key never enters the wasm sandbox. The host runs one
// CA per tenant (lazily, ECDSA P-256, sealed with FILEX_SECRET_KEY), issues
// short-lived leaf certificates to the plugin on request (cert_issue), signs
// digests with them (host_sign — a raw signature the plugin wraps into CMS
// with its own library) and destroys the key when told (key_destroy). What a
// reader needs to trust every signature made on this instance is the CA
// certificate, which the admin can download.

const (
	signCAYears     = 20
	signLeafMaxDays = 3650
	signLeafDefault = 3650
	signPerMinute   = 20
)

type signingKey = model.AppPluginSigningKey

// caFor returns the tenant's live CA, creating it on first use.
func (r *Registry) caFor(ctx context.Context, tenantID int64) (*signingKey, crypto.Signer, *x509.Certificate, error) {
	if !r.box.Enabled() {
		return nil, nil, nil, errors.New("signing needs FILEX_SECRET_KEY")
	}
	r.signMu.Lock()
	defer r.signMu.Unlock()
	row, err := r.opts.Store.GetAppPluginSigningCA(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, err
	}
	if row == nil {
		row, err = r.newCA(ctx, tenantID)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	key, cert, err := r.openCAKey(row)
	if err != nil {
		return nil, nil, nil, err
	}
	return row, key, cert, nil
}

func (r *Registry) newCA(ctx context.Context, tenantID int64) (*signingKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "filex signing CA", Organization: []string{"filex"}, SerialNumber: "tenant-" + itoa(tenantID)},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(signCAYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	row, err := r.storeKey(ctx, tenantID, 0, "ca", tpl.Subject.CommonName, der, priv, tpl.NotAfter)
	if err != nil {
		return nil, err
	}
	r.log.Info("app-plugins: signing CA created", "tenant", tenantID, "id", row.ID)
	return row, nil
}

func (r *Registry) storeKey(ctx context.Context, tenantID, pluginID int64, purpose, subject string, certDER []byte, priv *ecdsa.PrivateKey, expires time.Time) (*signingKey, error) {
	pk, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	sealed, err := r.box.Seal(base64.StdEncoding.EncodeToString(pk))
	if err != nil {
		return nil, err
	}
	idb := make([]byte, 16)
	_, _ = rand.Read(idb)
	row := &signingKey{
		ID: hex.EncodeToString(idb), TenantID: tenantID, PluginID: pluginID, Purpose: purpose, Subject: subject,
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})), KeySealed: sealed, ExpiresAt: &expires,
	}
	if err := r.opts.Store.CreateAppPluginSigningKey(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

// openCAKey opens a CA row. Unlike openKey it accepts any key type the CA
// may have been imported with (RSA is what most in-house PKIs hand out); the
// leaves it signs are still ours, ECDSA P-256, because host_sign makes those.
//
// CertPEM may hold a chain — the signing certificate first, its issuers
// after it — so an intermediate CA imported from a real PKI verifies up to
// its own root.
func (r *Registry) openCAKey(row *signingKey) (crypto.Signer, *x509.Certificate, error) {
	k, cert, err := r.unsealKey(row)
	if err != nil {
		return nil, nil, err
	}
	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("this key cannot sign")
	}
	return signer, cert, nil
}

// unsealKey opens the sealed private key of a row and reads its leading
// certificate. What the key must BE is the caller's question: a CA may be
// any signer, a leaf is always ECDSA P-256.
func (r *Registry) unsealKey(row *signingKey) (any, *x509.Certificate, error) {
	if row.DestroyedAt != nil || row.KeySealed == "" {
		return nil, nil, errors.New("key was destroyed")
	}
	plain, err := r.box.Open(row.KeySealed)
	if err != nil {
		return nil, nil, errors.New("key cannot be opened with this instance's secret key")
	}
	der, err := base64.StdEncoding.DecodeString(plain)
	if err != nil {
		return nil, nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, nil, err
	}
	cert, err := firstCertOf(row.CertPEM)
	if err != nil {
		return nil, nil, err
	}
	return k, cert, nil
}

// firstCertOf reads the leading certificate of a PEM bundle.
func firstCertOf(pemText string) (*x509.Certificate, error) {
	blk, _ := pem.Decode([]byte(pemText))
	if blk == nil {
		return nil, errors.New("certificate unreadable")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func (r *Registry) openKey(row *signingKey) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	k, cert, err := r.unsealKey(row)
	if err != nil {
		return nil, nil, err
	}
	// Leaves are always ours and always ECDSA P-256 — host_sign signs with
	// that and nothing else. A CA key of another type goes through
	// openCAKey instead.
	priv, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("unexpected key type")
	}
	return priv, cert, nil
}

func tenantOf(u *model.User) int64 {
	if u != nil && u.ProviderID != nil {
		return *u.ProviderID
	}
	return 0
}

// CACertPEM returns the tenant CA certificate (creating the CA on first use).
func (r *Registry) CACertPEM(ctx context.Context, tenantID int64) (string, error) {
	row, _, _, err := r.caFor(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return row.CertPEM, nil
}

// CACertsPEM returns EVERY certificate this tenant has ever signed with —
// the live CA first, then the retired ones, newest first — as one PEM
// bundle. This is the root set a verifier needs: a signature made two CAs
// ago must still check out, so nothing here is ever deleted.
func (r *Registry) CACertsPEM(ctx context.Context, tenantID int64) (string, error) {
	if _, _, _, err := r.caFor(ctx, tenantID); err != nil {
		return "", err
	}
	rows, err := r.opts.Store.ListAppPluginSigningCAs(ctx, tenantID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	live := ""
	for _, row := range rows {
		if row.RetiredAt == nil && live == "" {
			live = row.CertPEM
			continue
		}
	}
	if live != "" {
		b.WriteString(strings.TrimRight(live, "\n"))
		b.WriteString("\n")
	}
	for _, row := range rows {
		if row.RetiredAt == nil && row.CertPEM == live {
			continue
		}
		b.WriteString(strings.TrimRight(row.CertPEM, "\n"))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// CAList describes one signing authority for the admin surface.
type CAList struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	Issuer      string     `json:"issuer"`
	Fingerprint string     `json:"fingerprint"`
	Imported    bool       `json:"imported"`
	NotBefore   time.Time  `json:"not_before"`
	NotAfter    time.Time  `json:"not_after"`
	CreatedAt   time.Time  `json:"created_at"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
	Live        bool       `json:"live"`
}

// CAs lists the tenant's signing authorities, live and retired, for the
// admin panel and the verify screen: who signed, with what, until when.
func (r *Registry) CAs(ctx context.Context, tenantID int64) ([]CAList, error) {
	rows, err := r.opts.Store.ListAppPluginSigningCAs(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]CAList, 0, len(rows))
	for _, row := range rows {
		item := CAList{ID: row.ID, Subject: row.Subject, CreatedAt: row.CreatedAt, RetiredAt: row.RetiredAt, Live: row.RetiredAt == nil}
		if cert, err := firstCertOf(row.CertPEM); err == nil {
			sum := sha256.Sum256(cert.Raw)
			item.Subject = cert.Subject.String()
			item.Issuer = cert.Issuer.String()
			item.Fingerprint = hex.EncodeToString(sum[:])
			item.NotBefore, item.NotAfter = cert.NotBefore, cert.NotAfter
			item.Imported = cert.Subject.String() != cert.Issuer.String() || !strings.Contains(cert.Subject.CommonName, "filex signing CA")
		}
		out = append(out, item)
	}
	return out, nil
}

// ImportCA takes over signing with an authority the operator already has —
// their own PKI, or a certificate they trust everywhere else. The current CA
// is retired, not deleted, so everything it signed keeps verifying.
//
// certPEM may be a bundle: the signing certificate first, its issuers after
// it. keyPEM is that certificate's private key, in PKCS#8, PKCS#1 or SEC1
// form, NOT encrypted — a .p12/.pfx is converted first
// (`openssl pkcs12 -in ca.p12 -nodes -out ca.pem`), because the encrypted
// forms in the wild are more varied than any one library reads.
func (r *Registry) ImportCA(ctx context.Context, tenantID int64, certPEM, keyPEM []byte) (*signingKey, error) {
	if !r.box.Enabled() {
		return nil, errors.New("signing needs FILEX_SECRET_KEY")
	}
	cert, err := firstCertOf(string(certPEM))
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("this certificate is not a certificate authority (basicConstraints CA:TRUE is missing)")
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("this certificate may not sign other certificates (keyUsage has no keyCertSign)")
	}
	now := time.Now()
	if now.After(cert.NotAfter) {
		return nil, fmt.Errorf("this certificate expired on %s", cert.NotAfter.Format("2006-01-02"))
	}
	if now.Before(cert.NotBefore) {
		return nil, fmt.Errorf("this certificate is not valid until %s", cert.NotBefore.Format("2006-01-02"))
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("private key: this key cannot sign")
	}
	if !samePublicKey(signer.Public(), cert.PublicKey) {
		return nil, errors.New("the private key does not belong to this certificate")
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	sealed, err := r.box.Seal(base64.StdEncoding.EncodeToString(pk))
	if err != nil {
		return nil, err
	}

	r.signMu.Lock()
	defer r.signMu.Unlock()
	if err := r.opts.Store.RetireAppPluginSigningCA(ctx, tenantID); err != nil {
		return nil, err
	}
	idb := make([]byte, 16)
	_, _ = rand.Read(idb)
	notAfter := cert.NotAfter
	row := &signingKey{
		ID: hex.EncodeToString(idb), TenantID: tenantID, Purpose: "ca",
		Subject: cert.Subject.CommonName, CertPEM: normalisePEM(certPEM), KeySealed: sealed, ExpiresAt: &notAfter,
	}
	if err := r.opts.Store.CreateAppPluginSigningKey(ctx, row); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(cert.Raw)
	r.log.Info("app-plugins: signing CA imported", "tenant", tenantID, "id", row.ID,
		"subject", cert.Subject.String(), "fingerprint", hex.EncodeToString(sum[:]))
	return row, nil
}

// parsePrivateKey reads the three shapes a PEM private key comes in.
func parsePrivateKey(pemText []byte) (any, error) {
	rest := pemText
	for {
		blk, remainder := pem.Decode(rest)
		if blk == nil {
			return nil, errors.New("no private key found (is it PEM, and not encrypted?)")
		}
		rest = remainder
		if strings.Contains(blk.Type, "ENCRYPTED") || len(blk.Headers) > 0 && blk.Headers["DEK-Info"] != "" {
			return nil, errors.New("this key is encrypted; decrypt it first (openssl pkcs12 -in ca.p12 -nodes -out ca.pem)")
		}
		switch blk.Type {
		case "PRIVATE KEY":
			return x509.ParsePKCS8PrivateKey(blk.Bytes)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(blk.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(blk.Bytes)
		}
	}
}

// samePublicKey answers whether a key belongs to a certificate.
func samePublicKey(a, b any) bool {
	type equaler interface{ Equal(x crypto.PublicKey) bool }
	if e, ok := a.(equaler); ok {
		return e.Equal(b)
	}
	return false
}

// normalisePEM keeps only the certificate blocks, in order, so a bundle
// pasted with comments or a stray key still stores cleanly.
func normalisePEM(in []byte) string {
	var b strings.Builder
	rest := in
	for {
		blk, remainder := pem.Decode(rest)
		if blk == nil {
			break
		}
		rest = remainder
		if blk.Type == "CERTIFICATE" {
			b.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: blk.Bytes}))
		}
	}
	return b.String()
}

// RotateCA retires the tenant CA; the next signature creates a fresh one.
// Leaves issued by the old CA stay verifiable through its certificate,
// which is why retiring never deletes.
func (r *Registry) RotateCA(ctx context.Context, tenantID int64) error {
	r.signMu.Lock()
	defer r.signMu.Unlock()
	return r.opts.Store.RetireAppPluginSigningCA(ctx, tenantID)
}

// ── host functions (permission `sign`) ─────────────────────────────────

func hfSignInfo(ctx context.Context, s *Scope, _ json.RawMessage) (any, error) {
	if !s.reg.box.Enabled() {
		return map[string]any{"available": false, "reason": "FILEX_SECRET_KEY is not set"}, nil
	}
	row, _, _, err := s.reg.caFor(ctx, tenantOf(s.actor))
	if err != nil {
		return map[string]any{"available": false, "reason": err.Error()}, nil
	}
	// ca_certs_pem is every authority this tenant has signed with, retired
	// ones included: a verify screen that only trusts the live CA calls
	// last year's signatures untrusted.
	all, err := s.reg.CACertsPEM(ctx, tenantOf(s.actor))
	if err != nil {
		all = row.CertPEM
	}
	return map[string]any{"available": true, "ca_cert_pem": row.CertPEM, "ca_certs_pem": all, "algorithm": "ecdsa-p256-sha256"}, nil
}

func hfCertIssue(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		CommonName string `json:"common_name"`
		Email      string `json:"email"`
		Days       int    `json:"days"`
		// Purpose "platform" asks for the installation's seal instead of a
		// signer's certificate (seal.go): the same key every time, kept by
		// the host, never destroyed by the app.
		Purpose string `json:"purpose"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.writable {
		return nil, hostErr(wire.ErrPermissionDenied, "certificates are issued from action jobs only")
	}
	switch req.Purpose {
	case "", "signer":
	case sealPurpose:
		if !s.plugin.signRate.take(signPerMinute) {
			return nil, hostErr(wire.ErrBusy, "signing rate limit")
		}
		row, caCert, err := s.reg.sealFor(ctx, tenantOf(s.actor), s.plugin)
		if err != nil {
			return nil, hostErr(wire.ErrUnavailable, err.Error())
		}
		notAfter := time.Time{}
		if row.ExpiresAt != nil {
			notAfter = *row.ExpiresAt
		}
		return map[string]any{"key_ref": row.ID, "cert_pem": row.CertPEM, "chain_pem": caCert2PEM(caCert), "not_after": notAfter}, nil
	default:
		return nil, hostErr(wire.ErrInvalid, "purpose must be \"signer\" or \"platform\"")
	}
	cn := clip(strings.TrimSpace(req.CommonName), 120)
	if cn == "" {
		return nil, hostErr(wire.ErrInvalid, "common_name is required")
	}
	if !s.plugin.signRate.take(signPerMinute) {
		return nil, hostErr(wire.ErrBusy, "signing rate limit")
	}
	tenant := tenantOf(s.actor)
	_, caKey, caCert, err := s.reg.caFor(ctx, tenant)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, err.Error())
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "keygen")
	}
	days := req.Days
	if days <= 0 {
		days = signLeafDefault
	}
	if days > signLeafMaxDays {
		days = signLeafMaxDays
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"filex"}, OrganizationalUnit: []string{s.plugin.Row.Name}},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.AddDate(0, 0, days),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		// 1.3.6.1.5.5.7.3.36 (id-kp-documentSigning) is what PDF readers look for.
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{{1, 3, 6, 1, 5, 5, 7, 3, 36}},
	}
	if e := strings.TrimSpace(req.Email); e != "" && len(e) < 200 {
		tpl.EmailAddresses = []string{e}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "issue: "+err.Error())
	}
	row, err := s.reg.storeKey(ctx, tenant, s.plugin.Row.ID, "leaf", cn, der, priv, tpl.NotAfter)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "store: "+err.Error())
	}
	s.plugin.log("info", "certificate issued for "+cn+" ("+row.ID[:8]+"…)")
	return map[string]any{"key_ref": row.ID, "cert_pem": row.CertPEM, "chain_pem": caCert2PEM(caCert), "not_after": tpl.NotAfter}, nil
}

func caCert2PEM(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

func hfHostSign(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		KeyRef    string `json:"key_ref"`
		Hash      string `json:"hash"`
		DigestB64 string `json:"digest_b64"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	if !s.plugin.signRate.take(signPerMinute) {
		return nil, hostErr(wire.ErrBusy, "signing rate limit")
	}
	row, err := s.reg.opts.Store.GetAppPluginSigningKey(ctx, req.KeyRef)
	if err != nil || row == nil || row.PluginID != s.plugin.Row.ID || (row.Purpose != "leaf" && row.Purpose != sealPurpose) {
		return nil, hostErr(wire.ErrNotFound, "no such key")
	}
	if row.TenantID != tenantOf(s.actor) {
		return nil, hostErr(wire.ErrPermissionDenied, "key belongs to another tenant")
	}
	// ⚠ The seal signs from JOBS only, like cert_issue: a screen answering a
	// visitor must never be able to put the installation's name under
	// anything.
	if row.Purpose == sealPurpose && !s.writable {
		return nil, hostErr(wire.ErrPermissionDenied, "the platform seal signs from action jobs only")
	}
	digest, err := base64.StdEncoding.DecodeString(req.DigestB64)
	if err != nil {
		return nil, hostErr(wire.ErrInvalid, "digest_b64 is not base64")
	}
	if req.Hash != "" && req.Hash != "sha256" {
		return nil, hostErr(wire.ErrInvalid, "only sha256 digests are signed")
	}
	if len(digest) != sha256.Size {
		return nil, hostErr(wire.ErrInvalid, "digest must be 32 bytes (sha256)")
	}
	priv, _, err := s.reg.openKey(row)
	if err != nil {
		return nil, hostErr(wire.ErrUnavailable, err.Error())
	}
	sig, err := priv.Sign(rand.Reader, digest, crypto.SHA256)
	if err != nil {
		return nil, hostErr(wire.ErrInternal, "sign")
	}
	return map[string]any{"signature_b64": base64.StdEncoding.EncodeToString(sig), "algorithm": "ecdsa-p256-sha256"}, nil
}

func hfKeyDestroy(ctx context.Context, s *Scope, in json.RawMessage) (any, error) {
	var req struct {
		KeyRef string `json:"key_ref"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, hostErr(wire.ErrInvalid, "bad json")
	}
	row, err := s.reg.opts.Store.GetAppPluginSigningKey(ctx, req.KeyRef)
	if err == nil && row != nil && row.PluginID == s.plugin.Row.ID && row.Purpose == sealPurpose {
		return nil, hostErr(wire.ErrInvalid, errSealKept.Error())
	}
	if err != nil || row == nil || row.PluginID != s.plugin.Row.ID || row.Purpose != "leaf" {
		return nil, hostErr(wire.ErrNotFound, "no such key")
	}
	if err := s.reg.opts.Store.DestroyAppPluginSigningKey(ctx, row.ID); err != nil {
		return nil, hostErr(wire.ErrInternal, err.Error())
	}
	return map[string]any{"ok": true}, nil
}

func itoa(n int64) string { return big.NewInt(n).String() }

package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ── E2E key escrow: the server's half ────────────────────────────────
//
// Escrow gives the operator a second way into an encrypted folder, and it
// is deliberately lopsided: the server holds only the PUBLIC key, which is
// enough to wrap a new folder's master key to the escrow identity and
// useless for opening anything. The private half was handed to the admin
// once at install and is pasted back into the browser when it is used.
//
// So a stolen filex database — or a stolen host, or a subpoenaed backup —
// decrypts nothing. The operator who kept their private key can open any
// folder created while escrow was on, and nothing else.
//
// The browser side is packages/core/src/lib/e2ecrypto.ts; the two must
// agree on exactly two things: SPKI/PKCS#8 DER in base64, and RSA-OAEP
// with SHA-256. Both are WebCrypto-native, so no conversion step exists to
// get wrong.

// EscrowAlg is the only escrow algorithm this version understands. It is
// written into every escrow slot so a future format change is legible
// rather than silent.
const EscrowAlg = "RSA-OAEP-256"

// EscrowKeyBits is the modulus size `filex e2e-escrow keygen` mints.
// 3072 buys a comfortable margin over 2048 while still generating in about
// a second, which matters because an operator runs it interactively.
const EscrowKeyBits = 3072

// EscrowMinKeyBits rejects a public key too small to take seriously.
const EscrowMinKeyBits = 2048

// ErrNoEscrowKey is returned when no escrow key is configured at all.
var ErrNoEscrowKey = errors.New("e2e: no escrow key configured")

// EscrowKey is a parsed, validated installation escrow public key.
type EscrowKey struct {
	// KID is the first 8 bytes of SHA-256(SPKI) in hex — the short name
	// that appears in every escrow slot and in the boot banner.
	KID string
	// SPKI is the DER public key, base64, exactly as the browser imports it.
	SPKI string
	Bits int

	pub *rsa.PublicKey
}

// EscrowKeyID is the stable short name of an escrow key: the first 8 bytes
// of SHA-256 over the SPKI DER, hex-encoded. The browser computes the same
// value from the same bytes (escrowKeyId in e2ecrypto.ts).
func EscrowKeyID(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return hex.EncodeToString(sum[:8])
}

// ParseEscrowPublicKey validates an operator-supplied escrow public key.
//
// Accepts base64 SPKI DER with or without PEM armour and with any
// whitespace, because it arrives through an env var that has been through
// a shell, a .env file and probably a copy-paste.
func ParseEscrowPublicKey(s string) (*EscrowKey, error) {
	clean := stripArmour(s)
	if clean == "" {
		return nil, ErrNoEscrowKey
	}
	der, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("e2e: escrow public key is not valid base64: %w", err)
	}
	anyKey, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("e2e: escrow public key is not a DER SPKI public key: %w", err)
	}
	pub, ok := anyKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("e2e: escrow public key must be RSA (%s is not supported)", keyKind(anyKey))
	}
	bits := pub.N.BitLen()
	if bits < EscrowMinKeyBits {
		return nil, fmt.Errorf("e2e: escrow public key is %d bits, minimum is %d", bits, EscrowMinKeyBits)
	}
	return &EscrowKey{
		KID:  EscrowKeyID(der),
		SPKI: base64.StdEncoding.EncodeToString(der),
		Bits: bits,
		pub:  pub,
	}, nil
}

// SealNonce encrypts a challenge nonce to the escrow public key, so that
// only a caller actually holding the private half can echo it back.
//
// This is what turns "someone claims they used the escrow key" into
// "someone demonstrably had the escrow key in their browser". It does NOT
// stop an operator from decrypting files offline with the same key and
// never telling anyone — see docs/E2E-ENCRYPTION.md.
func (k *EscrowKey) SealNonce(nonce []byte) ([]byte, error) {
	if k == nil || k.pub == nil {
		return nil, ErrNoEscrowKey
	}
	return rsa.EncryptOAEP(sha256.New(), rand.Reader, k.pub, nonce, nil)
}

// GenerateEscrowKeyPair mints a fresh escrow keypair and returns both
// halves base64-encoded: SPKI for the server's config, PKCS#8 for the
// admin's safe.
//
// ⚠ The private half is returned to the caller and never written anywhere
// by filex. `filex e2e-escrow keygen` prints it once; that is the only
// copy that will ever exist.
func GenerateEscrowKeyPair(bits int) (spkiB64, pkcs8B64 string, err error) {
	if bits < EscrowMinKeyBits {
		bits = EscrowKeyBits
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return "", "", fmt.Errorf("e2e: escrow keygen: %w", err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("e2e: escrow keygen (public): %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("e2e: escrow keygen (private): %w", err)
	}
	return base64.StdEncoding.EncodeToString(spki), base64.StdEncoding.EncodeToString(pkcs8), nil
}

// stripArmour removes PEM header/footer lines and all whitespace.
func stripArmour(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-----") {
			continue
		}
		for _, r := range line {
			if r != ' ' && r != '\t' && r != '\r' {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func keyKind(k any) string {
	return fmt.Sprintf("%T", k)
}

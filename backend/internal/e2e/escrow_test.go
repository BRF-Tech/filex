package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateEscrowKeyPair_RoundTrips(t *testing.T) {
	spki, pkcs8, err := GenerateEscrowKeyPair(EscrowMinKeyBits)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	key, err := ParseEscrowPublicKey(spki)
	if err != nil {
		t.Fatalf("parse public: %v", err)
	}
	if key.Bits != EscrowMinKeyBits {
		t.Fatalf("bits = %d, want %d", key.Bits, EscrowMinKeyBits)
	}

	// The nonce the browser has to be able to read back. This is the exact
	// operation handlers.E2E performs to prove possession.
	nonce := make([]byte, escrowTestNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sealed, err := key.SealNonce(nonce)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	der, err := base64.StdEncoding.DecodeString(pkcs8)
	if err != nil {
		t.Fatal(err)
	}
	anyPriv, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatalf("the private half must be PKCS#8, which is what WebCrypto imports: %v", err)
	}
	priv, ok := anyPriv.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("private key is %T", anyPriv)
	}
	out, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, sealed, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(out) != string(nonce) {
		t.Fatal("the nonce did not survive the round trip")
	}
}

const escrowTestNonceLen = 32

func TestEscrowKeyID_IsStableAndDerivedFromTheSPKI(t *testing.T) {
	spki, _, err := GenerateEscrowKeyPair(EscrowMinKeyBits)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ParseEscrowPublicKey(spki)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseEscrowPublicKey(spki)
	if err != nil {
		t.Fatal(err)
	}
	if a.KID != b.KID {
		t.Fatal("the same key must produce the same id — markers are stamped with it")
	}
	if len(a.KID) != 16 {
		t.Fatalf("kid = %q, want 8 bytes of hex", a.KID)
	}
	der, _ := base64.StdEncoding.DecodeString(spki)
	sum := sha256.Sum256(der)
	if a.KID != EscrowKeyID(der) {
		t.Fatal("KID must be SHA-256(SPKI)[:8]; the browser computes it the same way")
	}
	_ = sum
}

func TestParseEscrowPublicKey_Rejects(t *testing.T) {
	if _, err := ParseEscrowPublicKey(""); err == nil {
		t.Fatal("empty must be ErrNoEscrowKey, not a key")
	}
	if _, err := ParseEscrowPublicKey("not base64 @@@"); err == nil {
		t.Fatal("garbage must not parse")
	}
	// A valid base64 blob that is not an SPKI.
	if _, err := ParseEscrowPublicKey(base64.StdEncoding.EncodeToString([]byte("hello"))); err == nil {
		t.Fatal("non-DER must not parse")
	}
	// A real key, but too small to take seriously.
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&small.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseEscrowPublicKey(base64.StdEncoding.EncodeToString(der)); err == nil {
		t.Fatalf("a %d-bit escrow key must be refused", small.N.BitLen())
	}
}

func TestParseEscrowPublicKey_SurvivesTheJourneyThroughAnEnvVar(t *testing.T) {
	// The value passes through a shell, a .env file, a Kubernetes secret and
	// at least one copy-paste. PEM armour and stray whitespace are normal.
	spki, _, err := GenerateEscrowKeyPair(EscrowMinKeyBits)
	if err != nil {
		t.Fatal(err)
	}
	want, err := ParseEscrowPublicKey(spki)
	if err != nil {
		t.Fatal(err)
	}

	wrapped := "-----BEGIN PUBLIC KEY-----\n" +
		strings.Join(chunk(spki, 64), "\n") +
		"\n-----END PUBLIC KEY-----\n"
	for name, in := range map[string]string{
		"pem":              wrapped,
		"leading spaces":   "   " + spki,
		"embedded newline": spki[:20] + "\n" + spki[20:],
		"trailing tab":     spki + "\t",
	} {
		got, err := ParseEscrowPublicKey(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got.KID != want.KID {
			t.Errorf("%s: kid = %q, want %q", name, got.KID, want.KID)
		}
	}
}

func chunk(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

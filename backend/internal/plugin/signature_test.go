package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The signing story in one place, because it was written, documented, and
// until now never measured — the sort of code that looks like a guarantee and
// is only a hope until a test drives it.
//
// What filex signs is the binary's sha256, lower-case hex, as a STRING. That
// choice is the one worth pinning: it lets an operator sign the same digest
// they already publish next to a release, with any ed25519 tool, instead of
// handing the whole binary to a signing step.

func sign(t *testing.T, priv ed25519.PrivateKey, sum string) string {
	t.Helper()
	return hex.EncodeToString(ed25519.Sign(priv, []byte(sum)))
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestCheckSignatureUnconfiguredAcceptsAnything(t *testing.T) {
	m := &Manager{}
	if err := m.checkSignature(sumOf([]byte("hello")), ""); err != nil {
		t.Fatalf("with no trusted key configured nothing should be demanded: %v", err)
	}
}

func TestCheckSignatureRefusesUnsigned(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{trusted: []ed25519.PublicKey{pub}}
	err = m.checkSignature(sumOf([]byte("payload")), "")
	if err == nil {
		t.Fatal("an unsigned plugin must not install on an instance that configured trusted keys")
	}
	// The message has to say what to do; "invalid signature" sends the
	// operator hunting for a signature they never made.
	if !strings.Contains(err.Error(), "FILEX_PLUGIN_TRUSTED_KEYS") {
		t.Fatalf("the refusal should name the setting that caused it: %v", err)
	}
}

func TestCheckSignatureAcceptsHexAndBase64(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{trusted: []ed25519.PublicKey{pub}}
	sum := sumOf([]byte("a real binary would go here"))

	if err := m.checkSignature(sum, sign(t, priv, sum)); err != nil {
		t.Fatalf("hex signature rejected: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sum)))
	if err := m.checkSignature(sum, b64); err != nil {
		t.Fatalf("base64 signature rejected: %v", err)
	}
	if err := m.checkSignature(sum, "  "+sign(t, priv, sum)+"\n"); err != nil {
		t.Fatalf("a signature pasted with whitespace should still verify: %v", err)
	}
	if err := m.checkSignature(strings.ToUpper(sum), sign(t, priv, sum)); err != nil {
		t.Fatalf("sha256sum output in upper case should verify too: %v", err)
	}
}

func TestCheckSignatureRefusesWrongKeyAndTamperedFile(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = other
	sum := sumOf([]byte("original"))

	m := &Manager{trusted: []ed25519.PublicKey{pub}}
	if err := m.checkSignature(sum, sign(t, otherPriv, sum)); err == nil {
		t.Fatal("a signature from a key nobody trusts must be refused")
	}

	// The point of signing the hash: change one byte of the plugin and the
	// signature no longer belongs to it.
	if err := m.checkSignature(sumOf([]byte("originaX")), sign(t, priv, sum)); err == nil {
		t.Fatal("a signature over a different sha256 must not verify")
	}
	if err := m.checkSignature(sum, "not-a-signature!!"); err == nil {
		t.Fatal("garbage must not verify")
	}
}

func TestCheckSignatureAnyTrustedKeyWorks(t *testing.T) {
	// Several keys means several signers, not several signatures: rotation
	// and a second maintainer both depend on this.
	pubA, _, _ := ed25519.GenerateKey(rand.Reader)
	pubB, privB, _ := ed25519.GenerateKey(rand.Reader)
	m := &Manager{trusted: []ed25519.PublicKey{pubA, pubB}}
	sum := sumOf([]byte("signed by the second maintainer"))
	if err := m.checkSignature(sum, sign(t, privB, sum)); err != nil {
		t.Fatalf("the second trusted key should verify: %v", err)
	}
}

func TestParsePublicKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	for name, in := range map[string]string{
		"hex":            hex.EncodeToString(pub),
		"base64":         base64.StdEncoding.EncodeToString(pub),
		"hex padded":     "  " + hex.EncodeToString(pub) + "\n",
		"hex upper case": strings.ToUpper(hex.EncodeToString(pub)),
	} {
		got, err := parsePublicKey(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !got.Equal(pub) {
			t.Fatalf("%s: parsed a different key", name)
		}
	}
	for name, in := range map[string]string{
		"empty":       "",
		"too short":   hex.EncodeToString(pub[:16]),
		"not a key":   "hunter2",
		"private key": hex.EncodeToString(make([]byte, ed25519.PrivateKeySize)),
	} {
		if _, err := parsePublicKey(in); err == nil {
			t.Fatalf("%s should not parse as a public key", name)
		}
	}
}

// TestNewCarriesTrustedKeys is the test whose absence let the bug through.
//
// Every other test here builds a Manager with `trusted` set by hand, so they
// all passed while New() computed the parsed keys into a local variable and
// dropped them on the floor — the struct literal simply never assigned the
// field. Validation still ran, so a malformed key was rejected and the
// setting looked alive; enforcement was off for every caller, embedder and
// environment variable alike. Construct it the way the server does.
func TestNewCarriesTrustedKeys(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, store := dbtest.NewTestDB(t)
	m, err := New(Options{Store: store, Dir: t.TempDir(), SecretKey: "test-secret-key",
		TrustedKeys: []string{hex.EncodeToString(pub)}})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(m.Shutdown)
	if !m.RequiresSignature() {
		t.Fatal("a manager built with a trusted key must require signatures")
	}
	if err := m.checkSignature(sumOf([]byte("anything")), ""); err == nil {
		t.Fatal("and it must actually refuse an unsigned plugin")
	}
}

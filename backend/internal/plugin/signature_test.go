package plugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// The signing story in one place, because it was written, documented, and
// until now never measured — the sort of code that looks like a guarantee and
// is only a hope until a test drives it.
//
// The checkSignature tests below pin the sha256-only form: the binary's
// sha256, lower-case hex, as a STRING (what an app module is still signed
// over, and what a storage plugin build was signed over until 0.55 - its old
// form, accepted in 0.55 with a warning). A storage plugin build is now
// signed over its text - name, version, platform, sha256 (VerifyBuild, the
// tests at the end of this file).

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

// ── A storage plugin build's signature (sec055 S5) ──────────────────────

// The build text, pinned by one vector that apps.filex.sh's
// backend/internal/signing/signing_test.go (TestStorageBuild_TheSharedVector)
// holds too: seed 32 x 0x07, myfs 1.3.0 on linux/amd64, sha256 64 x "1".
// If either program changes what is signed, both tests go red.
func TestVerifyBuild_TheSharedVector(t *testing.T) {
	const (
		publicHex  = "ea4a6c63e29c520abef5507b132ec5f9954776aebebe7b92421eea691446d22c"
		payloadHex = "66696c65782d73746f726167652d6275696c643a76310a6d7966730a312e332e300a6c696e75782f616d6436340a31313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131313131"
		sigHex     = "bc0be1ad6a3b7411c69dbe14e53c78192070a068843844173f01c2df8ada9a1d8da12d30c3f8ba5c07b34511c8600005d2b4cb7ccf4134c18da558c803bfad09"
	)
	sha := strings.Repeat("1", 64)
	c := BuildClaim{Name: "myfs", Version: "v1.3.0", Platform: "Linux/AMD64", SHA256: strings.ToUpper(sha)}
	if got := hex.EncodeToString(c.Payload()); got != payloadHex {
		t.Fatalf("the build text changed:\n got %s\nwant %s", got, payloadHex)
	}
	pubRaw, _ := hex.DecodeString(publicHex)
	pub := ed25519.PublicKey(pubRaw)
	v, err := VerifyBuild([]ed25519.PublicKey{pub}, c, sigHex)
	if err != nil {
		t.Fatalf("the shared vector does not verify: %v", err)
	}
	if v.Legacy {
		t.Fatal("a signature over the build text was taken for the old sha256-only form")
	}
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	if got := hex.EncodeToString(ed25519.Sign(priv, c.Payload())); got != sigHex {
		t.Fatalf("signing the text gives %s, want %s", got, sigHex)
	}
}

// What the signature names is what it vouches for: the same signature does
// not verify as another platform's, another version's or another plugin's
// build, and the old form (the sha256 alone) verifies only as that.
func TestVerifyBuild_TheSignatureNamesTheBuild(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trusted := []ed25519.PublicKey{pub}
	sum := sumOf([]byte("a build"))
	c := BuildClaim{Name: "myfs", Version: "1.3.0", Platform: "linux/amd64", SHA256: sum}
	sig := hex.EncodeToString(ed25519.Sign(priv, c.Payload()))

	if v, err := VerifyBuild(trusted, c, sig); err != nil || v.Legacy {
		t.Fatalf("the build's own signature: %+v %v", v, err)
	}
	for _, other := range []BuildClaim{
		{Name: "myfs", Version: "1.3.0", Platform: "linux/arm64", SHA256: sum},
		{Name: "myfs", Version: "1.2.0", Platform: "linux/amd64", SHA256: sum},
		{Name: "otherfs", Version: "1.3.0", Platform: "linux/amd64", SHA256: sum},
		{Name: "myfs", Version: "", Platform: "linux/amd64", SHA256: sum},
	} {
		if _, err := VerifyBuild(trusted, other, sig); !errors.Is(err, ErrSignatureInvalid) {
			t.Errorf("myfs 1.3.0 linux/amd64's signature verified as %+v (%v)", other, err)
		}
	}

	// The old form: still taken during 0.55, and said to be the old form.
	old := sign(t, priv, sum)
	v, err := VerifyBuild(trusted, c, old)
	if err != nil || !v.Legacy {
		t.Fatalf("a signature over the sha256 alone: %+v %v, want accepted as legacy", v, err)
	}
	if !bytes.Equal(v.Key, pub) {
		t.Fatal("the key that verified is not reported")
	}

	// The text is not the sha256: a signature over the build text never
	// verifies as a sha256-only signature (the form wasm modules use).
	if err := VerifyDetached(trusted, sum, sig); err == nil {
		t.Fatal("a build signature verified as a signature over the sha256 alone")
	}
}

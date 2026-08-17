package secretbox

import (
	"errors"
	"strings"
	"testing"
)

const key = "a-long-enough-test-key-from-the-environment"

func TestRoundTrip(t *testing.T) {
	b, err := New(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, secret := range []string{"", "s3cr3t", strings.Repeat("x", 4096), "ünïcode ✓"} {
		sealed, err := b.Seal(secret)
		if err != nil {
			t.Fatalf("Seal(%q): %v", secret, err)
		}
		if strings.Contains(sealed, secret) && secret != "" {
			t.Errorf("Seal(%q) leaked the plaintext into %q", secret, sealed)
		}
		got, err := b.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if got != secret {
			t.Errorf("round trip = %q, want %q", got, secret)
		}
	}
}

// Sealing the same secret twice must not produce the same bytes, or the column
// itself reveals which accounts share a secret.
func TestSealIsNonDeterministic(t *testing.T) {
	b, _ := New(key)
	a, _ := b.Seal("same")
	c, _ := b.Seal("same")
	if a == c {
		t.Fatal("two seals of the same secret are identical — the nonce is not random")
	}
	for _, s := range []string{a, c} {
		got, err := b.Open(s)
		if err != nil || got != "same" {
			t.Fatalf("Open(%q) = %q, %v", s, got, err)
		}
	}
}

// A wrong key must fail, not return garbage. GCM's tag is what guarantees it;
// this test is here so nobody replaces the mode with something unauthenticated.
func TestWrongKeyIsRefusedNotGuessed(t *testing.T) {
	b, _ := New(key)
	sealed, _ := b.Seal("s3cr3t")

	other, _ := New("a-completely-different-key")
	if _, err := other.Open(sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open with the wrong key = %v, want ErrCorrupt", err)
	}
}

func TestTamperedCiphertextIsRefused(t *testing.T) {
	b, _ := New(key)
	sealed, _ := b.Seal("s3cr3t")

	// Flip a byte in the middle of the payload.
	body := []byte(sealed)
	body[len(body)/2] ^= 0x01
	if _, err := b.Open(string(body)); !errors.Is(err, ErrCorrupt) {
		t.Errorf("tampered ciphertext = %v, want ErrCorrupt", err)
	}
	if _, err := b.Open(prefix + "not-base64!!"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("malformed base64 = %v, want ErrCorrupt", err)
	}
	if _, err := b.Open(prefix + "AAAA"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("too short to hold a nonce = %v, want ErrCorrupt", err)
	}
}

// Without a key, encryption must FAIL rather than quietly store plaintext: a
// "temporarily unencrypted" secret is one nobody ever goes back for.
func TestNoKeyRefusesToSealAndSaysSoOnOpen(t *testing.T) {
	b, err := New("")
	if err != nil {
		t.Fatalf("New(\"\"): %v", err)
	}
	if b.Enabled() {
		t.Error("a box with no key reports itself enabled")
	}
	if _, err := b.Seal("s3cr3t"); !errors.Is(err, ErrNoKey) {
		t.Errorf("Seal without a key = %v, want ErrNoKey", err)
	}

	// An encrypted row with no key must report the missing key, not corruption
	// — otherwise the operator goes looking for data damage that is not there.
	withKey, _ := New(key)
	sealed, _ := withKey.Seal("s3cr3t")
	if _, err := b.Open(sealed); !errors.Is(err, ErrNoKey) {
		t.Errorf("Open of an encrypted row without a key = %v, want ErrNoKey", err)
	}
}

// The prefix is what lets a column hold legacy plaintext beside encrypted rows
// during a migration without ever guessing which is which.
func TestPlaintextPassesThrough(t *testing.T) {
	for _, b := range []*Box{mustBox(t, key), mustBox(t, "")} {
		got, err := b.Open("legacy-plaintext-secret")
		if err != nil || got != "legacy-plaintext-secret" {
			t.Errorf("Open(plaintext) = %q, %v", got, err)
		}
	}
	if IsSealed("legacy-plaintext-secret") {
		t.Error("IsSealed said a plaintext value was encrypted")
	}
	sealed, _ := mustBox(t, key).Seal("x")
	if !IsSealed(sealed) {
		t.Error("IsSealed did not recognise its own output")
	}
}

func mustBox(t *testing.T, k string) *Box {
	t.Helper()
	b, err := New(k)
	if err != nil {
		t.Fatalf("New(%q): %v", k, err)
	}
	return b
}

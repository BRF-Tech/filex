package appstore

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func newKey(t *testing.T, id, use, status string) (Key, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Key{ID: id, Use: use, Ed25519: hex.EncodeToString(pub), Status: status}, priv
}

func sign(t *testing.T, priv ed25519.PrivateKey, keyID string, payload any) *Envelope {
	t.Helper()
	raw, _ := json.Marshal(payload)
	c, err := Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(c)
	return &Envelope{Payload: raw, KeyID: keyID, Signature: hex.EncodeToString(ed25519.Sign(priv, []byte(hex.EncodeToString(sum[:]))))}
}

func TestVerify_TheKeyNamedOfTheRightUseAndOnlyIt(t *testing.T) {
	idx, idxPriv := newKey(t, "idx-1", UseIndex, KeyActive)
	lic, licPriv := newKey(t, "lic-1", UseLicense, KeyActive)
	trusted := []Key{idx, lic}
	payload := map[string]any{"app": "sign", "version": "1.0.0"}

	if err := Verify(sign(t, idxPriv, "idx-1", payload), trusted, UseIndex); err != nil {
		t.Fatalf("a good intent signature is refused: %v", err)
	}
	// A license key signing an install link: refused, although it is trusted.
	if err := Verify(sign(t, licPriv, "lic-1", payload), trusted, UseIndex); !errors.Is(err, ErrWrongKeyUse) {
		t.Fatalf("a license key's signature on an intent: want ErrWrongKeyUse, got %v", err)
	}
	// key_id names the index key but the license key signed: only the named
	// key is tried, so the signature fails.
	if err := Verify(sign(t, licPriv, "idx-1", payload), trusted, UseIndex); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("a signature by another trusted key under this key's id: want ErrSignatureInvalid, got %v", err)
	}
	// An id this store was not trusted with.
	_, strangerPriv := newKey(t, "x", UseIndex, KeyActive)
	if err := Verify(sign(t, strangerPriv, "idx-9", payload), trusted, UseIndex); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an unknown key id: want ErrUnknownKey, got %v", err)
	}
}

func TestVerify_TamperedPayloadAndNotYetActiveKey(t *testing.T) {
	idx, priv := newKey(t, "idx-1", UseIndex, KeyActive)
	env := sign(t, priv, "idx-1", map[string]any{"app": "sign", "version": "1.0.0"})
	env.Payload = json.RawMessage(`{"app":"sign","version":"9.9.9"}`)
	if err := Verify(env, []Key{idx}, UseIndex); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("tampered payload: want ErrSignatureInvalid, got %v", err)
	}
	next, nextPriv := newKey(t, "idx-2", UseIndex, KeyNext)
	if err := Verify(sign(t, nextPriv, "idx-2", map[string]any{"a": 1}), []Key{next}, UseIndex); !errors.Is(err, ErrKeyNotActive) {
		t.Fatalf("a `next` key signing: want ErrKeyNotActive, got %v", err)
	}
	retired, retPriv := newKey(t, "idx-0", UseIndex, KeyRetired)
	if err := Verify(sign(t, retPriv, "idx-0", map[string]any{"a": 1}), []Key{retired}, UseIndex); !errors.Is(err, ErrKeyNotActive) {
		t.Fatalf("a retired key signing: want ErrKeyNotActive, got %v", err)
	}
}

// The signature is over the CANONICAL bytes: the same payload sent in
// another key order and with whitespace verifies; a store that signed its
// own non-canonical serialisation does not.
func TestVerify_SignatureIsOverTheCanonicalForm(t *testing.T) {
	idx, priv := newKey(t, "idx-1", UseIndex, KeyActive)
	env := sign(t, priv, "idx-1", map[string]any{"b": 2, "a": "x"})
	env.Payload = json.RawMessage("{\n  \"b\" : 2,\n  \"a\" : \"x\"\n}")
	if err := Verify(env, []Key{idx}, UseIndex); err != nil {
		t.Fatalf("a reordered, indented payload must verify: %v", err)
	}
	raw := []byte(`{"b":2,"a":"x"}`) // NOT canonical (keys unsorted)
	sum := sha256.Sum256(raw)
	bad := &Envelope{Payload: raw, KeyID: "idx-1", Signature: hex.EncodeToString(ed25519.Sign(priv, []byte(hex.EncodeToString(sum[:]))))}
	if err := Verify(bad, []Key{idx}, UseIndex); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("a signature over non-canonical bytes: want ErrSignatureInvalid, got %v", err)
	}
	// Base64 is read too (the plugin rule).
	sig, _ := hex.DecodeString(env.Signature)
	env.Signature = base64.StdEncoding.EncodeToString(sig)
	if err := Verify(env, []Key{idx}, UseIndex); err != nil {
		t.Fatalf("a base64 signature: %v", err)
	}
}

func TestKeySet_ValidateAndTrustedKeys(t *testing.T) {
	a, _ := newKey(t, "idx-1", UseIndex, KeyActive)
	b, _ := newKey(t, "lic-1", UseLicense, KeyNext)
	c, _ := newKey(t, "art-1", UseArtifact, KeyActive)
	d, _ := newKey(t, "idx-0", UseIndex, KeyRetired)
	ks := KeySet{Keys: []Key{a, b, c, d}}
	if err := ks.Validate(); err != nil {
		t.Fatal(err)
	}
	got := ks.TrustedKeys()
	if len(got) != 2 || got[0].ID != "idx-1" || got[1].ID != "lic-1" {
		t.Fatalf("trusted keys: %+v (want idx-1 and lic-1: no artifact key, no retired key)", got)
	}
	dup := KeySet{Keys: []Key{a, a}}
	if dup.Validate() == nil {
		t.Fatal("a key id twice must refuse the list")
	}
	bad := KeySet{Keys: []Key{{ID: "k", Use: "everything", Ed25519: a.Ed25519}}}
	if bad.Validate() == nil {
		t.Fatal("an unknown use must refuse the list")
	}
}

func TestNormalizeOrigin(t *testing.T) {
	ok := map[string]string{
		"https://Fapps.BRFD.app":     "https://fapps.brfd.app",
		"https://fapps.brfd.app/":    "https://fapps.brfd.app",
		"https://fapps.brfd.app:443": "https://fapps.brfd.app",
		"https://s.example:8443":     "https://s.example:8443",
	}
	for in, want := range ok {
		got, err := NormalizeOrigin(in, false)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"http://fapps.brfd.app", "https://fapps.brfd.app/v1", "https://u:p@fapps.brfd.app",
		"https://fapps.brfd.app?x=1", "javascript:alert(1)", "http://127.0.0.1:9000", "ftp://x",
	} {
		if _, err := NormalizeOrigin(in, false); err == nil {
			t.Fatalf("%q must be refused", in)
		}
	}
	if got, err := NormalizeOrigin("http://127.0.0.1:9000", true); err != nil || got != "http://127.0.0.1:9000" {
		t.Fatalf("loopback with loopback sources: %q %v", got, err)
	}
	if _, err := NormalizeOrigin("http://10.0.0.5", true); err == nil {
		t.Fatal("plain http to the private network must be refused even with loopback sources")
	}
}

func TestPrefixNeverTheKey(t *testing.T) {
	for _, k := range []string{"FXL-7Q2M-K9P4-ZZ31", "abcd", "abcdefghijkl"} {
		p := strings.TrimSuffix(Prefix(k), "…")
		if len(p) >= len(k)/2 || !strings.HasPrefix(k, p) {
			t.Fatalf("prefix of %q is %q: at most a third of the key, and its start", k, Prefix(k))
		}
	}
	if Prefix("FXL-7Q2M-K9P4-ZZ31") != "FXL-7Q…" {
		t.Fatalf("prefix: %q", Prefix("FXL-7Q2M-K9P4-ZZ31"))
	}
}

// A key id is shown on the trust question beside the fingerprint: a short
// ASCII name, never text that could make the question read otherwise
// (front-end review #7).
func TestKeyID_IsAShortASCIIName(t *testing.T) {
	good, _ := newKey(t, "idx-2026.1_a", UseIndex, KeyActive)
	if err := (&KeySet{Keys: []Key{good}}).Validate(); err != nil {
		t.Fatalf("a plain id: %v", err)
	}
	rlo := string(rune(0x202e))
	for _, id := range []string{"idx 1", "<b>idx</b>", "idx" + rlo + "1-xdi", "ключ", strings.Repeat("a", 65), "idx/1", "idx:1"} {
		k, _ := newKey(t, id, UseIndex, KeyActive)
		if err := (&KeySet{Keys: []Key{k}}).Validate(); err == nil {
			t.Fatalf("key id %q was accepted", id)
		}
		raw := []byte(`{"payload":{"a":1},"key_id":` + strconvQuote(id) + `,"signature":"00"}`)
		if _, err := ParseEnvelope(raw); !errors.Is(err, ErrMalformed) {
			t.Fatalf("envelope key_id %q: want ErrMalformed, got %v", id, err)
		}
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

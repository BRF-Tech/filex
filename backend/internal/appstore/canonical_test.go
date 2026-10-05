package appstore

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

func TestCanonical_SortsKeysDropsSpaceKeepsNumbers(t *testing.T) {
	in := `{ "b": [ 3, 1.50, -0, 2e10 ], "a": { "z": true, "y": null, "x": "q" } }`
	got, err := Canonical([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"x":"q","y":null,"z":true},"b":[3,1.50,-0,2e10]}`
	if string(got) != want {
		t.Fatalf("canonical:\n got %s\nwant %s", got, want)
	}
}

// What encoding/json.Marshal would change, and a store in JavaScript would
// not: `<`, `>`, `&`, U+2028/U+2029 are written as themselves; only `"`, `\`
// and the control characters are escaped, the way JSON.stringify does.
func TestCanonical_StringsAsJSONStringify(t *testing.T) {
	bs := string(rune(92))
	in := `{"s":"<a & b> ` + "\u2028" + ` Ş” \u0007 \t \n ` + bs + bs + ` \/ ` + bs + `u00e7"}`
	got, err := Canonical([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"s":"<a & b> ` + "\u2028" + ` Ş” ` + bs + `u0007 ` + bs + `t ` + bs + `n ` + bs + bs + ` / ç"}`
	if string(got) != want {
		t.Fatalf("canonical:\n got %q\nwant %q", got, want)
	}
}

func TestCanonical_RefusesWhatTwoReadersWouldReadTwoWays(t *testing.T) {
	for name, in := range map[string]string{
		"duplicate key":    `{"result":"revoked","result":"valid"}`,
		"nested duplicate": `{"a":{"k":1,"k":2}}`,
		"trailing data":    `{"a":1} {"b":2}`,
		"invalid utf-8":    "{\"a\":\"\xff\"}",
		"not json":         `{"a":}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Canonical([]byte(in)); !errors.Is(err, ErrNotCanonicalizable) {
				t.Fatalf("want ErrNotCanonicalizable, got %v", err)
			}
		})
	}
}

func TestCanonical_IsIdempotent(t *testing.T) {
	in := `{"z":[{"b":2,"a":1}],"a":"x"}`
	once, err := Canonical([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Canonical(once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Fatalf("not idempotent: %s vs %s", once, twice)
	}
}

// vector is one entry of testdata/vectors.json, produced by Node's crypto
// with no filex code (testdata/make-vectors.mjs) - the cross-implementation
// proof that a store written in another language and filex agree on the
// canonical bytes and the signature. The same vectors are in
// docs/APP-PLUGINS-API.md for a store's author.
type vector struct {
	Name         string `json:"name"`
	KeyID        string `json:"key_id"`
	Use          string `json:"use"`
	SeedHex      string `json:"seed_hex"`
	PublicHex    string `json:"public_hex"`
	Fingerprint  string `json:"fingerprint"`
	Envelope     string `json:"envelope"`
	Canonical    string `json:"canonical"`
	SHA256Hex    string `json:"sha256_hex"`
	SignatureHex string `json:"signature_hex"`
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	b, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Vectors) < 2 {
		t.Fatalf("want the intent and the license vector, got %d", len(doc.Vectors))
	}
	return doc.Vectors
}

func TestVectors_TheIndependentProducerAndFilexAgree(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			seed, _ := hex.DecodeString(v.SeedHex)
			priv := ed25519.NewKeyFromSeed(seed)
			if got := hex.EncodeToString(priv.Public().(ed25519.PublicKey)); got != v.PublicHex {
				t.Fatalf("public key from seed: %s, vector says %s", got, v.PublicHex)
			}
			env, err := ParseEnvelope([]byte(v.Envelope))
			if err != nil {
				t.Fatal(err)
			}
			c, err := Canonical(env.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if string(c) != v.Canonical {
				t.Fatalf("canonical differs from the vector's:\n got %s\nwant %s", c, v.Canonical)
			}
			sum, err := PayloadSHA256(env.Payload)
			if err != nil || sum != v.SHA256Hex {
				t.Fatalf("sha256: %s (%v), want %s", sum, err, v.SHA256Hex)
			}
			key := Key{ID: v.KeyID, Use: v.Use, Ed25519: v.PublicHex, Status: KeyActive}
			if key.Fingerprint() != v.Fingerprint {
				t.Fatalf("fingerprint %s, want %s", key.Fingerprint(), v.Fingerprint)
			}
			if err := Verify(env, []Key{key}, v.Use); err != nil {
				t.Fatalf("the vector does not verify: %v", err)
			}
			// The plugin rule itself, as a module's signature is checked.
			pub, _ := hex.DecodeString(v.PublicHex)
			if err := plugin.VerifyDetached([]ed25519.PublicKey{pub}, v.SHA256Hex, v.SignatureHex); err != nil {
				t.Fatalf("VerifyDetached refuses the vector: %v", err)
			}
		})
	}
}

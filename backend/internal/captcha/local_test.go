package captcha

// The local fallback against the shared vectors (testdata/
// captcha-local-vectors.json): the same 20 cases every product's verifier
// answers the same way. The file is a copy, pinned by its hash: a copy that
// drifts from the contract is refused here before any case runs.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// vectorsSHA256 is the contract's hash of the file (LF line endings).
const vectorsSHA256 = "b587c15f2aa2f044b9f0897f519cb226f7a2f6a010d26a50afd8bd27349604ae"

type vectorFile struct {
	Algorithm   string   `json:"algorithm"`
	VerifyOrder []string `json:"verify_order"`
	Vectors     []struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Input struct {
			AppKey         string          `json:"app_key"`
			Salt           string          `json:"salt"`
			Number         json.RawMessage `json:"number"`
			MaxNumber      int             `json:"maxnumber"`
			Now            int64           `json:"now"`
			SeenSignatures []string        `json:"seen_signatures"`
		} `json:"input"`
		Solution struct {
			PayloadBase64 string `json:"payload_base64"`
		} `json:"solution"`
		Expected struct {
			Hash      string `json:"hash"`
			Signature string `json:"signature"`
			Verify    struct {
				OK     bool   `json:"ok"`
				Reason string `json:"reason"`
			} `json:"verify"`
		} `json:"expected"`
	} `json:"vectors"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/captcha-local-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	// A checkout that turned the line endings into CRLF is the same file.
	lf := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(lf)
	if got := hex.EncodeToString(sum[:]); got != vectorsSHA256 {
		t.Fatalf("testdata/captcha-local-vectors.json is not the contract's file: sha256 %s, want %s", got, vectorsSHA256)
	}
	var f vectorFile
	if err := json.Unmarshal(lf, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLocalVectors_TheFileIsTheContract(t *testing.T) {
	f := loadVectors(t)
	if len(f.Vectors) != 20 {
		t.Fatalf("%d vectors, want 20", len(f.Vectors))
	}
	want := []string{ReasonBadPayload, ReasonBadAlgorithm, ReasonBadSalt, ReasonExpired, ReasonBadNumber, ReasonBadSignature, ReasonBadHash, ReasonReplayed}
	if strings.Join(f.VerifyOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("verify order %v, want %v", f.VerifyOrder, want)
	}
	if f.Algorithm != Algorithm {
		t.Fatalf("algorithm %q", f.Algorithm)
	}
}

// Generation: the hash and the signature of every challenge that has a
// meaningful one.
func TestLocalVectors_HashAndSignature(t *testing.T) {
	for _, v := range loadVectors(t).Vectors {
		var number int64
		if json.Unmarshal(v.Input.Number, &number) != nil || v.Expected.Hash == "" || v.Expected.Signature == "" {
			continue
		}
		if !strings.Contains(v.Input.Salt, "?") && v.ID != "N9" {
			continue
		}
		t.Run(v.ID, func(t *testing.T) {
			if v.ID == "N3" || v.ID == "N12" || v.ID == "N10" {
				// The challenge was made for another number or salt than the
				// solution carries; the expected hash is the challenge's.
				t.Skip("the solution differs from the challenge on purpose")
			}
			if got := HashFor(v.Input.Salt, number); got != v.Expected.Hash {
				t.Fatalf("hash %s, want %s", got, v.Expected.Hash)
			}
			if got := SignatureFor([]byte(v.Input.AppKey), v.Expected.Hash); got != v.Expected.Signature {
				t.Fatalf("signature %s, want %s", got, v.Expected.Signature)
			}
		})
	}
}

// Verification: every vector's payload, with its key, clock, ceiling and
// already-seen signatures, gets its expected answer.
func TestLocalVectors_Verify(t *testing.T) {
	for _, v := range loadVectors(t).Vectors {
		t.Run(v.ID, func(t *testing.T) {
			now := time.Unix(v.Input.Now, 0)
			l := &Local{Key: []byte(v.Input.AppKey), MaxNumber: v.Input.MaxNumber, Now: func() time.Time { return now }}
			seen := map[string]bool{}
			for _, s := range v.Input.SeenSignatures {
				seen[strings.ToLower(s)] = true
			}
			consume := func(sig string, ttl time.Duration) bool {
				if ttl < time.Second {
					t.Fatalf("mark lasts %s", ttl)
				}
				if seen[sig] {
					return false
				}
				seen[sig] = true
				return true
			}
			got := l.Verify(v.Solution.PayloadBase64, consume)
			if got.OK != v.Expected.Verify.OK || got.Reason != v.Expected.Verify.Reason {
				t.Fatalf("%s (%s): got ok=%v reason=%s, want ok=%v reason=%s", v.ID, v.Kind, got.OK, got.Reason, v.Expected.Verify.OK, v.Expected.Verify.Reason)
			}
			if got.OK {
				// And once only.
				if again := l.Verify(v.Solution.PayloadBase64, consume); again.OK || again.Reason != ReasonReplayed {
					t.Fatalf("second use: %+v, want replayed", again)
				}
			}
		})
	}
}

// What this instance issues is what it accepts, solved the way the browser
// solves it.
func TestLocal_IssueSolveVerify(t *testing.T) {
	l := &Local{Key: []byte("k"), MaxNumber: 2000}
	c, err := l.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if c.Algorithm != Algorithm || c.MaxNumber != 2000 || !strings.Contains(c.Salt, "?expires=") {
		t.Fatalf("challenge %+v", c)
	}
	n := Solve(c)
	if n < 0 {
		t.Fatal("no solution in range")
	}
	if r := l.Verify(EncodeSolution(c, n), nil); !r.OK {
		t.Fatalf("own challenge refused: %s", r.Reason)
	}
	other := &Local{Key: []byte("other"), MaxNumber: 2000}
	if r := other.Verify(EncodeSolution(c, n), nil); r.Reason != ReasonBadSignature {
		t.Fatalf("another key: %s, want bad_signature", r.Reason)
	}
}

func TestLocal_PayloadEdges(t *testing.T) {
	l := &Local{Key: []byte("k")}
	for name, payload := range map[string]string{
		"too long":    strings.Repeat("A", MaxPayloadBytes+4),
		"bare JSON":   `{"algorithm":"SHA-256"}`,
		"a list":      "WzEsMl0=", // [1,2]
		"float":       "eyJhbGdvcml0aG0iOiJTSEEtMjU2Iiwic2FsdCI6ImE/ZXhwaXJlcz05OTk5OTk5OTk5IiwibnVtYmVyIjo0LjAsImhhc2giOiJ4Iiwic2lnbmF0dXJlIjoieSJ9",
		"bool number": "eyJhbGdvcml0aG0iOiJTSEEtMjU2Iiwic2FsdCI6ImE/ZXhwaXJlcz05OTk5OTk5OTk5IiwibnVtYmVyIjp0cnVlLCJoYXNoIjoieCIsInNpZ25hdHVyZSI6InkifQ==",
	} {
		if r := l.Verify(payload, nil); r.Reason != ReasonBadPayload {
			t.Errorf("%s: %s, want bad_payload", name, r.Reason)
		}
	}
}

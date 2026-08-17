package s3api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// The SDK's signer is the golden source here. It cannot verify — that is why
// this package exists — but it can SIGN, and if our canonical request differs
// from its by a single byte the signatures do not match. That makes these
// tests a cross-implementation check rather than a restatement of our own
// arithmetic.
//
// ⚠ The pinned SDK is from July 2024, which matters a great deal for the
// CHECKSUM behaviour a PutObject has to handle (see the release notes in the
// handover) and not at all for SigV4 itself: the signing algorithm has not
// changed since 2012. Using it as an oracle here is sound; using it as the
// only client in an end-to-end test would not be.

const (
	testAKID   = "FLXTESTACCESSKEY0000"
	testSecret = "sekritsekritsekritsekritsekritsekritsekr"
	testRegion = "us-east-1"
)

func lookupOK(id string) (string, error) {
	if id != testAKID {
		return "", errors.New("no such key")
	}
	return testSecret, nil
}

// signWithSDK signs a request the way a real client would.
func signWithSDK(t *testing.T, r *http.Request, payloadHash string, at time.Time) {
	t.Helper()
	// ⚠ The bare signer does NOT set X-Amz-Content-Sha256 — the S3 client adds
	// it in a middleware, and S3 requires it. Setting it here is what makes
	// this harness behave like a real client rather than like the signer.
	r.Header.Set("X-Amz-Content-Sha256", payloadHash)
	s := v4.NewSigner(func(o *v4.SignerOptions) {
		// S3's exception: the path is signed as-is, neither normalized nor
		// double-encoded.
		o.DisableURIPathEscaping = true
	})
	creds := aws.Credentials{AccessKeyID: testAKID, SecretAccessKey: testSecret}
	if err := s.SignHTTP(context.Background(), creds, r, payloadHash, "s3", testRegion, at); err != nil {
		t.Fatalf("sign: %v", err)
	}
}

// serverSide turns a client-shaped request into the shape a Go server sees:
// Host lifted out of the header map, RequestURI populated, body length known.
func serverSide(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	rec := httptest.NewRequest(r.Method, r.URL.String(), nil)
	rec.Host = r.URL.Host
	rec.ContentLength = r.ContentLength
	for k, vs := range r.Header {
		for _, v := range vs {
			rec.Header.Add(k, v)
		}
	}
	return rec
}

func verifyRoundTrip(t *testing.T, method, rawURL, payloadHash string, headers map[string]string, contentLength int64) error {
	t.Helper()
	at := time.Now().UTC()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.ContentLength = contentLength
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	signWithSDK(t, req, payloadHash, at)

	srv := serverSide(t, req)
	parsed, err := Parse(srv)
	if err != nil {
		return err
	}
	return Verify(srv, parsed, lookupOK, at)
}

func TestVerifyAcceptsWhatTheSDKSigns(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		url         string
		payloadHash string
		headers     map[string]string
		length      int64
	}{
		{"bare GET", http.MethodGet, "https://s3.filex.sh/bucket/key.txt", emptyPayloadHash, nil, 0},
		{"root listing", http.MethodGet, "https://s3.filex.sh/", emptyPayloadHash, nil, 0},
		{"query parameters", http.MethodGet,
			"https://s3.filex.sh/bucket?list-type=2&prefix=a/b&delimiter=%2F&max-keys=100", emptyPayloadHash, nil, 0},
		{"space in the key", http.MethodGet,
			"https://s3.filex.sh/bucket/my%20file%20(1).txt", emptyPayloadHash, nil, 0},
		{"turkish characters in the key", http.MethodGet,
			"https://s3.filex.sh/bucket/g%C3%B6k%C3%A7il/%C5%9Feyler.txt", emptyPayloadHash, nil, 0},
		{"unsigned payload PUT", http.MethodPut,
			"https://s3.filex.sh/bucket/upload.bin", unsignedPayload,
			map[string]string{"Content-Type": "application/octet-stream"}, 1024},
		{"signed payload PUT", http.MethodPut,
			"https://s3.filex.sh/bucket/upload.bin", hashHex([]byte("hello")),
			map[string]string{"Content-Type": "text/plain"}, 5},
		{"extra signed headers", http.MethodPut,
			"https://s3.filex.sh/bucket/meta.txt", emptyPayloadHash,
			map[string]string{"X-Amz-Meta-Owner": "ada", "X-Amz-Acl": "private"}, 0},
		{"repeated slashes are NOT normalized", http.MethodGet,
			"https://s3.filex.sh/bucket/a//b/./c.txt", emptyPayloadHash, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := verifyRoundTrip(t, c.method, c.url, c.payloadHash, c.headers, c.length); err != nil {
				t.Fatalf("verify: %v", err)
			}
		})
	}
}

// Every one of these is a real request that must be refused. A verifier that
// accepts what the SDK signs is only half a verifier.
func TestVerifyRejectsTampering(t *testing.T) {
	at := time.Now().UTC()

	build := func() *http.Request {
		req, _ := http.NewRequest(http.MethodGet, "https://s3.filex.sh/bucket/key.txt?x=1", nil)
		req.Header.Set("X-Amz-Meta-Note", "original")
		signWithSDK(t, req, emptyPayloadHash, at)
		return serverSide(t, req)
	}

	tamper := map[string]func(*http.Request){
		"path changed":   func(r *http.Request) { r.URL.Path = "/bucket/other.txt" },
		"query changed":  func(r *http.Request) { r.URL.RawQuery = "x=2" },
		"query added":    func(r *http.Request) { r.URL.RawQuery = "x=1&y=2" },
		"method changed": func(r *http.Request) { r.Method = http.MethodDelete },
		"host changed":   func(r *http.Request) { r.Host = "evil.example" },
		"signed header":  func(r *http.Request) { r.Header.Set("X-Amz-Meta-Note", "tampered") },
		"payload hash":   func(r *http.Request) { r.Header.Set("X-Amz-Content-Sha256", hashHex([]byte("x"))) },
		"signature bytes": func(r *http.Request) {
			r.Header.Set("Authorization", strings.Replace(r.Header.Get("Authorization"), "Signature=", "Signature=0", 1))
		},
	}
	for name, mutate := range tamper {
		t.Run(name, func(t *testing.T) {
			r := build()
			mutate(r)
			parsed, err := Parse(r)
			if err != nil {
				return // a mangled Authorization line is refused earlier, which is also correct
			}
			if err := Verify(r, parsed, lookupOK, at); !errors.Is(err, ErrSignatureMismatch) {
				t.Fatalf("verify(%s) = %v, want ErrSignatureMismatch", name, err)
			}
		})
	}
}

// An unknown key id and a wrong secret must be indistinguishable, or the
// endpoint tells an unauthenticated caller which key ids exist.
func TestUnknownKeyLooksLikeAWrongSignature(t *testing.T) {
	at := time.Now().UTC()
	req, _ := http.NewRequest(http.MethodGet, "https://s3.filex.sh/bucket/key.txt", nil)
	signWithSDK(t, req, emptyPayloadHash, at)
	r := serverSide(t, req)
	parsed, _ := Parse(r)

	none := func(string) (string, error) { return "", errors.New("no such key") }
	wrong := func(string) (string, error) { return "a-different-secret-entirely", nil }

	errNone := Verify(r, parsed, none, at)
	errWrong := Verify(r, parsed, wrong, at)
	if !errors.Is(errNone, ErrSignatureMismatch) || !errors.Is(errWrong, ErrSignatureMismatch) {
		t.Fatalf("unknown=%v wrong=%v, both want ErrSignatureMismatch", errNone, errWrong)
	}
}

func TestClockSkewIsRefused(t *testing.T) {
	at := time.Now().UTC()
	req, _ := http.NewRequest(http.MethodGet, "https://s3.filex.sh/bucket/key.txt", nil)
	signWithSDK(t, req, emptyPayloadHash, at)
	r := serverSide(t, req)
	parsed, _ := Parse(r)

	if err := Verify(r, parsed, lookupOK, at.Add(MaxSkew+time.Minute)); !errors.Is(err, ErrExpired) {
		t.Errorf("stale request = %v, want ErrExpired", err)
	}
	if err := Verify(r, parsed, lookupOK, at.Add(-(MaxSkew + time.Minute))); !errors.Is(err, ErrExpired) {
		t.Errorf("future request = %v, want ErrExpired", err)
	}
	if err := Verify(r, parsed, lookupOK, at.Add(MaxSkew-time.Minute)); err != nil {
		t.Errorf("within the window = %v, want accepted", err)
	}
}

func TestPresignedURL(t *testing.T) {
	at := time.Now().UTC()
	// ⚠ X-Amz-Expires must be on the URL BEFORE signing: it is part of the
	// canonical query. Appending it afterwards (the obvious mistake) changes
	// the string that was signed and every verification fails.
	req, _ := http.NewRequest(http.MethodGet, "https://s3.filex.sh/bucket/key.txt?X-Amz-Expires=900", nil)
	s := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: testAKID, SecretAccessKey: testSecret}
	signedURL, _, err := s.PresignHTTP(context.Background(), creds, req, unsignedPayload, "s3", testRegion, at,
		func(o *v4.SignerOptions) {})
	if err != nil {
		t.Fatalf("presign: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, signedURL, nil)
	r.Host = "s3.filex.sh"
	parsed, err := Parse(r)
	if err != nil {
		t.Fatalf("parse presigned: %v", err)
	}
	if !parsed.Presigned {
		t.Fatal("a query-signed request was not recognised as presigned")
	}
	if err := Verify(r, parsed, lookupOK, at); err != nil {
		t.Fatalf("verify presigned: %v", err)
	}
	// Past its window it must stop working, which is the entire point of a
	// presigned URL.
	if err := Verify(r, parsed, lookupOK, at.Add(parsed.Expires+time.Second)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired presign = %v, want ErrExpired", err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"wrong algorithm":  "AWS4-HMAC-SHA512 Credential=a/b/c/d/aws4_request, SignedHeaders=host, Signature=x",
		"short credential": algorithm + " Credential=a/b/c, SignedHeaders=host, Signature=x",
		"no signature":     algorithm + " Credential=a/2024/us/s3/aws4_request, SignedHeaders=host",
		"no scope suffix":  algorithm + " Credential=a/2024/us/s3/nope, SignedHeaders=host, Signature=x",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://s3.filex.sh/b/k", nil)
			if header != "" {
				r.Header.Set("Authorization", header)
			}
			if _, err := Parse(r); err == nil {
				t.Fatal("Parse accepted garbage")
			}
		})
	}
}

// Content-Length is signed by some clients and Go keeps it out of the header
// map, so a verifier that reads it from there produces a signature that never
// matches and an error that says nothing about why.
func TestContentLengthIsReadFromTheField(t *testing.T) {
	at := time.Now().UTC()
	req, _ := http.NewRequest(http.MethodPut, "https://s3.filex.sh/bucket/k.bin", strings.NewReader("hello"))
	req.ContentLength = 5
	req.Header.Set("Content-Length", "5")
	signWithSDK(t, req, hashHex([]byte("hello")), at)
	if !strings.Contains(req.Header.Get("Authorization"), "content-length") {
		t.Skip("this SDK version does not sign content-length; the guard is still correct")
	}

	r := serverSide(t, req)
	r.ContentLength = 5
	parsed, _ := Parse(r)
	if err := Verify(r, parsed, lookupOK, at); err != nil {
		t.Fatalf("verify with a signed content-length: %v", err)
	}
}

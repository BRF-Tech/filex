package s3

// Issue #16 — "failed to compute payload hash: failed to seek body to start,
// request stream is not seekable".
//
// UploadPart's signature promises io.Reader, but the SDK signs a part with the
// SHA256 of its payload: it reads the whole body to hash it and then rewinds to
// send it. A plain io.Reader has nothing to rewind, so the request died in the
// client — before a byte reached the object store — and the error named the
// SDK rather than the caller, which is why the reporter read it as a broken
// Garage. Reproduced here against the real SDK and a real HTTP server.
//
// ⚠⚠ It is not the PROVIDER that decides this, it is the SCHEME — and that is
// measured below rather than argued, because the obvious guess is wrong twice
// over. The subtests record what the SDK put in x-amz-content-sha256:
//
//   - https:// → "UNSIGNED-PAYLOAD". TLS already protects the body, so the
//     signer does not hash it, never reads it twice, and a plain io.Reader has
//     always worked.
//   - http://  → a real 64-char SHA256 digest. Without TLS the payload hash is
//     what binds the body to the signature, so the signer MUST read the whole
//     part and then rewind to send it.
//
// So a non-seekable part body fails over http:// and survives over https://,
// identically on every provider.
//
// That is why this shipped: every S3 endpoint filex had been pointed at is
// https://, and the reporter's Garage — a container on a podman network — is
// http://. The bug was never Garage-specific and never AWS-specific; it was
// waiting for the first plaintext endpoint.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// partRecorder answers UploadPart with an ETag and keeps what arrived.
type partRecorder struct {
	mu   sync.Mutex
	body []byte
	sha  string // x-amz-content-sha256 — what the signer decided to do
	hits int
}

func newPartServer(t *testing.T, tls bool) (*httptest.Server, *partRecorder) {
	t.Helper()
	rec := &partRecorder{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.body = b
		rec.sha = r.Header.Get("X-Amz-Content-Sha256")
		rec.hits++
		rec.mu.Unlock()
		w.Header().Set("ETag", `"part-etag"`)
		w.WriteHeader(http.StatusOK)
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, rec
}

func partDriver(t *testing.T, srv *httptest.Server) *Driver {
	t.Helper()
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("fake", "fake", ""),
		),
		awsconfig.WithRetryer(newRetryer),
	)
	require.NoError(t, err)
	d := &Driver{bucket: "b", region: "auto", endpoint: srv.URL, pathStyle: true}
	d.client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
		o.HTTPClient = srv.Client()
	})
	return d
}

// unseekable hides the Seek method a bytes.Reader would otherwise expose —
// the shape io.LimitReader produced, and the shape any caller may legitimately
// hand to an io.Reader parameter.
type unseekable struct{ r io.Reader }

func (u unseekable) Read(p []byte) (int, error) { return u.r.Read(p) }

func TestUploadPart_AcceptsAPlainReader(t *testing.T) {
	const unsignedPayload = "UNSIGNED-PAYLOAD"
	for _, tc := range []struct {
		name string
		tls  bool
		// wantUnsigned says whether the SDK is expected to skip hashing the
		// payload. Only the hashing path needs a rewind, which is why only
		// plaintext ever failed.
		wantUnsigned bool
	}{
		{"plaintext", false, false},
		{"tls", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := newPartServer(t, tc.tls)
			d := partDriver(t, srv)

			// 6 MiB: over maxRewindBytes, so this exercises the temp-file spool
			// rather than the in-memory replay.
			src := bytes.Repeat([]byte("filex-issue-16-"), (6<<20)/15)
			etag, err := d.UploadPart(context.Background(), "big.bin", "upload-1", 1,
				unseekable{bytes.NewReader(src)}, int64(len(src)))
			require.NoError(t, err, "a plain io.Reader is what the interface promises to accept")
			assert.Equal(t, "part-etag", etag)

			rec.mu.Lock()
			defer rec.mu.Unlock()
			require.Equal(t, 1, rec.hits)
			assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(src)),
				fmt.Sprintf("%x", sha256.Sum256(rec.body)),
				"the bytes that arrive must be the bytes that were signed")

			// The measurement that explains the whole bug report.
			if tc.wantUnsigned {
				assert.Equal(t, unsignedPayload, rec.sha,
					"TLS already protects the body, so the SDK never hashes it and a plain reader was always fine here")
			} else {
				assert.NotEqual(t, unsignedPayload, rec.sha)
				assert.Len(t, rec.sha, 64,
					"over plaintext the part is signed with a real SHA256 digest — computing it is what needs the rewind")
			}
		})
	}
}

// A body that is already seekable is passed through untouched: no copy, no
// temp file. The common case must stay free.
func TestUploadPart_SeekableBodyIsNotCopied(t *testing.T) {
	srv, rec := newPartServer(t, false)
	d := partDriver(t, srv)

	src := bytes.Repeat([]byte("x"), 5<<20)
	body := bytes.NewReader(src)
	got, release, err := partBody(body, int64(len(src)))
	require.NoError(t, err)
	release()
	assert.Same(t, body, got, "a seekable body is handed straight to the SDK")

	_, err = d.UploadPart(context.Background(), "seekable.bin", "upload-1", 1,
		bytes.NewReader(src), int64(len(src)))
	require.NoError(t, err)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Len(t, rec.body, len(src))
}

// A short body is a truncated part. Accepting it would let the provider
// assemble an object quietly missing bytes, which is worse than a failed
// upload — so it fails loudly and names both numbers.
func TestPartBody_RefusesAShortBody(t *testing.T) {
	_, _, err := partBody(unseekable{strings.NewReader("only nine")}, 9<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declared")
}

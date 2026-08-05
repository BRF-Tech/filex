package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturedPut records what the fake object store actually received on the
// wire for a PutObject.
type capturedPut struct {
	contentLength    int64
	transferEncoding []string
	body             []byte
}

// fakeS3 stands up an httptest server that answers every PutObject with 200
// and records the framing of the request. A real S3 endpoint that refuses
// chunked bodies (DT Cloud S3) answers 411 instead; asserting on
// Content-Length here is the same check without the network.
//
// The server speaks TLS on purpose. Over plain HTTP the SDK signs the
// payload and needs to rewind the body, which fails for a different reason
// than the one we're testing; over HTTPS it uses UNSIGNED-PAYLOAD, exactly
// like the production DT Cloud S3 endpoint, so what's left under test is
// the request framing.
func fakeS3(t *testing.T, got *capturedPut) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.contentLength = r.ContentLength
		got.transferEncoding = r.TransferEncoding
		got.body = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("fake", "fake", ""),
		),
	)
	require.NoError(t, err)

	d := &Driver{bucket: "b", region: "auto", endpoint: srv.URL, pathStyle: true}
	d.client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
		o.HTTPClient = srv.Client() // trusts the httptest cert
	})
	return d
}

// nonSeekable wraps a reader so it exposes ONLY io.Reader — no Seeker, no
// Len(). This is what the upload handler hands the driver: it sniffs the
// first 512 bytes for mime detection and rejoins them with
// io.MultiReader(bytes.NewReader(sniff), part), which the AWS SDK cannot
// measure on its own.
type nonSeekable struct{ r io.Reader }

func (n nonSeekable) Read(p []byte) (int, error) { return n.r.Read(p) }

// TestWrite_SendsContentLength — a non-seekable body must still go out with
// a Content-Length header. Without it the SDK falls back to
// Transfer-Encoding: chunked; AWS and MinIO accept that, but the S3 spec
// lets a provider answer 411 MissingContentLength and DT Cloud S3 does,
// which broke every browser upload (olivov H1, 2026-08-05).
func TestWrite_SendsContentLength(t *testing.T) {
	var got capturedPut
	d := fakeS3(t, &got)

	payload := []byte("hello olivov")
	body := nonSeekable{io.MultiReader(
		bytes.NewReader(payload[:5]),
		bytes.NewReader(payload[5:]),
	)}

	require.NoError(t, d.Write(context.Background(), "/x.txt", body, int64(len(payload))))

	assert.Equal(t, int64(len(payload)), got.contentLength,
		"PutObject must declare Content-Length for a non-seekable body")
	assert.Empty(t, got.transferEncoding,
		"body must not be sent chunked")
	assert.Equal(t, payload, got.body)
}

// TestWrite_SeekableStillWorks guards the path that already worked (MCP
// file_write, WebDAV spool, the .empty marker) so the fix doesn't regress
// it.
func TestWrite_SeekableStillWorks(t *testing.T) {
	var got capturedPut
	d := fakeS3(t, &got)

	require.NoError(t, d.Write(context.Background(), "/y.txt", strings.NewReader("abc"), 3))

	assert.Equal(t, int64(3), got.contentLength)
	assert.Empty(t, got.transferEncoding)
	assert.Equal(t, []byte("abc"), got.body)
}

// TestWrite_UnknownSizeBuffers — callers that genuinely don't know the
// length (size < 0) must still produce a measured request rather than a
// chunked one, since the provider would reject it.
func TestWrite_UnknownSizeBuffers(t *testing.T) {
	var got capturedPut
	d := fakeS3(t, &got)

	body := nonSeekable{strings.NewReader("unknown length")}
	require.NoError(t, d.Write(context.Background(), "/z.txt", body, -1))

	assert.Equal(t, int64(len("unknown length")), got.contentLength)
	assert.Empty(t, got.transferEncoding)
	assert.Equal(t, []byte("unknown length"), got.body)
}

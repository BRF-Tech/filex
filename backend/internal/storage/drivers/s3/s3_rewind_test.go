package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flakyS3 answers the first `fail` PutObject attempts with 503 and every
// attempt after that with 200, recording what finally arrived. It stands in
// for the transient wobble newRetryer's six-attempt budget exists to absorb.
func flakyS3(t *testing.T, fail int32, attempts *int32, got *[]byte) *Driver {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(attempts, 1)
		if n <= fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		*got = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("fake", "fake", ""),
		),
		// The production retryer, so the test measures the budget we ship.
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

// TestWrite_NonSeekableSurvivesTransient503 — the defect this file exists for.
//
// Real failure (fm.example.com, GlitchTip filex issue #428, 2026-08-30,
// v0.27.4, twice):
//
//	upload ticket: write failed  dest=s3-test://thnk/thy/kuyruk-sonuc.txt
//	err="operation error S3: PutObject, failed to rewind transport stream for
//	     retry, request stream is not seekable"
//
// ⚠⚠ WHY THIS IS WORSE THAN IT LOOKS: newRetryer widens the budget to six
// attempts, and TestRetryerAbsorbsTransient503 proves a 503 is classified as
// retryable — but for an UPLOAD none of that could ever fire. Every upload
// surface hands Write a plain io.Reader (the handler sniffs the first 512
// bytes for mime detection and rejoins them with io.MultiReader), so the SDK
// has nothing to rewind and the second attempt dies before it is made. The
// retry budget was real for listings and reads and a no-op for writes; a
// single transient 503 turned into a permanent upload failure, reported with
// a message that points at the plumbing instead of the outage.
func TestWrite_NonSeekableSurvivesTransient503(t *testing.T) {
	var attempts int32
	var got []byte
	d := flakyS3(t, 1, &attempts, &got)

	payload := []byte("kuyruk sonucu")
	body := nonSeekable{io.MultiReader(
		bytes.NewReader(payload[:5]),
		bytes.NewReader(payload[5:]),
	)}

	require.NoError(t, d.Write(context.Background(), "/x.txt", body, int64(len(payload))),
		"a single transient 503 must not sink the upload")
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts), "the retry must actually be attempted")
	assert.Equal(t, payload, got, "the retried attempt must resend the whole body, unchanged")
}

// TestWrite_SeekableSurvivesTransient503 guards the path that already
// retried correctly (MCP file_write, WebDAV spool) against a regression.
func TestWrite_SeekableSurvivesTransient503(t *testing.T) {
	var attempts int32
	var got []byte
	d := flakyS3(t, 1, &attempts, &got)

	require.NoError(t, d.Write(context.Background(), "/y.txt", strings.NewReader("abc"), 3))
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
	assert.Equal(t, []byte("abc"), got)
}

// TestWrite_OversizeBodyIsNotBuffered — the guard on the fix itself.
//
// ⚠ Making every body rewindable would mean holding it in memory, and filex
// streams uploads that are far larger than this process's RAM. So the buffer
// only engages for bodies that are declared small enough to fit; anything
// bigger streams straight through exactly as before. If that ever regresses,
// a large upload silently becomes an OOM.
func TestWrite_OversizeBodyIsNotBuffered(t *testing.T) {
	body, size, release, err := measuredBody(nonSeekable{strings.NewReader("x")}, maxRewindBytes+1)
	require.NoError(t, err)
	defer release()

	assert.Equal(t, int64(maxRewindBytes+1), size)
	_, buffered := body.(*rewindable)
	assert.False(t, buffered, "a body larger than the cap must stream through unbuffered")
}

// TestRewindable_ReplaysExactlyOnce covers the reader in isolation, including
// the two Seek shapes smithy actually issues: it records the start offset
// with Seek(0, SeekCurrent) when the stream is set, and rewinds with
// Seek(start, SeekStart) before a retry.
func TestRewindable_ReplaysExactlyOnce(t *testing.T) {
	rw := &rewindable{r: strings.NewReader("merhaba dunya")}

	pos, err := rw.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	require.Equal(t, int64(0), pos)

	first, err := io.ReadAll(rw)
	require.NoError(t, err)
	require.Equal(t, "merhaba dunya", string(first))

	// Mid-stream position must be reported honestly, or smithy would rewind
	// to the wrong offset and resend a truncated body.
	pos, err = rw.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	require.Equal(t, int64(len("merhaba dunya")), pos)

	_, err = rw.Seek(0, io.SeekStart)
	require.NoError(t, err)

	second, err := io.ReadAll(rw)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "the replay must be byte-identical")
}

// TestRewindable_RefusesUnsupportedSeek — the wrapper is a rewind buffer, not
// a real Seeker. Pretending otherwise would let a caller seek to an offset
// that was never buffered and read silent garbage; an explicit error makes
// the limitation visible instead.
func TestRewindable_RefusesUnsupportedSeek(t *testing.T) {
	rw := &rewindable{r: strings.NewReader("abc")}

	_, err := rw.Seek(1, io.SeekStart)
	assert.Error(t, err, "seeking to an arbitrary offset must be refused, not faked")

	_, err = rw.Seek(0, io.SeekEnd)
	assert.Error(t, err, "SeekEnd cannot be answered without draining the stream")
}

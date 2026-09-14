package s3

// Issue #27 — "Error on move files larger than 10MB": a move onto an S3
// storage died with "operation error S3: PutObject, failed to compute payload
// hash: failed to seek body to start, request stream is not seekable".
//
// It is issue #16 again, one method over. #16 taught UploadPart to accept a
// plain io.Reader; Write kept its own rule, which held a non-seekable body in
// memory only up to maxRewindBytes and streamed anything larger straight into
// PutObject. Over https:// that works (the signer sends UNSIGNED-PAYLOAD and
// never reads the body twice). Over http:// the signer hashes the payload and
// must rewind to send it — so every file past 8 MiB failed, before a byte left
// the process, on exactly the plaintext endpoint the reporter runs (Garage on a
// podman network). A cross-storage move hands Write the source driver's
// response body, which is never seekable, which is why moves hit it first.
//
// The fix streams such a body as a multipart upload in parts small enough to
// hold, so it is never held whole and never spooled whole — and each part is
// retryable on its own, which the single streaming PUT never was.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// objectStore is a plaintext fake S3 that speaks the four calls a write can
// make — PutObject, CreateMultipartUpload, UploadPart, Complete/Abort — and
// assembles what arrived, so a test can compare bytes rather than calls.
type objectStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	parts     map[int][]byte
	partSizes []int
	puts      int
	aborted   int
	signed    []string // x-amz-content-sha256 of every body-carrying request
}

func newObjectStore(t *testing.T) (*httptest.Server, *objectStore) {
	t.Helper()
	st := &objectStore{objects: map[string][]byte{}, parts: map[int][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		body, _ := io.ReadAll(r.Body)
		st.mu.Lock()
		defer st.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && q.Get("uploadId") != "":
			n, _ := strconv.Atoi(q.Get("partNumber"))
			st.parts[n] = body
			st.partSizes = append(st.partSizes, len(body))
			st.signed = append(st.signed, r.Header.Get("X-Amz-Content-Sha256"))
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, n))
		case r.Method == http.MethodPost && q.Get("uploadId") != "":
			var req struct {
				Parts []struct {
					PartNumber int
					ETag       string
				} `xml:"Part"`
			}
			if err := xml.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			nums := make([]int, 0, len(req.Parts))
			for _, p := range req.Parts {
				nums = append(nums, p.PartNumber)
			}
			sort.Ints(nums)
			var whole []byte
			for _, n := range nums {
				whole = append(whole, st.parts[n]...)
			}
			st.objects[r.URL.Path] = whole
			fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"whole"</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete && q.Get("uploadId") != "":
			st.aborted++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut:
			st.puts++
			st.signed = append(st.signed, r.Header.Get("X-Amz-Content-Sha256"))
			st.objects[r.URL.Path] = body
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

func TestWrite_LargeNonSeekableBodyOverPlaintext(t *testing.T) {
	srv, st := newObjectStore(t)
	d := partDriver(t, srv)

	// 20 MiB and not a multiple of the part size, so the last part is short.
	src := bytes.Repeat([]byte("filex-issue-27-"), (20<<20)/15+7)
	err := d.Write(context.Background(), "Mikev/Prezentacija.mp4", unseekable{bytes.NewReader(src)}, int64(len(src)))
	require.NoError(t, err, "a move onto a plaintext S3 endpoint must not depend on the source body being seekable")

	st.mu.Lock()
	defer st.mu.Unlock()
	got := st.objects["/b/Mikev/Prezentacija.mp4"]
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(src)), fmt.Sprintf("%x", sha256.Sum256(got)),
		"the assembled object must be the bytes that were written")
	assert.Zero(t, st.puts, "a body too large to hold goes out in parts, not as one PutObject")
	require.NotEmpty(t, st.partSizes)
	for _, n := range st.partSizes[:len(st.partSizes)-1] {
		assert.GreaterOrEqual(t, n, 5<<20, "every non-final part must meet S3's minimum")
		assert.LessOrEqual(t, n, maxRewindBytes, "a part is held in memory, so it must stay within the rewind cap")
	}
	for _, sha := range st.signed {
		assert.Len(t, sha, 64, "over plaintext each part is signed with its real digest")
	}
}

// A seekable body already rewinds for free: it keeps going out as one
// PutObject, whatever its size.
func TestWrite_LargeSeekableBodyStaysOnePut(t *testing.T) {
	srv, st := newObjectStore(t)
	d := partDriver(t, srv)

	src := bytes.Repeat([]byte("s"), 9<<20)
	require.NoError(t, d.Write(context.Background(), "seek.bin", bytes.NewReader(src), int64(len(src))))

	st.mu.Lock()
	defer st.mu.Unlock()
	assert.Equal(t, 1, st.puts)
	assert.Empty(t, st.partSizes)
	assert.Len(t, st.objects["/b/seek.bin"], len(src))
}

// A body that ends before its declared size must fail the write AND abort the
// multipart upload: completing it would publish a truncated object, and
// leaving it open would bill the bucket owner for orphaned parts.
func TestWrite_ShortLargeBodyAbortsTheUpload(t *testing.T) {
	srv, st := newObjectStore(t)
	d := partDriver(t, srv)

	src := bytes.Repeat([]byte("x"), 12<<20)
	err := d.Write(context.Background(), "short.bin", unseekable{bytes.NewReader(src)}, int64(len(src))+(3<<20))
	require.Error(t, err)

	st.mu.Lock()
	defer st.mu.Unlock()
	assert.Equal(t, 1, st.aborted, "the half-finished upload is aborted")
	assert.NotContains(t, st.objects, "/b/short.bin", "nothing is published under the name")
}

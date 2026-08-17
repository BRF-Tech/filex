package s3api_test

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"hash/crc32"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// The integrity contracts. Two of them are live traffic — rclone still sends
// Content-MD5, the current SDKs send x-amz-checksum-* — and the reason to
// implement either is the failure they catch: a byte altered between the client
// and here is otherwise stored, and stored corruption is found months later by
// whoever opens the file.

func b64md5(b []byte) string {
	sum := md5.Sum(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func b64crc32(b []byte) string {
	h := crc32.NewIEEE()
	h.Write(b)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func TestContentMD5IsVerified(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	body := []byte("rclone sends this on every small upload")

	// The honest case passes.
	ok := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/good.txt", body, hz.at)
	ok.Header.Set("Content-Md5", b64md5(body))
	if rec := recorderFor(hz, ok); rec.Code != http.StatusOK {
		t.Fatalf("matching Content-MD5 = %d: %s", rec.Code, rec.Body.String())
	}

	// A digest that does not describe the bytes is refused…
	bad := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/bad.txt", body, hz.at)
	bad.Header.Set("Content-Md5", b64md5([]byte("different bytes entirely")))
	rec := recorderFor(hz, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched Content-MD5 = %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "BadDigest" {
		t.Errorf("code = %q, want BadDigest — SDKs branch on this one to retry", code)
	}
	// …and the bytes it described are NOT left under that name. Keeping them
	// would publish corruption under a key somebody trusts.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "bad.txt")); err == nil {
		t.Fatal("the object survived a failed integrity check")
	}
}

func TestAmzChecksumHeaderIsVerified(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	body := []byte("the current SDKs attach a crc32 by default")

	ok := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/c.txt", body, hz.at)
	ok.Header.Set("X-Amz-Checksum-Crc32", b64crc32(body))
	if rec := recorderFor(hz, ok); rec.Code != http.StatusOK {
		t.Fatalf("matching crc32 = %d: %s", rec.Code, rec.Body.String())
	}

	bad := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/c2.txt", body, hz.at)
	bad.Header.Set("X-Amz-Checksum-Crc32", b64crc32([]byte("other")))
	rec := recorderFor(hz, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched crc32 = %d, want 400", rec.Code)
	}
	// ⚠ A different code from Content-MD5 on purpose: clients treat the two
	// differently, and answering BadDigest here sends an SDK into a retry loop
	// for a body that will never change.
	if code := errorCode(t, rec.Body.Bytes()); code != "XAmzContentChecksumMismatch" {
		t.Errorf("code = %q, want XAmzContentChecksumMismatch", code)
	}
}

// ⚠⚠ The trailer variant is what a modern SDK sends by DEFAULT: the client is
// still hashing while it streams, so the checksum can only arrive after the
// body. A server that ignores the trailer accepts whatever turns up.
func TestStreamingTrailerChecksumIsVerified(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	body := []byte(strings.Repeat("streamed payload. ", 100))

	for name, tc := range map[string]struct {
		trailer string
		want    int
	}{
		"honest":     {b64crc32(body), http.StatusOK},
		"mismatched": {b64crc32([]byte("nope")), http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			framed := unsignedChunked(body, 64, "x-amz-checksum-crc32:"+tc.trailer)
			req := signedBodyHash(t, key, http.MethodPut,
				"https://s3.filex.test/main/tr-"+name+".bin", []byte(framed),
				"STREAMING-UNSIGNED-PAYLOAD-TRAILER", hz.at)
			req.Header.Set("X-Amz-Decoded-Content-Length", strconv.Itoa(len(body)))
			req.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc32")
			if rec := recorderFor(hz, req); rec.Code != tc.want {
				t.Fatalf("%s trailer = %d, want %d: %s", name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// ⚠⚠ A PART can be aws-chunked too, and this path did not decode it: the
// length prefixes and chunk signatures were written INTO the staged part, so
// the object assembled from corrupt pieces with nothing reporting an error.
func TestUploadPartDecodesChunkedFraming(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	const url = "https://s3.filex.test/main/parted.bin"
	uploadID := initiate(t, hz, key, url)
	part := []byte(strings.Repeat("A part that arrives framed. ", 400))
	framed := unsignedChunked(part, 128, "")

	req := signedBodyHash(t, key, http.MethodPut,
		url+"?partNumber=1&uploadId="+uploadID,
		[]byte(framed), "STREAMING-UNSIGNED-PAYLOAD-TRAILER", hz.at)
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.Itoa(len(part)))
	rec := recorderFor(hz, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("UploadPart = %d: %s", rec.Code, rec.Body.String())
	}
	// The part's ETag is its MD5, and the client compares it — a framed part
	// hashes to something else entirely.
	sum := md5.Sum(part)
	want := `"` + hex.EncodeToString(sum[:]) + `"`
	if got := rec.Header().Get("ETag"); got != want {
		t.Fatalf("part ETag = %s, want %s — the framing was staged as part of the data", got, want)
	}

	if c := complete(t, hz, key, url, uploadID, map[int]string{1: want}); c.Code != http.StatusOK {
		t.Fatalf("complete = %d: %s", c.Code, c.Body.String())
	}

	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "parted.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(onDisk) != string(part) {
		t.Fatalf("assembled object is %d bytes, want %d — framing leaked into the file", len(onDisk), len(part))
	}
}

// unsignedChunked frames body the way a client using the unsigned-trailer
// variant does, with an optional trailer line.
func unsignedChunked(body []byte, chunkSize int, trailer string) string {
	var b strings.Builder
	for off := 0; off < len(body); off += chunkSize {
		end := off + chunkSize
		if end > len(body) {
			end = len(body)
		}
		b.WriteString(strconv.FormatInt(int64(end-off), 16) + "\r\n")
		b.Write(body[off:end])
		b.WriteString("\r\n")
	}
	b.WriteString("0\r\n")
	if trailer != "" {
		b.WriteString(trailer + "\r\n")
	}
	b.WriteString("\r\n")
	return b.String()
}

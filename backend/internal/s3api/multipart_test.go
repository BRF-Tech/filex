package s3api_test

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Multipart is how every client sends anything large, so these walk the whole
// sequence a real one performs: create, upload parts, list them, complete, and
// read the object back.

func initiate(t *testing.T, hz *harness, key *protocolauth.IssuedKey, url string) string {
	t.Helper()
	rec := recorderFor(hz, signedBody(t, key, http.MethodPost, url+"?uploads", nil, hz.at))
	if rec.Code != http.StatusOK {
		t.Fatalf("create multipart = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.UploadID == "" {
		t.Fatal("no upload id")
	}
	return out.UploadID
}

func uploadPart(t *testing.T, hz *harness, key *protocolauth.IssuedKey, url, uploadID string, n int, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	u := fmt.Sprintf("%s?partNumber=%d&uploadId=%s", url, n, uploadID)
	return recorderFor(hz, signedBody(t, key, http.MethodPut, u, body, hz.at))
}

func complete(t *testing.T, hz *harness, key *protocolauth.IssuedKey, url, uploadID string, parts map[int]string) *httptest.ResponseRecorder {
	t.Helper()
	var b strings.Builder
	b.WriteString("<CompleteMultipartUpload>")
	for n := 1; n <= len(parts); n++ {
		fmt.Fprintf(&b, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", n, parts[n])
	}
	b.WriteString("</CompleteMultipartUpload>")
	u := url + "?uploadId=" + uploadID
	return recorderFor(hz, signedBody(t, key, http.MethodPost, u, []byte(b.String()), hz.at))
}

func TestMultipartRoundTrip(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	const url = "https://s3.filex.test/main/big.bin"

	uploadID := initiate(t, hz, key, url)

	// Two parts: the first at the 5 MiB minimum, the last smaller — the shape
	// every real client produces.
	first := bytes.Repeat([]byte("a"), 5<<20)
	last := bytes.Repeat([]byte("b"), 1024)
	etags := map[int]string{}
	for n, body := range map[int][]byte{1: first, 2: last} {
		rec := uploadPart(t, hz, key, url, uploadID, n, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("part %d = %d: %s", n, rec.Code, rec.Body.String())
		}
		sum := md5.Sum(body)
		if want := `"` + hex.EncodeToString(sum[:]) + `"`; rec.Header().Get("ETag") != want {
			t.Fatalf("part %d ETag = %q, want %q", n, rec.Header().Get("ETag"), want)
		}
		etags[n] = rec.Header().Get("ETag")
	}

	// ListParts is how a client resumes; it must see what was staged.
	lp := recorderFor(hz, signed(t, key, http.MethodGet, url+"?uploadId="+uploadID, hz.at))
	if lp.Code != http.StatusOK {
		t.Fatalf("list parts = %d", lp.Code)
	}
	var parts struct {
		Parts []struct {
			PartNumber int   `xml:"PartNumber"`
			Size       int64 `xml:"Size"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(lp.Body.Bytes(), &parts); err != nil {
		t.Fatalf("unmarshal parts: %v", err)
	}
	if len(parts.Parts) != 2 || parts.Parts[0].Size != int64(len(first)) {
		t.Fatalf("listed parts = %+v", parts.Parts)
	}

	rec := complete(t, hz, key, url, uploadID, etags)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete = %d: %s", rec.Code, rec.Body.String())
	}

	// ⚠ The completed ETag is the COMPOSITE (md5 of the concatenated part
	// md5s, then "-N"), not the md5 of the object. That is what S3 returns and
	// what clients store, so reporting the whole-object digest would make
	// every later comparison fail.
	var done struct {
		ETag string `xml:"ETag"`
	}
	_ = xml.Unmarshal(rec.Body.Bytes(), &done)
	if !strings.HasSuffix(strings.Trim(done.ETag, `"`), "-2") {
		t.Errorf("completed ETag = %q, want a composite ending in -2", done.ETag)
	}

	// The object is on the driver, whole and in order.
	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "big.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := append(append([]byte(nil), first...), last...)
	if !bytes.Equal(onDisk, want) {
		t.Fatalf("assembled object is %d bytes, want %d", len(onDisk), len(want))
	}
	// And readable through the endpoint.
	got := hz.do(t, key, http.MethodGet, url)
	if got.Code != http.StatusOK || got.Body.Len() != len(want) {
		t.Fatalf("GET after complete = %d / %d bytes", got.Code, got.Body.Len())
	}
	// The staging directory is gone: a completed upload must not leave debris.
	if hz.stagingDirs(t) != 0 {
		t.Errorf("staging still holds %d uploads after completion", hz.stagingDirs(t))
	}
}

// ⚠ This is where S3 puts the integrity check for a multipart upload. Skipping
// it would let a truncated part through: the upload would "succeed" and the
// object would be quietly wrong.
func TestCompleteRefusesAPartThatDoesNotMatch(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	const url = "https://s3.filex.test/main/x.bin"

	uploadID := initiate(t, hz, key, url)
	uploadPart(t, hz, key, url, uploadID, 1, bytes.Repeat([]byte("a"), 1024))

	rec := complete(t, hz, key, url, uploadID, map[int]string{1: `"00000000000000000000000000000000"`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("complete with a wrong part ETag = %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "InvalidPart" {
		t.Errorf("code = %q, want InvalidPart", code)
	}
}

func TestCompleteRefusesAnUnknownPart(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	const url = "https://s3.filex.test/main/x.bin"

	uploadID := initiate(t, hz, key, url)
	uploadPart(t, hz, key, url, uploadID, 1, []byte("hello"))

	rec := complete(t, hz, key, url, uploadID, map[int]string{1: "", 2: ""})
	if rec.Code != http.StatusBadRequest || errorCode(t, rec.Body.Bytes()) != "InvalidPart" {
		t.Fatalf("complete listing a part that was never uploaded = %d / %s",
			rec.Code, errorCode(t, rec.Body.Bytes()))
	}
}

// Every part but the last must reach 5 MiB — S3's rule, checked at completion
// rather than at upload because that is where S3 checks it.
func TestCompleteRefusesAnUndersizedMiddlePart(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	const url = "https://s3.filex.test/main/x.bin"

	uploadID := initiate(t, hz, key, url)
	etags := map[int]string{}
	for n, body := range map[int][]byte{1: []byte("tiny"), 2: []byte("also tiny")} {
		rec := uploadPart(t, hz, key, url, uploadID, n, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("part %d = %d", n, rec.Code)
		}
		etags[n] = rec.Header().Get("ETag")
	}

	rec := complete(t, hz, key, url, uploadID, etags)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("complete with an undersized middle part = %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "EntityTooSmall" {
		t.Errorf("code = %q, want EntityTooSmall", code)
	}
}

// Aborting frees the staged bytes. ⚠ And aborting an upload that is already
// gone is a SUCCESS: clients abort in a defer, so a 404 would turn cleanup
// into an error path.
func TestAbortDiscardsThePartsAndIsIdempotent(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	const url = "https://s3.filex.test/main/x.bin"

	uploadID := initiate(t, hz, key, url)
	uploadPart(t, hz, key, url, uploadID, 1, bytes.Repeat([]byte("a"), 4096))
	if hz.stagingDirs(t) == 0 {
		t.Fatal("nothing staged after a part upload")
	}

	rec := recorderFor(hz, signed(t, key, http.MethodDelete, url+"?uploadId="+uploadID, hz.at))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("abort = %d, want 204", rec.Code)
	}
	if n := hz.stagingDirs(t); n != 0 {
		t.Errorf("staging still holds %d uploads after abort", n)
	}
	// Again, on an upload that no longer exists.
	rec = recorderFor(hz, signed(t, key, http.MethodDelete, url+"?uploadId="+uploadID, hz.at))
	if rec.Code != http.StatusNoContent {
		t.Errorf("abort of a gone upload = %d, want 204", rec.Code)
	}
}

// A part uploaded against someone else's confinement must be refused like any
// other write.
func TestMultipartRespectsTheConfinement(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "mine"})

	rec := recorderFor(hz, signedBody(t, key, http.MethodPost,
		"https://s3.filex.test/main/elsewhere/x.bin?uploads", nil, hz.at))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create outside the confinement = %d, want 403", rec.Code)
	}
}

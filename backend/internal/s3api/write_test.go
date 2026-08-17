package s3api_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// Writing and deleting. The bytes are the easy half — what these pin is the
// bookkeeping around them, because a write that skips it leaves a file that
// looks fine and is invisible to the index, the thumbnails and the quota.

// signedBody builds a signed request that carries a body.
func signedBody(t *testing.T, key *protocolauth.IssuedKey, method, url string, body []byte, at time.Time) *http.Request {
	t.Helper()
	return signedBodyHash(t, key, method, url, body, sha256Hex(body), at)
}

// signedBodyHash signs with an EXPLICIT payload hash, so a test can send the
// STREAMING-* sentinels a real client uses.
//
// ⚠ It has to be signed as the payload hash rather than set afterwards:
// X-Amz-Content-Sha256 is a signed header, so patching it post-signature just
// produces a 403 and the test would pass for the wrong reason.
func signedBodyHash(t *testing.T, key *protocolauth.IssuedKey, method, url string, body []byte, hash string, at time.Time) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("X-Amz-Content-Sha256", hash)
	s := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: key.Key.AccessKeyID, SecretAccessKey: key.Secret}
	if err := s.SignHTTP(context.Background(), creds, req, hash, "s3", "us-east-1", at); err != nil {
		t.Fatalf("sign: %v", err)
	}

	srv := httptest.NewRequest(method, req.URL.String(), bytes.NewReader(body))
	srv.Host = req.URL.Host
	srv.ContentLength = int64(len(body))
	for k, vs := range req.Header {
		for _, v := range vs {
			srv.Header.Add(k, v)
		}
	}
	return srv
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (hz *harness) put(t *testing.T, key *protocolauth.IssuedKey, url string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	return recorderFor(hz, signedBody(t, key, http.MethodPut, url, body, hz.at))
}

func TestPutObjectStoresTheBytesAndTheBookkeeping(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	body := []byte("uploaded through the S3 endpoint")
	rec := hz.put(t, key, "https://s3.filex.test/main/docs/new.txt", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}

	// The bytes are really on the driver.
	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "docs", "new.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("on disk = %q, want %q", onDisk, body)
	}

	// The ETag is the real MD5, computed as the bytes went past.
	sum := md5.Sum(body)
	if want := `"` + hex.EncodeToString(sum[:]) + `"`; rec.Header().Get("ETag") != want {
		t.Errorf("ETag = %q, want %q", rec.Header().Get("ETag"), want)
	}

	// ⚠ And the node row exists, with its parent chain. Without it the file is
	// on disk and invisible everywhere else in the product until the next sync
	// run — the exact failure the shared syncer exists to prevent.
	ctx := context.Background()
	node, err := hz.store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/docs/new.txt"))
	if err != nil || node == nil {
		t.Fatalf("no node row for the written object (%v)", err)
	}
	if node.Size != int64(len(body)) {
		t.Errorf("node size = %d, want %d", node.Size, len(body))
	}
	parent, err := hz.store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/docs"))
	if err != nil || parent == nil {
		t.Fatal("no parent directory row — the chain was not created")
	}
}

// The object must be readable through the same endpoint immediately, which is
// the round trip an rclone copy actually performs.
func TestPutThenGetRoundTrip(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	body := []byte("round trip payload")
	if rec := hz.put(t, key, "https://s3.filex.test/main/rt.bin", body); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d", rec.Code)
	}
	got := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/rt.bin")
	if got.Code != http.StatusOK || got.Body.String() != string(body) {
		t.Fatalf("GET = %d / %q", got.Code, got.Body.String())
	}
	// The ETag the read path reports must be the one the write path returned,
	// without re-reading the object to learn it.
	put := hz.put(t, key, "https://s3.filex.test/main/rt2.bin", body)
	head := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/rt2.bin")
	if put.Header().Get("ETag") != head.Header().Get("ETag") {
		t.Errorf("ETag PUT=%q HEAD=%q", put.Header().Get("ETag"), head.Header().Get("ETag"))
	}
}

// ⚠ A write refusal says AccessDenied, unlike a read. A client that cannot
// write must be told so; NoSuchKey would make it retry forever.
func TestPutOutsideTheConfinementIsAccessDenied(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "mine"})

	if rec := hz.put(t, key, "https://s3.filex.test/main/mine/ok.txt", []byte("x")); rec.Code != http.StatusOK {
		t.Fatalf("inside the confinement = %d, want 200", rec.Code)
	}
	rec := hz.put(t, key, "https://s3.filex.test/main/elsewhere/no.txt", []byte("x"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside the confinement = %d, want 403", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "AccessDenied" {
		t.Errorf("code = %q, want AccessDenied", code)
	}
}

// A viewer-level grant reads and does not write. The two gates are different
// levels, and using one for both is how a read-only credential gets to write.
func TestViewerGrantCannotWrite(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "shared/readme.txt", []byte("hello"))

	st.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("rbac: %v", err)
	}
	if _, err := hz.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: st.ID, PathPrefix: "shared", IsDir: true, UserID: u.ID, Level: "viewer",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	if rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/shared/readme.txt"); rec.Code != http.StatusOK {
		t.Fatalf("viewer read = %d, want 200", rec.Code)
	}
	if rec := hz.put(t, key, "https://s3.filex.test/main/shared/new.txt", []byte("x")); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d, want 403", rec.Code)
	}
}

// Deleting moves the bytes to the trash, on this protocol like on every other
// one. A permanent delete here would be a data-loss difference between
// surfaces.
func TestDeleteObjectGoesToTheTrash(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	body := []byte("delete me")
	if rec := hz.put(t, key, "https://s3.filex.test/main/gone.txt", body); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d", rec.Code)
	}

	req := signed(t, key, http.MethodDelete, "https://s3.filex.test/main/gone.txt", hz.at)
	rec := recorderFor(hz, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", rec.Code, rec.Body.String())
	}

	// Gone from its own key…
	if got := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/gone.txt"); got.Code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", got.Code)
	}
	// …but the bytes are still there, under the trash prefix.
	root := hz.rootOf(t, st)
	found := false
	_ = filepath.Walk(filepath.Join(root, ".filex-trash"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && bytes.Equal(b, body) {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("the bytes are not in the trash — this was a permanent delete")
	}
	if !trash.IsTrashPath(".filex-trash/x") {
		t.Error("trash path helper disagrees with the layout this test assumes")
	}
}

// ⚠ Deleting something that is not there is a SUCCESS in S3. Clients delete
// optimistically; a 404 turns a no-op into a retry loop.
func TestDeleteMissingObjectSucceeds(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := recorderFor(hz, signed(t, key, http.MethodDelete, "https://s3.filex.test/main/never-existed.txt", hz.at))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE of a missing key = %d, want 204", rec.Code)
	}
}

func TestReadOnlyBucketRefusesWrites(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	st.ReadOnly = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("read-only: %v", err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	if rec := hz.put(t, key, "https://s3.filex.test/main/x.txt", []byte("x")); rec.Code != http.StatusForbidden {
		t.Errorf("PUT to a read-only bucket = %d, want 403", rec.Code)
	}
	rec := recorderFor(hz, signed(t, key, http.MethodDelete, "https://s3.filex.test/main/x.txt", hz.at))
	if rec.Code != http.StatusForbidden {
		t.Errorf("DELETE on a read-only bucket = %d, want 403", rec.Code)
	}
}

// ⚠⚠ A key ending in "/" is a DIRECTORY MARKER: the only way S3 has of saying
// "make a folder", and what every client presenting folders sends. This test
// used to assert a 400 — which is what the endpoint did, and it meant `mkdir`
// simply did not work: s3fs answered "Input/output error" and no folder could
// be created at all (2026-08-16, found by mounting it).
//
// What still holds is the reason behind the old refusal: no FILE is created
// with a separator in its name. The marker maps onto the thing filex actually
// has — a directory — so every other surface sees a normal folder.
func TestPutOnAFolderShapedKeyCreatesAFolder(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.put(t, key, "https://s3.filex.test/main/folder/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT on a trailing-slash key = %d: %s", rec.Code, rec.Body.String())
	}
	info, err := os.Stat(filepath.Join(hz.rootOf(t, st), "folder"))
	if err != nil {
		t.Fatalf("no folder was created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("a FILE was created for the marker — every other surface shows that as a broken entry")
	}
	// Clients HEAD the marker right afterwards to confirm; a 404 there reads as
	// a failed mkdir.
	head := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/folder/")
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD of the marker = %d, want 200", head.Code)
	}
	if got := head.Header().Get("Content-Length"); got != "0" {
		t.Errorf("marker Content-Length = %q, want 0", got)
	}
	if got := head.Header().Get("Content-Type"); got != "application/x-directory" {
		t.Errorf("marker Content-Type = %q — this is how a client tells a folder from a file", got)
	}
	// A marker that does not exist is still absent.
	if miss := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/nothere/"); miss.Code != http.StatusNotFound {
		t.Errorf("HEAD of a missing folder = %d, want 404", miss.Code)
	}
	// ⚠ And a marker carrying a body is refused rather than silently dropping
	// the bytes.
	if bad := hz.put(t, key, "https://s3.filex.test/main/withbody/", []byte("payload")); bad.Code != http.StatusBadRequest {
		t.Errorf("PUT of a marker with a body = %d, want 400", bad.Code)
	}
	// Removing the folder is a DELETE of the same marker.
	if del := hz.do(t, key, http.MethodDelete, "https://s3.filex.test/main/folder/"); del.Code != http.StatusNoContent {
		t.Fatalf("DELETE of the marker = %d", del.Code)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "folder")); err == nil {
		t.Error("the folder survived a DELETE of its marker")
	}
}

// The per-user ceiling is checked BEFORE the bytes land: checking afterwards
// means the disk already holds what the quota was meant to prevent.
func TestQuotaIsEnforcedBeforeTheBytesLand(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	if err := quota.New(hz.store).SetQuota(context.Background(), u.ID, 64); err != nil {
		t.Fatalf("set quota: %v", err)
	}

	small := bytes.Repeat([]byte("a"), 32)
	if rec := hz.put(t, key, "https://s3.filex.test/main/small.bin", small); rec.Code != http.StatusOK {
		t.Fatalf("within quota = %d, want 200", rec.Code)
	}
	big := bytes.Repeat([]byte("b"), 4096)
	rec := hz.put(t, key, "https://s3.filex.test/main/big.bin", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over quota = %d, want 413", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "EntityTooLarge" {
		t.Errorf("code = %q, want EntityTooLarge", code)
	}
	// ⚠ And the bytes must NOT be on disk: a 413 after the write would mean
	// the quota reported a refusal it did not perform.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "big.bin")); err == nil {
		t.Fatal("the over-quota object was written anyway")
	}
}

var _ = io.Discard

// Bulk delete is what rclone, restic and mc actually use, and it is the one
// place in the protocol where PARTIAL failure is normal: nine keys succeed,
// one is refused, and the client needs to know which.
func TestBulkDeleteReportsPerKeyResults(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "mine/a.txt", []byte("a"))
	hz.writeFile(t, st, "mine/b.txt", []byte("b"))
	hz.writeFile(t, st, "theirs/c.txt", []byte("c"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "mine"})

	body := []byte(`<Delete>` +
		`<Object><Key>mine/a.txt</Key></Object>` +
		`<Object><Key>mine/b.txt</Key></Object>` +
		`<Object><Key>theirs/c.txt</Key></Object>` +
		`<Object><Key>mine/never-existed.txt</Key></Object>` +
		`</Delete>`)
	req := signedBody(t, key, http.MethodPost, "https://s3.filex.test/main?delete", body, hz.at)
	rec := recorderFor(hz, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk delete = %d: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		Deleted []struct {
			Key string `xml:"Key"`
		} `xml:"Deleted"`
		Errors []struct {
			Key  string `xml:"Key"`
			Code string `xml:"Code"`
		} `xml:"Error"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}

	deleted := map[string]bool{}
	for _, d := range out.Deleted {
		deleted[d.Key] = true
	}
	// The two real keys and the missing one all count as deleted.
	for _, want := range []string{"mine/a.txt", "mine/b.txt", "mine/never-existed.txt"} {
		if !deleted[want] {
			t.Errorf("%s missing from Deleted: %s", want, rec.Body.String())
		}
	}
	// The one outside the confinement is refused, BY NAME, rather than
	// silently dropped or failing the whole batch.
	if len(out.Errors) != 1 || out.Errors[0].Key != "theirs/c.txt" || out.Errors[0].Code != "AccessDenied" {
		t.Fatalf("errors = %+v, want one AccessDenied for theirs/c.txt", out.Errors)
	}
	// And its bytes are still there.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "theirs", "c.txt")); err != nil {
		t.Error("a refused key was deleted anyway")
	}
}

// Quiet mode is what the bulk clients ask for; a thousand success elements per
// batch is XML nobody reads.
func TestBulkDeleteQuietOmitsSuccesses(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("a"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	body := []byte(`<Delete><Quiet>true</Quiet><Object><Key>a.txt</Key></Object></Delete>`)
	rec := recorderFor(hz, signedBody(t, key, http.MethodPost, "https://s3.filex.test/main?delete", body, hz.at))
	if rec.Code != http.StatusOK {
		t.Fatalf("quiet bulk delete = %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("<Deleted>")) {
		t.Errorf("quiet mode still listed successes: %s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "a.txt")); err == nil {
		t.Error("the key was not deleted")
	}
}

// The handler side of aws-chunked. The DECODER itself is unit-tested in
// chunked_test.go against a properly signed chain; what matters here is that
// the endpoint refuses what it cannot decode rather than storing the framing.
func TestUnsupportedStreamingVariantIsRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	framed := []byte("a;chunk-signature=0000\r\nHELLOWORLD\r\n0;chunk-signature=0000\r\n\r\n")
	req := signedBodyHash(t, key, http.MethodPut, "https://s3.filex.test/main/chunked.bin",
		framed, "STREAMING-SOMETHING-WE-DO-NOT-KNOW", hz.at)
	req.Header.Set("X-Amz-Decoded-Content-Length", "10")
	rec := recorderFor(hz, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unknown streaming variant = %d, want 501", rec.Code)
	}
	// ⚠ And nothing was written. Storing the framed bytes while reporting a
	// refusal would be the worst of both.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "chunked.bin")); err == nil {
		t.Fatal("the framed body was stored anyway")
	}
}

// ⚠ A chunked request declares TWO lengths: Content-Length for the framed body
// and x-amz-decoded-content-length for the object. Without the second one the
// object's size is unknowable, and guessing it records the framing overhead as
// part of the file.
func TestChunkedWithoutDecodedLengthIsRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	framed := []byte("0;chunk-signature=0000\r\n\r\n")
	req := signedBodyHash(t, key, http.MethodPut, "https://s3.filex.test/main/nolen.bin",
		framed, "STREAMING-AWS4-HMAC-SHA256-PAYLOAD", hz.at)
	rec := recorderFor(hz, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chunked without a decoded length = %d, want 400", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "nolen.bin")); err == nil {
		t.Fatal("something was written anyway")
	}
}

// The wiring, end to end: a framed body must land as the OBJECT.
//
// The unsigned-trailer variant is used here on purpose — it needs no chunk
// signature chain, so the test can build a body freely and still exercise
// everything that matters at this layer: the framing is stripped, the
// DECODED length is what the driver is told, and the file contains the
// payload rather than the wrapper. The signed chain has its own tests in
// chunked_test.go, where the primitives are reachable.
func TestChunkedBodyLandsAsTheObject(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	payload := []byte("HELLOWORLD-and-then-some-more-bytes")
	framed := []byte(
		fmt.Sprintf("%x", len(payload)) + "\r\n" + string(payload) + "\r\n" +
			"0" + "\r\n" + "\r\n")

	req := signedBodyHash(t, key, http.MethodPut, "https://s3.filex.test/main/streamed.bin",
		framed, "STREAMING-UNSIGNED-PAYLOAD-TRAILER", hz.at)
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.Itoa(len(payload)))
	rec := recorderFor(hz, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chunked PUT = %d: %s", rec.Code, rec.Body.String())
	}

	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "streamed.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("stored %q, want %q", onDisk, payload)
	}
	// ⚠ The failure this guards is silent: the framing inside the file.
	if bytes.Contains(onDisk, []byte("chunk")) || len(onDisk) != len(payload) {
		t.Fatalf("the framing leaked into the object: %q", onDisk)
	}

	// And the ETag is the md5 of the OBJECT, not of the framed body — a client
	// verifying its upload compares against the former.
	sum := md5.Sum(payload)
	if want := `"` + hex.EncodeToString(sum[:]) + `"`; rec.Header().Get("ETag") != want {
		t.Errorf("ETag = %q, want the object md5 %q", rec.Header().Get("ETag"), want)
	}

	// The node row records the decoded size, not the framed one.
	node, err := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/streamed.bin"))
	if err != nil || node == nil {
		t.Fatalf("no node row: %v", err)
	}
	if node.Size != int64(len(payload)) {
		t.Errorf("node size = %d, want %d (the framed body is %d)", node.Size, len(payload), len(framed))
	}
}

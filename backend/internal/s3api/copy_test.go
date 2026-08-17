package s3api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// CopyObject is how a client renames, moves or duplicates. What these pin is
// that BOTH ends are gated with their own rules — a copy is a read of one
// object and a write of another.

func (hz *harness) copy(t *testing.T, key *protocolauth.IssuedKey, dstURL, source string) *httptest.ResponseRecorder {
	t.Helper()
	req := signedBody(t, key, http.MethodPut, dstURL, nil, hz.at)
	req.Header.Set("X-Amz-Copy-Source", source)
	return recorderFor(hz, req)
}

func TestCopyObject(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	body := []byte("the original bytes")
	hz.writeFile(t, st, "src/original.txt", body)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.copy(t, key, "https://s3.filex.test/main/dst/copy.txt", "/main/src/original.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("copy = %d: %s", rec.Code, rec.Body.String())
	}

	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "dst", "copy.txt"))
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("copy = %q, want %q", onDisk, body)
	}
	// The source survives — this is a copy, not a move.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "src", "original.txt")); err != nil {
		t.Error("the source was removed by a COPY")
	}
	// And the destination is bookkept like any other write.
	node, err := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/dst/copy.txt"))
	if err != nil || node == nil {
		t.Fatalf("no node row for the copy: %v", err)
	}
	// Readable through the endpoint straight away.
	got := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/dst/copy.txt")
	if got.Code != http.StatusOK || got.Body.String() != string(body) {
		t.Fatalf("GET after copy = %d / %q", got.Code, got.Body.String())
	}
}

// ⚠ Checking only the destination would turn CopyObject into a way to READ
// anything the caller can write over: name a source you cannot see, copy it
// somewhere you can, then download it.
func TestCopyChecksTheSourceNotOnlyTheDestination(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "secrets/theirs.txt", []byte("not yours"))
	hz.writeFile(t, st, "mine/ok.txt", []byte("yours"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "mine"})

	rec := hz.copy(t, key, "https://s3.filex.test/main/mine/stolen.txt", "/main/secrets/theirs.txt")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("copy from outside the confinement = %d, want 404", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "NoSuchKey" {
		t.Errorf("code = %q, want NoSuchKey", code)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "mine", "stolen.txt")); err == nil {
		t.Fatal("the object was copied anyway")
	}

	// Within the confinement it works.
	if rec := hz.copy(t, key, "https://s3.filex.test/main/mine/copy.txt", "/main/mine/ok.txt"); rec.Code != http.StatusOK {
		t.Fatalf("copy inside the confinement = %d, want 200", rec.Code)
	}
}

func TestCopyToAnUnwritableDestinationIsAccessDenied(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "mine/ok.txt", []byte("yours"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "mine"})

	rec := hz.copy(t, key, "https://s3.filex.test/main/elsewhere/x.txt", "/main/mine/ok.txt")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("copy to an unwritable key = %d, want 403", rec.Code)
	}
}

// ⚠ The header is percent-encoded. Copying to the literal escaped name would
// create a second file whose name nobody can type.
func TestCopySourceIsURLDecoded(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "gölge dosya.txt", []byte("türkçe"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.copy(t, key, "https://s3.filex.test/main/copy.txt", "/main/g%C3%B6lge%20dosya.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("copy with an encoded source = %d: %s", rec.Code, rec.Body.String())
	}
	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "copy.txt"))
	if err != nil || string(onDisk) != "türkçe" {
		t.Fatalf("copied content = %q (%v)", onDisk, err)
	}
}

func TestCopyRejectsBadSources(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("a"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	// ⚠ An EMPTY x-amz-copy-source is not a bad source, it is no source: the
	// request is an ordinary PUT and must be treated as one. The first draft of
	// this test listed it as invalid and failed for that reason.
	for name, source := range map[string]string{
		"no key":         "/main",
		"versioned":      "/main/a.txt?versionId=3",
		"unknown bucket": "/nope/a.txt",
		"not a path":     "main",
	} {
		t.Run(name, func(t *testing.T) {
			rec := hz.copy(t, key, "https://s3.filex.test/main/out.txt", source)
			if rec.Code == http.StatusOK {
				t.Fatalf("bad source %q was accepted", source)
			}
		})
	}

	// Copying an object onto itself does nothing, so it is refused rather than
	// reported as a success that changed something.
	if rec := hz.copy(t, key, "https://s3.filex.test/main/a.txt", "/main/a.txt"); rec.Code != http.StatusBadRequest {
		t.Errorf("self-copy = %d, want 400", rec.Code)
	}
}

// UploadPartCopy needs a ranged read of the source into a staged part. It is
// refused rather than half-done, because a plausible wrong answer there
// corrupts a multipart assembly.
func TestUploadPartCopyIsRefusedHonestly(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("a"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	req := signedBody(t, key, http.MethodPut,
		"https://s3.filex.test/main/big.bin?partNumber=1&uploadId=whatever", nil, hz.at)
	req.Header.Set("X-Amz-Copy-Source", "/main/a.txt")
	rec := recorderFor(hz, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("UploadPartCopy = %d, want 501", rec.Code)
	}
}

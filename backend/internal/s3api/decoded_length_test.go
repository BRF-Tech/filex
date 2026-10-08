package s3api_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
)

// A chunked body is held to the decoded length it declares
// (x-amz-decoded-content-length). The quota and the per-file limit are asked
// with that number before a byte is read, so the bytes that land have to be
// exactly that many: a body that goes on past it, or stops short of it, is
// refused with S3's IncompleteBody and nothing is kept.

func TestChunkedBodyLongerThanDeclaredIsRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})
	if err := quota.New(hz.store).SetQuota(context.Background(), u.ID, 64); err != nil {
		t.Fatalf("set quota: %v", err)
	}

	payload := bytes.Repeat([]byte("x"), 4096)
	req := signedBodyHash(t, key, http.MethodPut, "https://s3.filex.test/main/declared-one.bin",
		[]byte(unsignedChunked(payload, 512, "")), "STREAMING-UNSIGNED-PAYLOAD-TRAILER", hz.at)
	req.Header.Set("X-Amz-Decoded-Content-Length", "1")
	rec := recorderFor(hz, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a body longer than it declared = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "IncompleteBody" {
		t.Errorf("code = %q, want IncompleteBody", code)
	}
	if fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "declared-one.bin")); err == nil {
		t.Fatalf("%d bytes were kept past a 64-byte quota", fi.Size())
	}
	if n, _ := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/declared-one.bin")); n != nil {
		t.Fatalf("a catalogue row was written: size %d", n.Size)
	}
}

func TestChunkedBodyShorterThanDeclaredIsRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	payload := bytes.Repeat([]byte("y"), 1000)
	req := signedBodyHash(t, key, http.MethodPut, "https://s3.filex.test/main/short.bin",
		[]byte(unsignedChunked(payload, 256, "")), "STREAMING-UNSIGNED-PAYLOAD-TRAILER", hz.at)
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.Itoa(len(payload)+500))
	rec := recorderFor(hz, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a body shorter than it declared = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "IncompleteBody" {
		t.Errorf("code = %q, want IncompleteBody", code)
	}
	if n, _ := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/short.bin")); n != nil {
		t.Fatalf("a catalogue row was written for a short body: size %d", n.Size)
	}
}

// A part goes through the same body door, and is held to the same rule.
func TestChunkedPartLongerThanDeclaredIsRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	const url = "https://s3.filex.test/main/parts.bin"
	uploadID := initiate(t, hz, key, url)
	part := bytes.Repeat([]byte("p"), 2048)
	req := signedBodyHash(t, key, http.MethodPut, url+"?partNumber=1&uploadId="+uploadID,
		[]byte(unsignedChunked(part, 256, "")), "STREAMING-UNSIGNED-PAYLOAD-TRAILER", hz.at)
	req.Header.Set("X-Amz-Decoded-Content-Length", "16")
	rec := recorderFor(hz, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a part longer than it declared = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "IncompleteBody" {
		t.Errorf("code = %q, want IncompleteBody", code)
	}
}

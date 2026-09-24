package handlers_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/ops"
)

func craftedZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		entry, err := zw.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func waitForArchiveOp(t *testing.T, svc *ops.Service, id int64) *ops.Op {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := svc.Get(context.Background(), id)
		require.NoError(t, err)
		switch op.Status {
		case ops.StatusOK, ops.StatusFailed, ops.StatusPartial, ops.StatusCancelled:
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive operation %d did not finish (status %s)", id, op.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestArchiveCreateAndFirstClassExtract(t *testing.T) {
	srv, client, store, tok := aiFixture(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{
		"main://source/one.txt": "ONE",
		"main://source/two.txt": "TWO",
	})

	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/create", tok, map[string]any{
		"sources": []string{"main://source/one.txt"},
		"dest":    "main://archives/bundle.rar",
		"format":  "rar",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var unsupported map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&unsupported))
	resp.Body.Close()
	assert.Equal(t, "UNSUPPORTED_FORMAT", unsupported["code"])

	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/create", tok, map[string]any{
		"sources":  []string{"main://source/one.txt"},
		"dest":     "main://archives/bundle.tar.gz",
		"format":   "tar.gz",
		"password": "not-supported",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var invalidOptions map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&invalidOptions))
	resp.Body.Close()
	assert.Equal(t, "INVALID_ARCHIVE_OPTIONS", invalidOptions["code"])

	// Disabling external command providers must not remove the safe built-in
	// ZIP path that existing installations already rely on.
	require.NoError(t, store.UpsertSetting(context.Background(), archivecli.SettingEnabled, "false"))

	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/create", tok, map[string]any{
		"sources": []string{"main://source/one.txt", "main://source/two.txt"},
		"dest":    "main://archives/bundle.zip",
		"format":  "zip",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	code, raw := aiDownload(t, client, srv.URL, tok, "main://archives/bundle.zip")
	require.Equal(t, http.StatusOK, code)
	got := openZip(t, raw)
	assert.Equal(t, "ONE", got["one.txt"])
	assert.Equal(t, "TWO", got["two.txt"])

	// Archive creation is not an overwrite operation. A retry or a second
	// selection with the same suggested name must fail before replacing the
	// existing archive, and must give the explorer a stable error to display.
	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/create", tok, map[string]any{
		"sources": []string{"main://source/one.txt"},
		"dest":    "main://archives/bundle.zip",
		"format":  "zip",
	})
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	var conflict map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&conflict))
	resp.Body.Close()
	assert.Equal(t, "TARGET_EXISTS", conflict["code"])

	code, afterConflict := aiDownload(t, client, srv.URL, tok, "main://archives/bundle.zip")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, raw, afterConflict, "a conflicting create must preserve the existing archive")

	// Omitting dest is the API form of the explorer's "Extract here": members
	// land beside the archive, with no automatically-created parent folder.
	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/extract", tok, map[string]any{
		"path": "main://archives/bundle.zip",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	code, extractedHere := aiDownload(t, client, srv.URL, tok, "main://archives/one.txt")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ONE", string(extractedHere))

	// The explorer sends an adapter-qualified destination. The handler must
	// resolve it back onto the archive's storage rather than writing a literal
	// `main:` directory into that storage.
	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/extract", tok, map[string]any{
		"path": "main://archives/bundle.zip",
		"dest": "main://restored/bundle",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	code, body := aiDownload(t, client, srv.URL, tok, "main://restored/bundle/one.txt")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ONE", string(body))
}

func TestArchiveCreateAndExtractRunThroughOperationsWorker(t *testing.T) {
	srv, client, _, tok, opsSvc := aiFixtureWithOps(t)
	seedFiles(t, client, srv.URL, tok, map[string]string{
		"main://source/report.txt": "operation-backed archive",
	})

	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/create", tok, map[string]any{
		"sources": []string{"main://source/report.txt"},
		"dest":    "main://archives/report.zip",
		"format":  "zip",
	})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var created struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	createOp := waitForArchiveOp(t, opsSvc, created.Op.ID)
	require.Equal(t, ops.StatusOK, createOp.Status, createOp.Error)
	assert.Equal(t, 100, createOp.Total)
	assert.Equal(t, 100, createOp.Done)

	code, raw := aiDownload(t, client, srv.URL, tok, "main://archives/report.zip")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "operation-backed archive", openZip(t, raw)["report.txt"])

	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/extract", tok, map[string]any{
		"path": "main://archives/report.zip",
		"dest": "main://restored",
	})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var extracted struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&extracted))
	resp.Body.Close()
	extractOp := waitForArchiveOp(t, opsSvc, extracted.Op.ID)
	require.Equal(t, ops.StatusOK, extractOp.Status, extractOp.Error)
	assert.Equal(t, 1, extractOp.Total)
	assert.Equal(t, 1, extractOp.Done)

	code, body := aiDownload(t, client, srv.URL, tok, "main://restored/report.txt")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "operation-backed archive", string(body))
}

func TestArchiveExtractRejectsUnsafeZipBeforeQueueing(t *testing.T) {
	srv, client, _, tok, _ := aiFixtureWithOps(t)
	raw := craftedZip(t, map[string]string{
		"../escape.txt": "must never be written",
		"safe.txt":      "the whole unsafe archive is rejected",
	})
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":           "main://archives/unsafe.zip",
		"content_base64": base64.StdEncoding.EncodeToString(raw),
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/extract", tok, map[string]any{
		"path": "main://archives/unsafe.zip",
		"dest": "main://restored",
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	resp.Body.Close()
	assert.Equal(t, "UNSUPPORTED_FORMAT", body["code"])

	for _, target := range []string{"main://escape.txt", "main://restored/safe.txt"} {
		code, _ := aiDownload(t, client, srv.URL, tok, target)
		assert.Equal(t, http.StatusNotFound, code, "%s must not have been written", target)
	}
}

func TestArchiveExtractEnforcesPolicyBeforeQueueing(t *testing.T) {
	srv, client, store, tok, _ := aiFixtureWithOps(t)
	require.NoError(t, store.UpsertSetting(context.Background(), archivecli.SettingMaxEntries, "1"))
	raw := craftedZip(t, map[string]string{"one.txt": "one", "two.txt": "two"})
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{
		"path":           "main://archives/too-many.zip",
		"content_base64": base64.StdEncoding.EncodeToString(raw),
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/extract", tok, map[string]any{
		"path": "main://archives/too-many.zip",
		"dest": "main://restored",
	})
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	resp.Body.Close()
	assert.Equal(t, "ARCHIVE_LIMIT_EXCEEDED", body["code"])

	for _, target := range []string{"main://restored/one.txt", "main://restored/two.txt"} {
		code, _ := aiDownload(t, client, srv.URL, tok, target)
		assert.Equal(t, http.StatusNotFound, code)
	}
}

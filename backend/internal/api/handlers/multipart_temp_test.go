package handlers_test

// Guards against the multipart temp-file leak that filled fm.brf.sh's disk on
// 2026-08-09: 74 leftover /tmp/multipart-* files, 29 GB, from uploads that had
// all returned 200. Parts above the in-memory limit are spilled to $TMPDIR by
// mime/multipart, and net/http only removes them when the request the server
// holds is the same struct the handler parsed — behind a router that hands the
// handler a derived request, nothing cleans up and the disk fills silently.
//
// The behavioural test drives a REAL httptest server on purpose. With
// httptest.NewRecorder the server's own cleanup never runs at all, so the test
// would fail even against correct code — it would be measuring the wrong thing.

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// multipartTempLeftovers lists spilled multipart temp files still sitting in dir.
func multipartTempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "multipart-*"))
	require.NoError(t, err)
	return matches
}

// TestAIUpload_DoesNotLeakMultipartTempFiles uploads past the in-memory limit
// so the body is spilled to disk, then asserts nothing is left behind.
func TestAIUpload_DoesNotLeakMultipartTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	srv, client, _, tok := aiFixture(t)

	// 40 MiB > the 32 MiB in-memory limit, so mime/multipart spills to $TMPDIR.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("path", "main://buyuk/dosya.bin"))
	fw, err := mw.CreateFormFile("file", "dosya.bin")
	require.NoError(t, err)
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 40; i++ {
		_, err = fw.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())

	req, err := http.NewRequest("POST", srv.URL+"/api/ai/upload", &body)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	leftovers := multipartTempLeftovers(t, tmp)
	assert.Empty(t, leftovers,
		"upload left multipart temp files behind; they accumulate until the disk fills")
}

// TestMultipartHandlers_AllRemoveTempFiles is the source-scanning guard: every
// handler that parses a multipart form must also drop the spilled temp files.
// A new upload endpoint added without that line reintroduces the leak, and the
// symptom (a slowly filling disk) points nowhere near the new code.
func TestMultipartHandlers_AllRemoveTempFiles(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	var offenders []string
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		if !bytes.Contains(src, []byte("ParseMultipartForm(")) {
			continue
		}
		scanned++
		if !bytes.Contains(src, []byte("MultipartForm.RemoveAll()")) {
			offenders = append(offenders, f)
		}
	}

	require.NotZero(t, scanned, "guard scanned no files — the glob or the package layout changed")
	assert.Empty(t, offenders,
		"these call ParseMultipartForm without removing the spilled temp files: %v", offenders)
}

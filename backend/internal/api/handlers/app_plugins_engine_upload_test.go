package handlers_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Uploading an app that brings the office engine (filex 0.57) ─────────
//
// The wizard's Upload files carries engine.zip as the multipart part
// `engine`, beside the interface bundle, for an app whose manifest has an
// `engines.office` block (filex-office-editor 0.4.0). The route reads the
// part for the review and the install alike; without it the manifest is
// refused before anything is kept.

func storedZip(t *testing.T, files [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f[0], Method: zip.Store})
		require.NoError(t, err)
		_, _ = io.WriteString(w, f[1])
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestAppPlugins_AnUploadCarriesTheOfficeEngineAsItsOwnPart(t *testing.T) {
	f := newAppFixture(t, nil)
	wasm := "\x00asm\x01\x00\x00\x00"
	engine := storedZip(t, [][2]string{{"x2t-server.wasm", wasm}, {"layout-host.wasm", wasm}, {"layout/worker.js", "print('ready')"}})
	ui := storedZip(t, [][2]string{
		{"index.html", "<!doctype html><html><head><meta charset=utf-8></head><body>editor</body></html>"},
		{"editor/sdkjs/word/sdk-all.js", "var sdk;"},
		{"editor/fonts/000", "FONT"},
	})
	sum := sha256.Sum256(engine)
	manifest, err := json.Marshal(map[string]any{
		"manifest_version": 1, "name": "oe-upload", "version": "0.4.0", "label": map[string]any{"en": "Office engine upload"},
		"permissions": []string{"files:read", "files:write"},
		"ui":          map[string]any{"bundle": map[string]any{}},
		"views": []any{map[string]any{"id": "editor", "placement": "viewer", "ui": "index.html",
			"label": map[string]any{"en": "Office engine upload"}, "applies": map[string]any{"ext": []string{"oeu"}}}},
		"engines": map[string]any{"office": map[string]any{"sha256": hex.EncodeToString(sum[:])}},
	})
	require.NoError(t, err)

	dryRun := func(withEngine bool) (int, map[string]any) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		parts := map[string][]byte{"manifest": manifest, "ui": ui}
		if withEngine {
			parts["engine"] = engine
		}
		for _, name := range []string{"manifest", "ui", "engine"} {
			if b, ok := parts[name]; ok {
				fw, _ := w.CreateFormFile(name, name)
				_, _ = fw.Write(b)
			}
		}
		require.NoError(t, w.Close())
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		res, err := f.admin.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var body map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		return res.StatusCode, body
	}

	code, body := dryRun(true)
	require.Equal(t, http.StatusOK, code, "%v", body)
	var ids []string
	for _, p := range body["permissions"].([]any) {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	assert.Contains(t, ids, "office-engine", "the engine is a grant of its own, derived from engines.office")

	code, body = dryRun(false)
	assert.Equal(t, http.StatusBadRequest, code, "%v", body)
	assert.Equal(t, "manifest_invalid", body["error"])
	assert.Contains(t, body["detail"], "engine.zip", "the refusal names the file that is missing")
}

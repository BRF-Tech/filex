package handlers_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ⚠⚠ The review of an install from a source reads the source's manifest
// ALONE (wasmplugin/fetch.go, "A REVIEW READS THE MANIFEST"). It used to
// download the whole app for the review - for the Office editor a 126 MB
// ui.zip and a 14 MB engine.zip from GitHub while the request waited - and
// the "Review update" screen answered 504 from the proxy (2026-11-09). The
// install that follows the review downloads the files and holds every byte
// to the sha256 the manifest pins: a bundle that is not the reviewed one is
// refused there.
func TestAppPlugins_ASourceReviewReadsTheManifestAloneAndTheInstallHoldsTheFilesToIt(t *testing.T) {
	f := newAppFixture(t, nil)
	bundle := uiBundleZip(t)
	sum := sha256.Sum256(bundle)
	pin := hex.EncodeToString(sum[:])
	manifest := strings.Replace(uiOnlyManifest, `"ui":{"bundle":{}}`, `"ui":{"bundle":{"url":"ui.zip","sha256":"`+pin+`"}}`, 1)
	require.NotEqual(t, uiOnlyManifest, manifest)

	var mu sync.Mutex
	var asked []string
	served := bundle
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		body := served
		mu.Unlock()
		switch r.URL.Path {
		case "/filex-app.json":
			_, _ = w.Write([]byte(manifest))
		case "/ui.zip":
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(src.Close)
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := asked
		asked = nil
		return out
	}
	body := map[string]any{"url": "", "manifest_url": src.URL + "/filex-app.json",
		"permissions": []string{"files:read", "files:write", "ui", "ui-viewer:.sketch"}}

	// The review: the manifest, and nothing else.
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", body)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, []string{"/filex-app.json"}, take(), "the review downloads no bundle")
	var dry struct {
		FromManifest bool `json:"from_manifest"`
		Engine       bool `json:"engine"`
		UI           struct {
			SHA256 string `json:"sha256"`
			Files  int    `json:"files"`
		} `json:"ui"`
		Permissions []struct {
			ID string `json:"id"`
		} `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(raw, &dry))
	assert.True(t, dry.FromManifest, "the review says it was read from the manifest")
	assert.False(t, dry.Engine, "an interface-only app: no module")
	assert.Equal(t, pin, dry.UI.SHA256, "the bundle is named by the hash the manifest pins")
	assert.Zero(t, dry.UI.Files, "its files are not known before it is downloaded")
	var ids []string
	for _, p := range dry.Permissions {
		ids = append(ids, p.ID)
	}
	assert.Equal(t, []string{"files:read", "files:write", "ui", "ui-viewer:.sketch"}, ids, "every permission comes from the manifest")

	// A source that serves another bundle than the one the manifest pins:
	// the review (the manifest) passes, the install refuses it.
	mu.Lock()
	served = append(append([]byte(nil), bundle...), 0)
	mu.Unlock()
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", body)
	require.Equal(t, http.StatusOK, status, string(raw))
	take()
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins", body)
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "sha256_mismatch", "the install holds the bytes to the reviewed pin")
	assert.Equal(t, []string{"/filex-app.json", "/ui.zip"}, take(), "the install downloads the bundle")

	// The bundle the manifest pins: installed.
	mu.Lock()
	served = bundle
	mu.Unlock()
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins", body)
	require.Equal(t, http.StatusCreated, status, string(raw))
	assert.Equal(t, []string{"/filex-app.json", "/ui.zip"}, take())
}

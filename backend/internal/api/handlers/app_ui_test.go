package handlers_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── An app's own interface through the whole router (M1) ────────────────

func uiBundleZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"index.html": "<!doctype html><html><head><script src=app.js></script></head><body>hi</body></html>",
		"app.js":     "console.log(1)",
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = io.WriteString(w, body)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

const uiOnlyManifest = `{"manifest_version":1,"name":"sketch","version":"1.0.0","label":{"en":"Sketch"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Sketch"},"applies":{"ext":["sketch"]}}]}`

// installUIApp uploads the interface-only app: dry run, then install.
func (f *appFixture) installUIApp(t *testing.T) map[string]any {
	t.Helper()
	bundle := uiBundleZip(t)
	body := func(grant []string) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		mf, _ := w.CreateFormFile("manifest", "filex-app.json")
		_, _ = mf.Write([]byte(uiOnlyManifest))
		uf, _ := w.CreateFormFile("ui", "ui.zip")
		_, _ = uf.Write(bundle)
		if grant != nil {
			g, _ := json.Marshal(map[string]any{"permissions": grant})
			_ = w.WriteField("grant", string(g))
		}
		_ = w.Close()
		return &buf, w.FormDataContentType()
	}
	post := func(url string, grant []string) (int, []byte) {
		buf, ct := body(grant)
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+url, buf)
		req.Header.Set("Content-Type", ct)
		res, err := f.admin.Do(req)
		require.NoError(t, err)
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, raw
	}
	code, raw := post("/api/admin/app-plugins?dry_run=1", nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	var dry map[string]any
	require.NoError(t, json.Unmarshal(raw, &dry))
	code, raw = post("/api/admin/app-plugins", []string{"files:read", "files:write", "ui", "ui-viewer:.sketch"})
	require.Equal(t, http.StatusCreated, code, string(raw))
	return dry
}

func TestAppUI_AnInterfaceOnlyAppInstallsServesAndIsListed(t *testing.T) {
	f := newAppFixture(t, nil)
	dry := f.installUIApp(t)
	assert.Equal(t, false, dry["engine"], "the review says there is no module")
	ui, _ := dry["ui"].(map[string]any)
	require.NotNil(t, ui, "the review has the interface's own group")
	assert.EqualValues(t, 2, ui["files"])
	var ids []string
	for _, p := range dry["permissions"].([]any) {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	assert.Equal(t, []string{"files:read", "files:write", "ui", "ui-viewer:.sketch"}, ids)

	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/actions")
	require.NoError(t, err)
	var ans struct {
		Views []struct {
			Placement string `json:"placement"`
			UI        *struct {
				URL    string   `json:"url"`
				Grants []string `json:"grants"`
			} `json:"ui"`
		} `json:"views"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&ans))
	res.Body.Close()
	require.Len(t, ans.Views, 1)
	require.NotNil(t, ans.Views[0].UI)
	assert.Equal(t, "viewer", ans.Views[0].Placement)
	url := ans.Views[0].UI.URL
	assert.True(t, strings.HasPrefix(url, "/_appui/sketch/"), url)

	// No session at all: the interface is public bytes under a policy.
	res, err = http.Get(f.srv.URL + url)
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	csp := res.Header.Get("Content-Security-Policy")
	pkg := f.srv.URL + strings.TrimSuffix(url, "index.html")
	assert.Contains(t, csp, "script-src "+pkg+" 'sha256-", "the package is named by its explicit origin and path")
	assert.Contains(t, csp, "sandbox allow-scripts")
	assert.Contains(t, csp, "connect-src 'none'")
	assert.NotContains(t, csp, "frame-ancestors 'self'", "the explorer on another site frames it too; the middleware must not add filex's own")
	assert.Equal(t, `("`+pkg+`*")`, res.Header.Get("Connection-Allowlist"), "this version's path, nothing else")
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
	assert.Contains(t, string(body), "<script>(()=>{", "the bootstrap is in")

	// The signed-in person's cookie changes nothing and nothing is set.
	res, err = f.admin.Get(f.srv.URL + url)
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Empty(t, res.Header.Values("Set-Cookie"))
	assert.Empty(t, res.Header.Get("Access-Control-Allow-Credentials"))

	// Behind a TLS-terminating proxy the package is named https.
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+url, nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	assert.Contains(t, res.Header.Get("Content-Security-Policy"), "script-src https://"+strings.TrimPrefix(pkg, "http://"))
}

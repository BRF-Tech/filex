package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An app that ships a language adds it to filex itself, public pages
// included: a signature request in a language the page around it does not
// speak is half a translation.
func TestPublicBranding_CarriesLanguagesAppsAdd(t *testing.T) {
	f := newAppFixture(t, nil)

	status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/branding", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var before struct {
		Locales   []string          `json:"locales"`
		UILocales []json.RawMessage `json:"ui_locales"`
	}
	require.NoError(t, json.Unmarshal(raw, &before))
	assert.Contains(t, before.Locales, "en")
	assert.Empty(t, before.UILocales, "nothing installed, nothing added")

	f.installEcho(t)

	status, raw = doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/api/public/branding", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var after struct {
		Locales   []string          `json:"locales"`
		UILocales []json.RawMessage `json:"ui_locales"`
	}
	require.NoError(t, json.Unmarshal(raw, &after))
	// The echo fixture ships no language pack, so the shape must stay honest
	// rather than invent one.
	assert.Empty(t, after.UILocales)
	assert.Equal(t, before.Locales, after.Locales, "an app without a pack adds no language")
}

// postLanguagePack installs a manifest through the multipart route with NO
// module part — the way the admin panel sends a language pack.
func postLanguagePack(t *testing.T, f *appFixture, manifest []byte, dry bool) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write(manifest)
	_ = w.WriteField("grant", `{"permissions":[]}`)
	require.NoError(t, w.Close())
	u := f.srv.URL + "/api/admin/app-plugins"
	if dry {
		u += "?dry_run=1"
	}
	req, _ := http.NewRequest(http.MethodPost, u, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := f.admin.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func spanishPack(t *testing.T, keys int) []byte {
	t.Helper()
	es := map[string]string{"ctx.download": "Descargar", "appPlugins.title": "Aplicaciones"}
	for i := 0; len(es) < keys; i++ {
		es[fmt.Sprintf("filler.k%d", i)] = "texto de relleno"
	}
	b, err := json.Marshal(map[string]any{
		"manifest_version": 1, "name": "lang-es", "version": "1.0.0",
		"label":       map[string]string{"en": "Spanish"},
		"permissions": []string{},
		"ui_locales":  map[string]any{"es": es, "ar": map[string]string{"ctx.download": "تحميل"}},
	})
	require.NoError(t, err)
	return b
}

// The whole road a translator's pack travels, over HTTP: installed without a
// module (the measurement that started this — "wasm file is required", and
// before that "at most 2000" for a pack of 2 500 strings), listed as a
// language pack, offered on the public branding answer as a list, its strings
// fetched per language, and gone the moment it is removed.
func TestLanguagePack_InstallsWithoutAModule_AndIsServedPerLanguage(t *testing.T) {
	f := newAppFixture(t, nil)
	manifest := spanishPack(t, 2500)

	status, raw := postLanguagePack(t, f, manifest, true)
	require.Equal(t, http.StatusOK, status, string(raw))
	var dry struct {
		Kind           string `json:"kind"`
		ManifestSHA256 string `json:"manifest_sha256"`
		WasmSHA256     string `json:"wasm_sha256"`
		Languages      []struct {
			Code string `json:"code"`
			Keys int    `json:"keys"`
			RTL  bool   `json:"rtl"`
		} `json:"languages"`
	}
	require.NoError(t, json.Unmarshal(raw, &dry))
	assert.Equal(t, "language_pack", dry.Kind)
	assert.Len(t, dry.ManifestSHA256, 64)
	assert.Empty(t, dry.WasmSHA256)
	require.Len(t, dry.Languages, 2)
	assert.Equal(t, "ar", dry.Languages[0].Code)
	assert.True(t, dry.Languages[0].RTL, "the review is told Arabic is right to left, so it can say the layout is not yet")
	assert.Equal(t, 2500, dry.Languages[1].Keys)

	status, raw = postLanguagePack(t, f, manifest, false)
	require.Equal(t, http.StatusCreated, status, string(raw))
	var row struct {
		ID    int64  `json:"id"`
		Kind  string `json:"kind"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &row))
	assert.Equal(t, "language_pack", row.Kind)
	assert.Equal(t, "running", row.State)

	anon := freshClient(t)
	status, raw = doReq(t, anon, http.MethodGet, f.srv.URL+"/api/public/branding", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var b struct {
		Locales   []string `json:"locales"`
		UILocales []struct {
			Code    string          `json:"code"`
			Source  string          `json:"source"`
			Plugin  string          `json:"plugin"`
			RTL     bool            `json:"rtl"`
			Strings json.RawMessage `json:"strings"`
		} `json:"ui_locales"`
	}
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Contains(t, b.Locales, "es")
	assert.Contains(t, b.Locales, "ar")
	require.Len(t, b.UILocales, 2, string(raw))
	assert.Equal(t, "es", b.UILocales[1].Code)
	assert.Equal(t, "plugin", b.UILocales[1].Source)
	assert.Equal(t, "lang-es", b.UILocales[1].Plugin)
	assert.Nil(t, b.UILocales[1].Strings, "the list carries no strings — those are fetched per language")
	assert.Less(t, len(raw), 2000, "the answer every public page reads stays small whatever the packs weigh")

	status, raw = doReq(t, anon, http.MethodGet, f.srv.URL+"/api/public/ui-locales/es", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var es struct {
		Code    string            `json:"code"`
		Strings map[string]string `json:"strings"`
	}
	require.NoError(t, json.Unmarshal(raw, &es))
	assert.Equal(t, "es", es.Code)
	assert.Len(t, es.Strings, 2500)
	assert.Equal(t, "Descargar", es.Strings["ctx.download"])

	status, _ = doReq(t, anon, http.MethodGet, f.srv.URL+"/api/public/ui-locales/fr", nil)
	assert.Equal(t, http.StatusNotFound, status, "a language no app ships")

	// Uninstalled: the language leaves the list AND stops being served.
	status, raw = doReq(t, f.admin, http.MethodDelete, fmt.Sprintf("%s/api/admin/app-plugins/%d", f.srv.URL, row.ID), nil)
	require.True(t, status/100 == 2, "%d %s", status, raw)
	status, raw = doReq(t, anon, http.MethodGet, f.srv.URL+"/api/public/branding", nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(raw), `"es"`)
	status, _ = doReq(t, anon, http.MethodGet, f.srv.URL+"/api/public/ui-locales/es", nil)
	assert.Equal(t, http.StatusNotFound, status)
}

// A manifest larger than the ceiling is refused as too large, not truncated
// into a JSON syntax error about a file that is merely big.
func TestLanguagePack_ManifestOverTheCeiling_IsTooLarge(t *testing.T) {
	f := newAppFixture(t, nil)
	huge := []byte(`{"manifest_version":1,"pad":"` + strings.Repeat("x", 16<<20) + `"}`)
	status, raw := postLanguagePack(t, f, huge, true)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status, string(raw))
	assert.Contains(t, string(raw), "too_large")
}

// An app with actions still needs its module, and says so in words.
func TestLanguagePack_AnAppWithoutItsModule_IsRefused(t *testing.T) {
	f := newAppFixture(t, nil)
	b, _ := json.Marshal(map[string]any{
		"manifest_version": 1, "name": "hello", "version": "1", "label": map[string]string{"en": "Hello"},
		"permissions": []string{"state"},
	})
	status, raw := postLanguagePack(t, f, b, true)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, string(raw), "no module supplied")
}

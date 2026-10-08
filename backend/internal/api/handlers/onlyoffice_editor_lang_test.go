package handlers_test

// The ONLYOFFICE editor's language (GitHub Discussion #93, task #214),
// through the whole router: the editor config speaks the language of the
// screen that asked for it (Accept-Language, which the viewer sends), the
// administrator can fix one language for everybody on External services, and
// the admin API refuses a language the editor does not offer.
//
// Red on the old code: the config said `"lang": "en"` whatever the request
// carried short of an explicit `lang`, and there was no setting.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/external"
)

// editorLangOf asks for the editor config of nodeID with an Accept-Language
// header (and query extras) and answers its editorConfig.lang and .region.
func editorLangOf(t *testing.T, h *extHarness, nodeID int64, acceptLanguage, query string) (string, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/files/onlyoffice/config?id="+itoa(nodeID)+"&mode=view"+query, nil)
	require.NoError(t, err)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	resp, err := h.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Config struct {
			EditorConfig struct {
				Lang   string `json:"lang"`
				Region string `json:"region"`
			} `json:"editorConfig"`
		} `json:"config"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Config.EditorConfig.Lang, out.Config.EditorConfig.Region
}

func patchExternal(t *testing.T, h *extHarness, name string, body map[string]any) (int, map[string]any) {
	t.Helper()
	resp := h.Patch(t, "/api/admin/external/"+name, body)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestOnlyOfficeConfig_TheEditorSpeaksTheScreensLanguage(t *testing.T) {
	h, nodeID := liveExternalServer(t, nil)
	status, _ := patchExternal(t, h, "onlyoffice", map[string]any{
		"enabled": true, "url": "https://docs.example", "secret": "s3cr3t",
	})
	require.Equal(t, http.StatusOK, status)

	lang, region := editorLangOf(t, h, nodeID, "tr-TR,tr;q=0.9,en;q=0.8", "")
	assert.Equal(t, "tr", lang, "a Turkish screen got an English editor")
	assert.Equal(t, "tr-TR", region)

	lang, region = editorLangOf(t, h, nodeID, "de-AT,de;q=0.9", "")
	assert.Equal(t, "de", lang)
	assert.Equal(t, "de-AT", region, "a region ONLYOFFICE lists is kept for the spreadsheet's formats")

	// The request's own `lang` comes before the screen's.
	lang, _ = editorLangOf(t, h, nodeID, "tr-TR", "&lang=es")
	assert.Equal(t, "es", lang)

	// A language the editor does not offer falls to the next the person gave.
	lang, _ = editorLangOf(t, h, nodeID, "fa-IR,fa;q=0.9,fr;q=0.5", "")
	assert.Equal(t, "fr", lang)
}

func TestOnlyOfficeConfig_AFixedEditorLanguageIsEverybodys(t *testing.T) {
	h, nodeID := liveExternalServer(t, nil)
	ctx := context.Background()
	status, _ := patchExternal(t, h, "onlyoffice", map[string]any{
		"enabled": true, "url": "https://docs.example", "secret": "s3cr3t",
		"callback_url": "http://filex:5212",
	})
	require.Equal(t, http.StatusOK, status)

	status, _ = patchExternal(t, h, "onlyoffice", map[string]any{"editor_lang": "de-DE"})
	require.Equal(t, http.StatusOK, status)
	row, err := h.Store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "de", external.EditorLangFromOptions(row.OptionsJSON), "stored the way ONLYOFFICE's list writes it")
	assert.Equal(t, "http://filex:5212", external.CallbackURLFromOptions(row.OptionsJSON), "the other options are kept")

	lang, region := editorLangOf(t, h, nodeID, "tr-TR,tr;q=0.9", "&lang=tr")
	assert.Equal(t, "de", lang, "the administrator's language beats the person's")
	assert.Equal(t, "de-DE", region)

	// The list says it, and offers the server's languages.
	resp := h.Get(t, "/api/admin/external")
	var list struct {
		Entries []struct {
			Name            string `json:"Name"`
			EditorLang      string `json:"editor_lang"`
			EditorLangEnv   bool   `json:"editor_lang_env_managed"`
			EditorLanguages []struct {
				Code string `json:"code"`
				Name string `json:"name"`
			} `json:"editor_languages"`
		} `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	resp.Body.Close()
	var found bool
	for _, e := range list.Entries {
		if e.Name != "onlyoffice" {
			assert.Empty(t, e.EditorLanguages, "%s has no editor language", e.Name)
			continue
		}
		found = true
		assert.Equal(t, "de", e.EditorLang)
		assert.False(t, e.EditorLangEnv)
		codes := map[string]string{}
		for _, l := range e.EditorLanguages {
			codes[l.Code] = l.Name
		}
		assert.Equal(t, "Türkçe", codes["tr"])
		assert.Equal(t, "Deutsch", codes["de"])
		assert.Contains(t, codes, "pt-PT")
		assert.Contains(t, codes, "zh-TW")
	}
	require.True(t, found, "the ONLYOFFICE row is listed")

	// Back to automatic: the person's language again, and no key left behind.
	status, _ = patchExternal(t, h, "onlyoffice", map[string]any{"editor_lang": "auto"})
	require.Equal(t, http.StatusOK, status)
	lang, _ = editorLangOf(t, h, nodeID, "tr-TR", "")
	assert.Equal(t, "tr", lang)
	row, err = h.Store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.NotContains(t, row.OptionsJSON, "editor_lang")
}

func TestExternalAdmin_RefusesALanguageTheEditorDoesNotOffer(t *testing.T) {
	h, _ := liveExternalServer(t, nil)
	ctx := context.Background()
	status, _ := patchExternal(t, h, "onlyoffice", map[string]any{
		"enabled": true, "url": "https://docs.example", "secret": "s3cr3t", "editor_lang": "fr",
	})
	require.Equal(t, http.StatusOK, status)

	for _, bad := range []string{"klingon", "fa", "<script>"} {
		status, out := patchExternal(t, h, "onlyoffice", map[string]any{"editor_lang": bad})
		assert.Equal(t, http.StatusBadRequest, status, bad)
		assert.Equal(t, "editor_lang_invalid", out["error"], bad)
		assert.NotEmpty(t, out["message"], bad)
	}
	row, err := h.Store.GetExternalService(ctx, "onlyoffice")
	require.NoError(t, err)
	assert.Equal(t, "fr", external.EditorLangFromOptions(row.OptionsJSON), "a refused value changes nothing")

	// draw.io has no editor language.
	status, out := patchExternal(t, h, "drawio", map[string]any{"editor_lang": "de"})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "editor_lang_invalid", out["error"])
}

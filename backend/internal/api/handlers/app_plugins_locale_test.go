package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A queued job speaks the language the screen was speaking.
//
// The person walking the wizard sees surfaces rendered in the language of
// the request; the job the wizard submits used to fall back to English
// whenever the account had no language set, so the toast and the ops row
// that closed a Turkish flow came back in English. One flow, two
// languages — the exact thing that made the signing plugin look half
// translated.
func TestAppPlugins_Run_JobSpeaksTheRequestLanguage(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "hello")
	me, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)

	for _, tc := range []struct {
		account string
		accept  string
		label   string
	}{
		{"tr", "en-GB,en;q=0.9", "Büyük harf"},
		{"", "tr-TR,tr;q=0.9,en;q=0.8", "Büyük harf"},
		{"", "", "Upper-case"},
		{"en", "tr-TR,tr;q=0.9", "Upper-case"},
	} {
		require.NoError(t, f.store.UpdateUserLocale(t.Context(), me.ID, tc.account, "UTC"))
		body, _ := json.Marshal(map[string]any{"paths": []string{"main://docs/a.txt"}})
		req, err2 := http.NewRequest(http.MethodPost,
			f.srv.URL+"/api/files/plugins/actions/echo/upper/run", bytes.NewReader(body))
		require.NoError(t, err2)
		req.Header.Set("Content-Type", "application/json")
		if tc.accept != "" {
			req.Header.Set("Accept-Language", tc.accept)
		}
		resp, err3 := f.admin.Do(req)
		require.NoError(t, err3)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))

		var ans struct {
			Op struct {
				ID    int64  `json:"id"`
				Label string `json:"label"`
			} `json:"op"`
		}
		require.NoError(t, json.Unmarshal(raw, &ans))
		assert.Equal(t, tc.label, ans.Op.Label,
			"account language %q, Accept-Language %q", tc.account, tc.accept)
	}
}

// A Spanish reader (a Spanish pack installed) — the app is told `es`, and an
// app that has no Spanish answers in English: never Turkish, never a key.
func TestAppPlugins_Run_APackLanguageReachesTheApp(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{"es": {"nav.files": "Archivos"}})
	t.Cleanup(func() { srvtext.SetPacks(nil) })
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "hello")
	me, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)
	require.NoError(t, f.store.UpdateUserLocale(t.Context(), me.ID, "es", "UTC"))

	body, _ := json.Marshal(map[string]any{"paths": []string{"main://docs/a.txt"}})
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var ans struct {
		Op struct {
			ID    int64  `json:"id"`
			Label string `json:"label"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	assert.Equal(t, "Upper-case", ans.Op.Label, "echo has no Spanish label: English")

	jobs, err := f.store.ListAppPluginJobsByOp(t.Context(), []int64{ans.Op.ID})
	require.NoError(t, err)
	require.NotNil(t, jobs[ans.Op.ID])
	assert.Equal(t, "es", jobs[ans.Op.ID].Locale, "the job runs in the reader's language — it was narrowed to `en`")
}

// The signing app ships Spanish, German and French (filex-sign c1699ed) —
// and until srvtext removed normLang the host told every app `en` for all
// three, so those translations never reached anybody. A reader in any of
// them (a pack for it installed) opens an app SCREEN in that language and
// its job runs in it: the two calls the signing wizard and the signing mail
// are made from. Red if a normLang-style collapse to en/tr comes back.
func TestAppPlugins_ESDEFRReachTheAppsScreensAndJobs(t *testing.T) {
	srvtext.SetPacks(srvtext.StaticPacks{
		"es": {"nav.files": "Archivos"},
		"de": {"nav.files": "Dateien"},
		"fr": {"nav.files": "Fichiers"},
	})
	t.Cleanup(func() { srvtext.SetPacks(nil) })
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "hello")
	me, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)

	for _, lang := range []string{"es", "de", "fr"} {
		require.NoError(t, f.store.UpdateUserLocale(t.Context(), me.ID, lang, "UTC"))

		// The screen: what the signing wizard is drawn from.
		status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/plugins/views/echo/wizard?path=main://docs/a.txt", nil)
		require.Equal(t, http.StatusOK, status, string(raw))
		assert.Contains(t, string(raw), "locale="+lang, "the app's screen was told another language than %s", lang)

		// The job: what the request, its mail and its receipt are written in.
		status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
			map[string]any{"paths": []string{"main://docs/a.txt"}})
		require.Equal(t, http.StatusAccepted, status, string(raw))
		var ans struct {
			Op struct {
				ID int64 `json:"id"`
			} `json:"op"`
		}
		require.NoError(t, json.Unmarshal(raw, &ans))
		jobs, err := f.store.ListAppPluginJobsByOp(t.Context(), []int64{ans.Op.ID})
		require.NoError(t, err)
		require.NotNil(t, jobs[ans.Op.ID])
		assert.Equal(t, lang, jobs[ans.Op.ID].Locale, "the job was narrowed away from %s", lang)
	}
}

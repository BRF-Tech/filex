package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// ⭐ The screen's language wins over the account's — when the screen says
// which it is.
//
// An embedded explorer (<filex-explorer>, config.locale) draws Turkish for a
// person whose account says English: the host page chose the language, the
// account was never asked. The explorer names its language on every app
// call (`?lang=`), and the app must answer THAT screen in it — the plain
// strings an app picks by `context.locale` (a form field's label and help,
// a select's options) came out English in the middle of a Turkish wizard
// (2026-09-26: "Identity" and "One signer per line…" in the signing app's
// Turkish popup). Without `?lang=` nothing changes: the account first, then
// Accept-Language (the rows of TestAppPlugins_Run_JobSpeaksTheRequestLanguage).
func TestAppPlugins_TheScreensOwnLanguageWins(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "hello")
	me, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)
	require.NoError(t, f.store.UpdateUserLocale(t.Context(), me.ID, "en", "UTC"))

	get := func(url string) string {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := f.admin.Do(req)
		require.NoError(t, err)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
		return string(raw)
	}
	base := f.srv.URL + "/api/files/plugins/views/echo/wizard?path=main://docs/a.txt"

	// The screen: the opening GET and every later event.
	assert.Contains(t, get(base+"&lang=tr"), "locale=tr", "an explorer drawing Turkish was answered in the account's English")
	body, _ := json.Marshal(map[string]any{"paths": []string{"main://docs/a.txt"}, "event": "change", "state": map[string]any{}})
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/wizard/event?lang=tr", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Contains(t, string(raw), "locale=tr", "a later event on the same screen went back to the account's language")

	// Nothing named, or nothing this server speaks: the account, as before.
	assert.Contains(t, get(base), "locale=en")
	assert.Contains(t, get(base+"&lang=xx"), "locale=en", "an unknown ?lang= is not a language")

	// The job the screen queues runs in the screen's language too: its ops
	// row, its toast, whatever it writes for this person.
	body, _ = json.Marshal(map[string]any{"paths": []string{"main://docs/a.txt"}})
	req, err = http.NewRequest(http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run?lang=tr", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err = f.admin.Do(req)
	require.NoError(t, err)
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var ans struct {
		Op struct {
			ID    int64  `json:"id"`
			Label string `json:"label"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	assert.Equal(t, "Büyük harf", ans.Op.Label)
	jobs, err := f.store.ListAppPluginJobsByOp(t.Context(), []int64{ans.Op.ID})
	require.NoError(t, err)
	require.NotNil(t, jobs[ans.Op.ID])
	assert.Equal(t, "tr", jobs[ans.Op.ID].Locale)
}

// ⭐ A job's words are chosen when somebody READS the operations list, in the
// language on THEIR screen — not frozen at submit in the submitter's.
//
// The ops row used to carry the action label and the app's final message
// flattened to one language when the job was created (`Label.Get(locale)`,
// `Message.Get(job.Locale)`), so an embed drawing Turkish over an English
// account read "Convert…" and an English result under a Turkish tray
// (2026-09-26, the Convert app in an embedded explorer). The row keeps every
// language the app wrote; `?lang=` on the read picks one.
func TestAppPlugins_OpsRowSpeaksTheReadersLanguage(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "hello")
	me, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)
	require.NoError(t, f.store.UpdateUserLocale(t.Context(), me.ID, "en", "UTC"))

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run?lang=en",
		map[string]any{"paths": []string{"main://docs/a.txt"}})
	require.Equal(t, http.StatusAccepted, status, string(raw))
	var ans struct {
		Op struct {
			ID    int64  `json:"id"`
			Label string `json:"label"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	assert.Equal(t, "Upper-case", ans.Op.Label, "submitted from an English screen")
	done := f.drain(t, ans.Op.ID)
	require.Equal(t, "ok", done["status"], done)

	read := func(lang string) map[string]any {
		status, raw := doReq(t, f.admin, http.MethodGet, f.srv.URL+fmt.Sprintf("/api/files/ops/%d?lang=%s", ans.Op.ID, lang), nil)
		require.Equal(t, http.StatusOK, status, string(raw))
		var op map[string]any
		require.NoError(t, json.Unmarshal(raw, &op))
		return op
	}
	tr := read("tr")
	assert.Equal(t, "Büyük harf", tr["label"], "a Turkish tray reads the Turkish label")
	assert.Equal(t, "bitti", tr["message"], "…and the app's Turkish result")
	en := read("en")
	assert.Equal(t, "Upper-case", en["label"])
	assert.Equal(t, "done", en["message"])

	// The list the tray polls says the same.
	status, raw = doReq(t, f.admin, http.MethodGet, f.srv.URL+"/api/files/ops?lang=tr", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"label":"Büyük harf"`)
	assert.Contains(t, string(raw), `"message":"bitti"`)
}

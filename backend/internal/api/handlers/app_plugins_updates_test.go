package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// The update routes over HTTP: the per-app switch, "Check now", and an
// upgrade from the app's own source — each held to the same gate as the rest
// of the Apps surface.
func TestAppPlugins_UpdateRoutes(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t) // uploaded: no source to ask
	one := fmt.Sprintf("%s/api/admin/app-plugins/%d", f.srv.URL, id)

	status, raw := doReq(t, f.admin, http.MethodPatch, one, map[string]any{"auto_update": false})
	require.Equal(t, http.StatusOK, status, string(raw))
	var row struct {
		AutoUpdate   bool   `json:"auto_update"`
		Enabled      bool   `json:"enabled"`
		UpdateSource string `json:"update_source"`
	}
	require.NoError(t, json.Unmarshal(raw, &row))
	assert.False(t, row.AutoUpdate)
	assert.True(t, row.Enabled, "switching updates leaves the app on")
	assert.Empty(t, row.UpdateSource, "an uploaded app has no source")

	status, raw = doReq(t, f.admin, http.MethodPatch, one, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins/updates/check", map[string]any{})
	require.Equal(t, http.StatusOK, status, string(raw))
	var checked struct {
		Report struct {
			Checked int `json:"checked"`
		} `json:"report"`
		Runtime map[string]any   `json:"runtime"`
		Plugins []map[string]any `json:"plugins"`
	}
	require.NoError(t, json.Unmarshal(raw, &checked))
	assert.Zero(t, checked.Report.Checked, "nothing with a source to ask")
	assert.Len(t, checked.Plugins, 1, "the list comes back with the answer")
	assert.Contains(t, checked.Runtime, "filex_version")
	assert.Contains(t, checked.Runtime, "updates_checked_at", "the check's time is stored and said")

	// "Review update" on an app that has no source: said, not attempted.
	status, raw = doReq(t, f.admin, http.MethodPost, one+"/upgrade?dry_run=1", map[string]any{"from_source": true})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "manifest_invalid")
	// …and an install has no source of its own to read from.
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", map[string]any{"from_source": true})
	assert.Equal(t, http.StatusBadRequest, status, string(raw))
}

func TestAppPlugins_Demo_NoUpdateCheck(t *testing.T) {
	f := newAppFixture(t, func(c *config.Config) { c.Demo.Mode = true })
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins/updates/check", map[string]any{})
	assert.Equal(t, http.StatusForbidden, status, string(raw))
}

// A language pack installs from its manifest's address alone — the body the
// wizard's From-a-URL tab sends for one (`url` empty: a pack has no module).
// It answered "give github_repo, or url + manifest_url" because the handler
// chose the URL path by the MODULE's address; and an app installed that way
// is the one the update check re-reads.
func TestAppPlugins_LanguagePackInstallsFromItsManifestAddressAlone(t *testing.T) {
	f := newAppFixture(t, nil)
	pack := `{"manifest_version":1,"name":"lang-eo","version":"1.0.0","label":{"en":"Esperanto","tr":"Esperanto"},` +
		`"languages":["en","tr"],"permissions":[],"ui_locales":{"eo":{"common.cancel":"Nuligi"}}}`
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/filex-app.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(pack))
	}))
	t.Cleanup(src.Close)
	body := map[string]any{"url": "", "manifest_url": src.URL + "/filex-app.json", "permissions": []string{}}

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", body)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"kind":"language_pack"`)

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins", body)
	require.Equal(t, http.StatusCreated, status, string(raw))
	var row struct {
		Source       string `json:"source"`
		ManifestURL  string `json:"manifest_url"`
		UpdateSource string `json:"update_source"`
		AutoUpdate   bool   `json:"auto_update"`
	}
	require.NoError(t, json.Unmarshal(raw, &row))
	assert.Equal(t, "url", row.Source)
	assert.Equal(t, src.URL+"/filex-app.json", row.ManifestURL)
	assert.Equal(t, "url", row.UpdateSource, "the update check has an address to re-read")
	assert.True(t, row.AutoUpdate)
}

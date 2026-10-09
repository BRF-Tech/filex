package handlers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The refusals an app's doors give a request they cannot act on - a body
// that is not JSON, no file, too many files, a storage that is not there, an
// id that is not a number, an install that names no source - answer a code
// and the server's sentence in the reader's language, through the production
// router (0.55, second round).
//
// RED PROOF (int/055-wave d8a8cc2f): each answered an English phrase in
// `error` and no `message` ("bad json", "paths are required", "too many
// paths", "unknown adapter: nope", "bad id"), or `manifest_invalid` with
// Go's English ("give github_repo, or url + manifest_url, or a multipart
// upload") that the wizard turned into a sentence of its own.
func TestAppRefusals_ARequestTheDoorsCannotReadIsSaid(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	// The reader is the administrator's account: requestLang reads its
	// language before Accept-Language, and testutil opens it in "en".
	me, err := f.store.GetUserByEmail(t.Context(), "admin@test.local")
	require.NoError(t, err)
	require.NoError(t, f.store.UpdateUserLocale(t.Context(), me.ID, "tr", "UTC"))
	run := f.srv.URL + "/api/files/plugins/actions/echo/upper/run"
	check := func(status int, body map[string]any, wantStatus int, code, said string, p apierr.Params) {
		t.Helper()
		require.Equal(t, wantStatus, status, "%v", body)
		assert.Equal(t, code, body["error"], "%v", body)
		assert.Equal(t, apierr.Text("tr", said, p), body["message"], "%v", body)
	}

	status, body := langJSON(t, f.admin, http.MethodPost, run, "tr", "not an object")
	check(status, body, http.StatusBadRequest, "bad_json", "bad_json", nil)

	status, body = langJSON(t, f.admin, http.MethodPost, run, "tr", map[string]any{"storage_id": f.st.ID, "paths": []string{}})
	check(status, body, http.StatusBadRequest, "paths_required", "paths_required", nil)

	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprintf("main://docs/f%03d.txt", i)
	}
	status, body = langJSON(t, f.admin, http.MethodPost, run, "tr", map[string]any{"paths": many})
	check(status, body, http.StatusBadRequest, "too_many_paths", "too_many_paths", apierr.Params{"max": "500"})

	status, body = langJSON(t, f.admin, http.MethodPost, run, "tr", map[string]any{"paths": []string{"nope://a.txt"}})
	check(status, body, http.StatusBadRequest, "unknown_storage", "unknown_storage", apierr.Params{"name": "nope"})

	status, body = langJSON(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/abc", "tr", nil)
	check(status, body, http.StatusBadRequest, "bad_id", "bad_id", nil)
	status, body = langJSON(t, f.admin, http.MethodGet, f.srv.URL+"/api/admin/app-plugins/999999", "tr", nil)
	check(status, body, http.StatusNotFound, "not_found", "app_missing", nil)

	// An install that names no source: the code the wizard reads stays, the
	// sentence is the server's, the English is detail.
	status, body = langJSON(t, f.admin, http.MethodPost, f.srv.URL+"/api/admin/app-plugins", "tr", map[string]any{})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	assert.Equal(t, "manifest_invalid", body["error"])
	assert.Equal(t, srvtext.Text("tr", "server.install.source_missing", nil), body["message"])
	assert.Contains(t, body["detail"], "github_repo")
}

// Every code and reason the second round says has its sentence in both
// shipped languages, filled and translated.
func TestErrorEnvelope_TheSecondRoundsSentencesAreSaidInBothLanguages(t *testing.T) {
	p := apierr.Params{"max": "500", "mode": "sideways", "received": "1024", "id": "7", "name": "nope", "capability": "open",
		"field": "timeout_s", "range": "1..300", "ext": "pdf", "placement": "open .pdf x", "place": "middle",
		"endpoint": "/api/admin/plugin-requests", "detail": "go's words"}
	for _, said := range []string{"internal_error", "bad_json", "bad_id", "bad_body", "bad_multipart", "bad_path", "bad_name",
		"paths_required", "too_many_paths", "bad_output_mode", "root_not_input", "params_too_large", "queue_unavailable",
		"app_plugins_disabled", "app_missing", "unauthenticated", "unauthorized", "too_large", "chunk_too_large",
		"spool_unavailable", "offset", "app_busy", "app_unavailable", "app_refused", "fingerprints_required",
		"store_nothing_to_remove", "bad_tenant", "bad_group", "store_screen_person", "app_store_disabled",
		"signing_unavailable", "ca_pair_required", "ca_invalid", "lock_place_required", "enabled_required",
		"auto_update_removed", "unknown_storage", "mixed_storages", "storage_required", "default_apps_demo",
		"default_apps_off", "bad_file_kind", "rule_shape", "open_or_thumbnail", "out_of_range", "rule_capability",
		"rule_kind", "rule_not_handler", "rule_cannot", "rule_unknown", "rule_placement", "rule_place",
		"plugin_session_required", "admin_session_required", "supertenant_only", "plugins_disabled",
		"storage_plugin_missing", "file_required", "plugin_source_missing", "request_too_large", "from_source_required",
		"plugin_patch_shape", "plugin_install_failed", "plugin_upgrade_failed", "plugin_change_failed"} {
		require.True(t, apierr.Known(said), said)
		en, tr := apierr.Text("en", said, p), apierr.Text("tr", said, p)
		assert.NotEmpty(t, en, said)
		assert.NotEqual(t, en, tr, "%s is not translated", said)
		assert.NotContains(t, en+tr, "{", "%s left a placeholder open", said)
	}
	for _, said := range []string{"manifest_missing", "manifest_unreadable", "module_unreadable", "ui_unreadable",
		"manifest_too_large", "from_source_install", "source_missing"} {
		en := srvtext.Text("en", "server.install."+said, srvtext.Vars{"max": "1 MB"})
		tr := srvtext.Text("tr", "server.install."+said, srvtext.Vars{"max": "1 MB"})
		assert.True(t, srvtext.Has("server.install."+said), said)
		assert.NotEqual(t, en, tr, "%s is not translated", said)
	}
}

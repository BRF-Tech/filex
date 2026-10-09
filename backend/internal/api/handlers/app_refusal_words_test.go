package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An app's doors refuse in the one envelope (docs/API-ERRORS.md): the code a
// program branches on stays (`permission_denied`, `encrypted`, `not_found`,
// `not_applicable`), and `message` is the server's sentence in the reader's
// language - the explorer, the panel, the CLI and an agent print it as it is.
//
// RED PROOF (int/055-wave b5c58508): every refusal below carried a sentence
// of Go's own in English for every reader ("insufficient permission:
// docs/a.txt", "plugins cannot read files in an encrypted folder: kasa/x.txt",
// "no such action", "this app does not save this kind of file", "an app
// cannot write into an encrypted folder"), or a bare path as its message.

// saidIn is the refusal's sentence of `said` in either shipped language (the
// fixture's administrator has no language of their own, so the instance
// default answers).
func saidIn(said string, p apierr.Params) []any {
	return []any{apierr.Text("en", said, p), apierr.Text("tr", said, p)}
}

func refusalOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &out), string(raw))
	return out
}

func TestAppRefusals_TheRunDoorSaysWhyInTheServersWords(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "x")
	run := f.srv.URL + "/api/files/plugins/actions/echo/upper/run"

	// An action that is not there: not_found, said - with Go's words as detail.
	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/nope/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"docs/a.txt"}})
	require.Equal(t, http.StatusNotFound, status, string(raw))
	body := refusalOf(t, raw)
	assert.Equal(t, "not_found", body["error"])
	assert.Contains(t, saidIn("action_unavailable", nil), body["message"], "%v", body)
	assert.Equal(t, "no such action", body["detail"], "Go's own words stay beside it, for a log")

	// A file that is not there: the path stays as a field, the sentence is said.
	status, raw = doReq(t, f.admin, http.MethodPost, run, map[string]any{"storage_id": f.st.ID, "paths": []string{"missing.txt"}})
	require.Equal(t, http.StatusNotFound, status, string(raw))
	body = refusalOf(t, raw)
	assert.Equal(t, "not_found", body["error"])
	assert.Contains(t, saidIn("path_missing", nil), body["message"], "%v", body)
	assert.Equal(t, "missing.txt", body["path"])

	// An encrypted folder: the server has no key, and says so with the name.
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/x.txt", "sealed")
	status, raw = doReq(t, f.admin, http.MethodPost, run, map[string]any{"storage_id": f.st.ID, "paths": []string{"kasa/x.txt"}})
	require.Equal(t, http.StatusForbidden, status, string(raw))
	body = refusalOf(t, raw)
	assert.Equal(t, "encrypted", body["error"])
	assert.Contains(t, saidIn("app_encrypted_read", apierr.Params{"name": "kasa/x.txt"}), body["message"], "%v", body)

	// A viewer and a writing action: permission_denied, naming the file.
	u := seedSharedUser(t, f.store, "viewer@test.local", "ViewerPass1!")
	grant(t, f.store, f.st, u, "", model.GrantViewer, true)
	client := freshClient(t)
	testutil.LoginAs(t, f.srv, client, "viewer@test.local", "ViewerPass1!")
	status, raw = doReq(t, client, http.MethodPost, run, map[string]any{"storage_id": f.st.ID, "paths": []string{"docs/a.txt"}})
	require.Equal(t, http.StatusForbidden, status, string(raw))
	body = refusalOf(t, raw)
	assert.Equal(t, "permission_denied", body["error"])
	assert.Contains(t, saidIn("app_input_denied", apierr.Params{"name": "docs/a.txt"}), body["message"], "%v", body)
}

func TestAppRefusals_TheInterfaceDoorsSayWhyInTheServersWords(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)

	// "Save as" of a kind the view does not open.
	code, out := f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://")+"&name=evil.html", "<script>")
	require.Equal(t, http.StatusUnprocessableEntity, code, out)
	body := refusalOf(t, []byte(out))
	assert.Equal(t, "not_applicable", body["error"])
	assert.Contains(t, saidIn("app_kind_unsaved", nil), body["message"], "%v", body)

	// "Save as" into an encrypted folder.
	f.markEncrypted(t, f.st, f.root, "kasa")
	code, out = f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://kasa")+"&name=plain.sketch", "plaintext")
	require.Equal(t, http.StatusForbidden, code, out)
	body = refusalOf(t, []byte(out))
	assert.Equal(t, "encrypted", body["error"])
	assert.Contains(t, saidIn("app_encrypted_write", nil), body["message"], "%v", body)

	// A screen the app does not have.
	code, out = f.uiSave(t, "sketch", "nosuchview", "path="+url.QueryEscape("main://doc.sketch"), "x")
	require.Equal(t, http.StatusNotFound, code, out)
	body = refusalOf(t, []byte(out))
	assert.Equal(t, "not_found", body["error"])
	assert.Contains(t, saidIn("app_screen_missing", nil), body["message"], "%v", body)
}

// A code a handler sends with another code's sentence (writeErrorSaid) has
// that sentence in both shipped languages, filled, and translated.
func TestErrorEnvelope_TheAppDoorsSentencesAreSaidInBothLanguages(t *testing.T) {
	p := apierr.Params{"name": "docs/a.txt", "ext": "pdf", "permission": "files:write"}
	for _, said := range []string{"outside_root", "permission_denied", "app_input_denied", "app_folder_denied",
		"app_save_denied", "app_no_lookup", "action_admin_only", "action_unavailable", "action_from_app",
		"app_not_running", "app_screen_missing", "save_gone", "app_encrypted_read", "app_encrypted_write",
		"app_encrypted_file_read", "app_encrypted_file_write",
		"handler_off", "not_granted", "folder_not_offered", "bad_folder", "app_kind_unsaved", "save_failed",
		"storage_unavailable", "store_screen_hidden", "store_unknown", "store_app_unknown", "demo_store", "lock_none"} {
		require.True(t, apierr.Known(said), said)
		en, tr := apierr.Text("en", said, p), apierr.Text("tr", said, p)
		assert.NotEmpty(t, en, said)
		assert.NotEmpty(t, tr, said)
		assert.NotEqual(t, en, tr, "%s is not translated", said)
		assert.NotContains(t, en, "{", "%s left a placeholder open: %s", said, en)
		assert.NotContains(t, tr, "{", "%s left a placeholder open: %s", said, tr)
	}
	assert.Contains(t, apierr.Text("tr", "handler_off", p), ".pdf")
	assert.Contains(t, apierr.Text("en", "app_input_denied", p), "docs/a.txt")
}

package handlers_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
)

// sec055 S16 follow-up: a single encrypted file (`.fxe`) is ciphertext the
// server has no key to, as a file in an encrypted folder is, and every app
// door refuses it the same way - an action's run, a screen's event, an
// interface's call, read and save over it - by the rule every file row's
// `encrypted` says (encryptedAtDoor). RED PROOF (int/055-wave 5de220be): the
// doors asked e2e.UnderEncrypted alone, so the run answered 422
// not_applicable, the event and the call went to the app, and the save over
// it answered 422 - none of them `403 encrypted`.
func TestAppDoors_ASingleEncryptedFileIsRefusedAtEveryDoor(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.installUIApp(t)
	f.writeFile(t, "rapor.txt.fxe", "filexfxe-sealed-1")
	f.writeFile(t, "doc.sketch.fxe", "filexfxe-sealed-2")
	f.writeFile(t, "docs/a.txt", "plain")

	refused := func(label string, status int, raw []byte, said, name string) {
		t.Helper()
		require.Equal(t, http.StatusForbidden, status, "%s: %s", label, raw)
		body := refusalOf(t, raw)
		assert.Equal(t, "encrypted", body["error"], "%s: the code every app door answers", label)
		assert.Contains(t, saidIn(said, apierr.Params{"name": name}), body["message"], "%s: %v", label, body)
		assert.NotContains(t, string(raw), "filexfxe-sealed", "%s: no byte of it", label)
	}

	status, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"rapor.txt.fxe"}})
	refused("an action's run", status, raw, "app_encrypted_file_read", "rapor.txt.fxe")

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/views/echo/hello/event",
		map[string]any{"event": "change", "paths": []string{"main://rapor.txt.fxe"}})
	refused("a screen's event", status, raw, "app_encrypted_file_read", "rapor.txt.fxe")

	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/ui/sketch/editor/call",
		map[string]any{"method": "x", "paths": []string{"main://doc.sketch.fxe"}})
	refused("an interface's call", status, raw, "app_encrypted_file_read", "doc.sketch.fxe")

	status, raw = doReq(t, f.admin, http.MethodGet,
		f.srv.URL+"/api/files/plugins/ui/sketch/editor/read?path="+url.QueryEscape("main://doc.sketch.fxe"), nil)
	refused("an interface's read", status, raw, "app_encrypted_file_read", "doc.sketch.fxe")

	code, out := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch.fxe"), "plaintext over ciphertext")
	refused("an interface's save over it", code, []byte(out), "app_encrypted_file_write", "doc.sketch.fxe")
	assert.Equal(t, "filexfxe-sealed-2", f.readFile(t, "doc.sketch.fxe"), "nothing was written over it")

	// A plain file still goes through the same door.
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/plugins/actions/echo/upper/run",
		map[string]any{"storage_id": f.st.ID, "paths": []string{"docs/a.txt"}})
	assert.NotEqual(t, http.StatusForbidden, status, "a plain file: %s", raw)
}

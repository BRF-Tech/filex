package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// ── An app's interface editing a file of an encrypted folder ─────────────
//
// filex 0.56.0, task #189 (app_ui_e2e.go). The explorer holds the folder's
// key: with `e2e=1` the read door hands a granted app's frame host the
// CIPHERTEXT, and the save door takes a body the browser encrypted. The
// server never sees the plaintext, records every handover and save, and
// refuses what it can judge without the key. All red before 0.56: `e2e=1`
// meant nothing, both doors answered `403 encrypted` to every app.

const e2eAppManifest = `{"manifest_version":1,"name":"sketch","version":"1.0.0","label":{"en":"Sketch"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},"encrypted_folders":{"open":true},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Sketch"},"applies":{"ext":["sketch"]}}]}`

// installE2EApp installs the interface-only app that asks for encrypted
// folders, granted what it asks.
func (f *appFixture) installE2EApp(t *testing.T) {
	t.Helper()
	_, _, err := f.reg.Install(t.Context(), &wasmplugin.InstallInput{
		Manifest: []byte(e2eAppManifest), UI: bytes.NewReader(uiBundleZip(t)),
		Granted: []string{"files:read", "files:write", "ui", "ui-viewer:.sketch", "files:e2e-plaintext"},
	})
	require.NoError(t, err)
}

func (f *appFixture) uiRead(t *testing.T, query string) (int, string) {
	t.Helper()
	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/ui/sketch/editor/read?" + query)
	require.NoError(t, err)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (f *appFixture) e2eAudit(t *testing.T, action string) []map[string]any {
	t.Helper()
	entries, err := f.store.ListAuditRecent(context.Background(), 200)
	require.NoError(t, err)
	var out []map[string]any
	for _, e := range entries {
		if e.Action == action && e.TargetID == "sketch" {
			out = append(out, e.Metadata)
		}
	}
	return out
}

func e2eErrCode(t *testing.T, body string) (string, string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m), body)
	code, _ := m["error"].(string)
	msg, _ := m["message"].(string)
	return code, msg
}

// statSig is the `expect` of a file as the storage holds it (size, ms).
func (f *appFixture) statSig(t *testing.T, rel string) string {
	t.Helper()
	fi, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixMilli())
}

const sketchCiphertext = "filexe2e\x01" + "ciphertext-the-server-holds"

func TestAppUIE2E_TheReadHandsAGrantedAppTheCiphertextAndRecordsIt(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", sketchCiphertext)

	code, body := f.uiRead(t, "path="+url.QueryEscape("main://kasa/doc.sketch")+"&e2e=1")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, sketchCiphertext, body, "the bytes the storage holds: the browser decrypts them")
	rows := f.e2eAudit(t, "app_plugin.e2e_read")
	require.Len(t, rows, 1, "one handover, one row")
	assert.Equal(t, "kasa/doc.sketch", rows[0]["path"])
	assert.Equal(t, "editor", rows[0]["view"])

	// Without e2e=1 nothing changed: 0.55's refusal.
	code, body = f.uiRead(t, "path="+url.QueryEscape("main://kasa/doc.sketch"))
	require.Equal(t, http.StatusForbidden, code, body)
	c, _ := e2eErrCode(t, body)
	assert.Equal(t, "encrypted", c)
	assert.NotContains(t, body, "ciphertext-the-server-holds")

	// A plain file reads as it always did, and nothing is recorded for it.
	code, body = f.uiRead(t, "path="+url.QueryEscape("main://doc.sketch")+"&e2e=1")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "v1", body)
	assert.Len(t, f.e2eAudit(t, "app_plugin.e2e_read"), 1)
}

func TestAppUIE2E_TheReadRefusesWhatItCannotVouchFor(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t) // asks for nothing in encrypted folders
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", sketchCiphertext)
	f.writeFile(t, "tek.sketch.fxe", "filexfxe-ciphertext-on-its-own")

	code, body := f.uiRead(t, "path="+url.QueryEscape("main://kasa/doc.sketch")+"&e2e=1")
	require.Equal(t, http.StatusForbidden, code, body)
	c, msg := e2eErrCode(t, body)
	assert.Equal(t, "not_granted", c)
	assert.Contains(t, msg, "files:e2e-plaintext")
	assert.NotContains(t, body, "ciphertext-the-server-holds")
	assert.Empty(t, f.e2eAudit(t, "app_plugin.e2e_read"))
}

func TestAppUIE2E_TheAdministratorsSwitchClosesBothDoors(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t)
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", sketchCiphertext)
	f.writeFile(t, "tek.sketch.fxe", "filexfxe-ciphertext-on-its-own")
	require.NoError(t, f.store.UpsertSetting(t.Context(), wasmplugin.E2EPlaintextSetting.Key, "false"))

	code, body := f.uiRead(t, "path="+url.QueryEscape("main://kasa/doc.sketch")+"&e2e=1")
	require.Equal(t, http.StatusForbidden, code, body)
	c, msg := e2eErrCode(t, body)
	assert.Equal(t, "encrypted", c)
	assert.Contains(t, msg, "an administrator turned it off")

	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://kasa/doc.sketch")+"&e2e=1", "filexe2e\x01new")
	require.Equal(t, http.StatusForbidden, code, body)
	assert.Equal(t, sketchCiphertext, f.readFile(t, "kasa/doc.sketch"))

	// The capabilities say it, so the explorer offers no app there.
	res, err := f.admin.Get(f.srv.URL + "/api/capabilities")
	require.NoError(t, err)
	var caps map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&caps))
	res.Body.Close()
	assert.Equal(t, false, caps["apps_e2e_plaintext"])

	// A single encrypted file is refused whatever the switch says.
	require.NoError(t, f.store.UpsertSetting(t.Context(), wasmplugin.E2EPlaintextSetting.Key, "true"))
	code, body = f.uiRead(t, "path="+url.QueryEscape("main://tek.sketch.fxe")+"&e2e=1")
	require.Equal(t, http.StatusForbidden, code, body)
	c, _ = e2eErrCode(t, body)
	assert.Equal(t, "encrypted", c)
	res, err = f.admin.Get(f.srv.URL + "/api/capabilities")
	require.NoError(t, err)
	caps = map[string]any{}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&caps))
	res.Body.Close()
	assert.Equal(t, true, caps["apps_e2e_plaintext"], "on unless turned off")
}

func TestAppUIE2E_TheSaveTakesOnlyCiphertextOverTheFileAsItWasOpened(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t)
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", sketchCiphertext)
	target := "path=" + url.QueryEscape("main://kasa/doc.sketch")

	// Plaintext is never stored in an encrypted folder through this door.
	code, body := f.uiSave(t, "sketch", "editor", target+"&e2e=1", "PK\x03\x04a document in the clear")
	require.Equal(t, http.StatusUnprocessableEntity, code, body)
	c, msg := e2eErrCode(t, body)
	assert.Equal(t, "not_encrypted", c)
	assert.Contains(t, msg, "kasa/doc.sketch")
	assert.Equal(t, sketchCiphertext, f.readFile(t, "kasa/doc.sketch"))

	// Without e2e=1: 0.55's refusal.
	code, body = f.uiSave(t, "sketch", "editor", target, "filexe2e\x01new")
	require.Equal(t, http.StatusForbidden, code, body)
	c, _ = e2eErrCode(t, body)
	assert.Equal(t, "encrypted", c)

	// Over another version than the one the app opened: refused, untouched.
	code, body = f.uiSave(t, "sketch", "editor", target+"&e2e=1&expect=3:1000", "filexe2e\x01new")
	require.Equal(t, http.StatusPreconditionFailed, code, body)
	c, msg = e2eErrCode(t, body)
	assert.Equal(t, "changed", c)
	assert.Contains(t, msg, "kasa/doc.sketch")
	assert.Equal(t, sketchCiphertext, f.readFile(t, "kasa/doc.sketch"))

	// Over the file as it was opened: a new version, recorded, with the
	// listing's date for the next save's `expect`.
	next := "filexe2e\x01" + "the-second-ciphertext"
	code, body = f.uiSave(t, "sketch", "editor", target+"&e2e=1&expect="+url.QueryEscape(f.statSig(t, "kasa/doc.sketch")), next)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, next, f.readFile(t, "kasa/doc.sketch"))
	var ans map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &ans))
	assert.EqualValues(t, len(next), ans["size"])
	modified, ok := ans["modified"].(float64)
	require.True(t, ok, "the answer names the version's date: %s", body)
	rows := f.e2eAudit(t, "app_plugin.e2e_save")
	require.Len(t, rows, 1)
	assert.Equal(t, "kasa/doc.sketch", rows[0]["path"])
	assert.EqualValues(t, len(next), rows[0]["size"])

	// The answer's size and date are the next save's `expect`.
	third := "filexe2e\x02" + "a-streamed-ciphertext"
	code, body = f.uiSave(t, "sketch", "editor", target+"&e2e=1&expect="+url.QueryEscape(fmt.Sprintf("%d:%d", len(next), int64(modified))), third)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, third, f.readFile(t, "kasa/doc.sketch"))
	assert.Len(t, f.e2eAudit(t, "app_plugin.e2e_save"), 2)
}

// A save in chunks names the door it was started for in every chunk.
func TestAppUIE2E_AChunkedSaveKeepsItsDoor(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t)
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", sketchCiphertext)
	target := "path=" + url.QueryEscape("main://kasa/doc.sketch")

	code, body := f.uiSave(t, "sketch", "editor", target+"&e2e=1&chunk=start", "filexe2e\x01first-")
	require.Equal(t, http.StatusAccepted, code, body)
	var started struct {
		Session  string `json:"session"`
		Received int64  `json:"received"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &started))
	rest := fmt.Sprintf("&session=%s&offset=%d&final=1", started.Session, started.Received)

	code, body = f.uiSave(t, "sketch", "editor", target+rest, "second")
	require.Equal(t, http.StatusNotFound, code, "a chunk without the door is not this save's: %s", body)
	code, body = f.uiSave(t, "sketch", "editor", target+"&e2e=1"+rest, "second")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "filexe2e\x01first-second", f.readFile(t, "kasa/doc.sketch"))
}

// The install review lists the derived grant with its own sentence.
func TestAppUIE2E_TheReviewSaysWhatTheGrantMeans(t *testing.T) {
	f := newAppFixture(t, nil)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write([]byte(e2eAppManifest))
	uf, _ := w.CreateFormFile("ui", "ui.zip")
	_, _ = uf.Write(uiBundleZip(t))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins?dry_run=1", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, string(raw))
	var dry struct {
		Permissions []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(raw, &dry))
	found := false
	for _, p := range dry.Permissions {
		if p.ID == "files:e2e-plaintext" {
			found = true
			assert.Contains(t, []string{wasmplugin.PermFilesE2EPlaintext.Label("en"), wasmplugin.PermFilesE2EPlaintext.Label("tr")}, p.Label,
				"the reader's sentence, never the id")
		}
	}
	assert.True(t, found, "the review lists files:e2e-plaintext: %s", raw)
}

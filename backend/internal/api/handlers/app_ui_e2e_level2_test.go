package handlers_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// ── A file of a folder whose NAMES are encrypted (level 2) ───────────────
//
// filex 0.56, task #189 (app_ui_e2e.go, the header's level-2 paragraph). The
// stored name of a file there is ciphertext: the server cannot tell its kind,
// so the kind is the browser's decision (it decrypts the name and picks the
// app by its extension; neither is ever sent). The doors still decide the
// grant, the folder, the person's rights and ciphertext only - and hold every
// name they CAN read to its kind, as at level 1.
//
// Red before: the save door asked the view's kinds of the stored name
// (viewSaves) and answered `422 not_applicable` to every save in a level-2
// folder.

// A stored name as the browser writes it in a level-2 folder (lib/e2enames:
// base64url, no dot - e2e testdata/name-vectors.json), and a long one.
const (
	level2Name     = "ugkmCcOceKUyA7tsdZxzltk"
	level2LongName = "21d4xY_T-0Cos4uxsBA6kXQ1j4JOw8-KedEwgAy-NO0.fxl"
)

// markNamesEncrypted turns an encrypted folder made by markEncrypted into a
// level-2 one: its key file says the names are encrypted (v3, req names, a
// names slot), as the browser writes it.
func (f *appFixture) markNamesEncrypted(t *testing.T, dir string) {
	t.Helper()
	marker := `{"v":3,"req":["names"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=",` +
		`"names":{"alg":"AES-SIV-512","enc":"base64url","long":220,"key":"a2V5","root_id":"cm9vdGlkcm9vdGlkcm9vdA"}}`
	require.NoError(t, os.WriteFile(filepath.Join(f.root, dir, ".filex-e2e.json"), []byte(marker), 0o644))
}

// uiSaveAs PUTs a save through another person's session.
func (f *appFixture) uiSaveAs(t *testing.T, c *http.Client, query, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, f.srv.URL+"/api/files/plugins/ui/sketch/editor/save?"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(raw)
}

func TestAppUIE2E_ALevel2FileIsNotHeldToAKindTheServerCannotRead(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t) // a viewer of .sketch
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.markNamesEncrypted(t, "kasa")
	rel := "kasa/" + level2Name
	f.writeFile(t, rel, sketchCiphertext)
	target := "path=" + url.QueryEscape("main://"+rel)

	// The read: the ciphertext, recorded - no kind asked of the stored name.
	code, body := f.uiRead(t, target+"&e2e=1")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, sketchCiphertext, body)
	rows := f.e2eAudit(t, "app_plugin.e2e_read")
	require.Len(t, rows, 1)
	assert.Equal(t, rel, rows[0]["path"])

	// The save: ciphertext over the file as it was opened, though the stored
	// name says no `.sketch` - the browser chose the app by the decrypted name.
	next := "filexe2e\x01" + "the-level-2-ciphertext"
	code, body = f.uiSave(t, "sketch", "editor", target+"&e2e=1&expect="+url.QueryEscape(f.statSig(t, rel)), next)
	require.Equal(t, http.StatusOK, code, "level 2: the server cannot read the kind: %s", body)
	assert.Equal(t, next, f.readFile(t, rel))
	require.Len(t, f.e2eAudit(t, "app_plugin.e2e_save"), 1)

	// Everything else it decides as before: ciphertext only ...
	code, body = f.uiSave(t, "sketch", "editor", target+"&e2e=1", "PK\x03\x04a document in the clear")
	require.Equal(t, http.StatusUnprocessableEntity, code, body)
	c, _ := e2eErrCode(t, body)
	assert.Equal(t, "not_encrypted", c)
	assert.Equal(t, next, f.readFile(t, rel))

	// ... and, without the door, 0.55's refusal.
	code, body = f.uiSave(t, "sketch", "editor", target, "filexe2e\x01other")
	require.Equal(t, http.StatusForbidden, code, body)
	c, _ = e2eErrCode(t, body)
	assert.Equal(t, "encrypted", c)

	// A shortened name (`<hash>.fxl`) is a stored name too.
	long := "kasa/" + level2LongName
	f.writeFile(t, long, sketchCiphertext)
	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://"+long)+"&e2e=1", "filexe2e\x02streamed")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "filexe2e\x02streamed", f.readFile(t, long))
}

// The server still holds every name it CAN read to its kind: a level-1
// folder (whatever its names look like), and a name written in the clear
// into a level-2 folder.
func TestAppUIE2E_AReadableNameIsStillHeldToItsKind(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t)

	// Level 1: the key file says nothing of names, so a name that merely
	// LOOKS like ciphertext is a name the server reads - and it is no .sketch.
	f.markEncrypted(t, f.st, f.root, "plain")
	f.writeFile(t, "plain/"+level2Name, sketchCiphertext)
	code, body := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://plain/"+level2Name)+"&e2e=1", "filexe2e\x01new")
	require.Equal(t, http.StatusUnprocessableEntity, code, body)
	c, _ := e2eErrCode(t, body)
	assert.Equal(t, "not_applicable", c)
	assert.Equal(t, sketchCiphertext, f.readFile(t, "plain/"+level2Name))

	// Level 2, a readable name (written over WebDAV, or before a names pass
	// reached it): its kind is .txt, not the view's.
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.markNamesEncrypted(t, "kasa")
	f.writeFile(t, "kasa/notes.txt", sketchCiphertext)
	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://kasa/notes.txt")+"&e2e=1", "filexe2e\x01new")
	require.Equal(t, http.StatusUnprocessableEntity, code, body)
	c, _ = e2eErrCode(t, body)
	assert.Equal(t, "not_applicable", c)
	assert.Equal(t, sketchCiphertext, f.readFile(t, "kasa/notes.txt"))
}

// Level 2's save is the grant, the folder and the person's WRITE right: a
// person who may only look at the folder is refused, one who may edit it
// saves.
func TestAppUIE2E_ALevel2SaveStillNeedsThePersonsWriteRight(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installE2EApp(t)
	f.markEncrypted(t, f.st, f.root, "docs")
	f.markNamesEncrypted(t, "docs")
	rel := "docs/" + level2Name
	f.writeFile(t, rel, sketchCiphertext)
	target := "path=" + url.QueryEscape("main://"+rel) + "&e2e=1"

	viewer, _, _ := f.loginRegular(t, "ada", model.GrantViewer)
	code, body := f.uiSaveAs(t, viewer, target, "filexe2e\x01by-a-viewer")
	require.Equal(t, http.StatusForbidden, code, body)
	assert.Equal(t, sketchCiphertext, f.readFile(t, rel))

	editor, _, _ := f.loginRegular(t, "grace", "editor")
	code, body = f.uiSaveAs(t, editor, target, "filexe2e\x01by-an-editor")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "filexe2e\x01by-an-editor", f.readFile(t, rel))
}

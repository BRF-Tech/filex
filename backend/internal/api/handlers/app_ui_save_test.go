package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// ── An app's own interface saving (M2's server half) ─────────────────────

// uiSave PUTs a save through the admin's session.
func (f *appFixture) uiSave(t *testing.T, plugin, view, query, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, f.srv.URL+"/api/files/plugins/ui/"+plugin+"/"+view+"/save?"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(raw)
}

func (f *appFixture) readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(b)
}

// A save from an interface is judged here again, whatever the frame decided:
// the grant, the kind of file the view opens, the person's right to write it,
// the storage, somebody else's draft.
func TestAppUI_SaveIsCheckedOnTheServer(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	f.writeFile(t, "notes.txt", "keep me")

	code, body := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), "v2")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "v2", f.readFile(t, "doc.sketch"))
	assert.Contains(t, body, `"path":"main://doc.sketch"`)

	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://notes.txt"), "overwritten")
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	assert.Equal(t, "keep me", f.readFile(t, "notes.txt"), "a diagram editor never writes a text file")

	code, _ = f.uiSave(t, "sketch", "nope", "path="+url.QueryEscape("main://doc.sketch"), "x")
	assert.Equal(t, http.StatusNotFound, code)

	// Somebody else's draft is not the admin's to write, administrator or not.
	f.writeFile(t, ".filex-drafts/999/abcdef12/other.sketch", "theirs")
	code, body = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://.filex-drafts/999/abcdef12/other.sketch"), "mine")
	assert.NotEqual(t, http.StatusOK, code, body)
	assert.Equal(t, "theirs", f.readFile(t, ".filex-drafts/999/abcdef12/other.sketch"))

	// Save as: a NEW file beside what is there, never over it.
	code, body = f.uiSave(t, "sketch", "editor", "dir="+url.QueryEscape("main://")+"&name=doc.sketch", "copy")
	require.Equal(t, http.StatusCreated, code, body)
	assert.Equal(t, "v2", f.readFile(t, "doc.sketch"), "the existing file is untouched")
	assert.NotContains(t, body, `"path":"main://doc.sketch"`)

	// No session: no save.
	req, _ := http.NewRequest(http.MethodPut, f.srv.URL+"/api/files/plugins/ui/sketch/editor/save?path="+url.QueryEscape("main://doc.sketch"), strings.NewReader("anon"))
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	assert.Equal(t, "v2", f.readFile(t, "doc.sketch"))

	// A read-only storage takes no save.
	st, err := f.store.GetStorage(t.Context(), f.st.ID)
	require.NoError(t, err)
	st.ReadOnly = true
	require.NoError(t, f.store.UpdateStorage(t.Context(), st))
	code, _ = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), "v3")
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "v2", f.readFile(t, "doc.sketch"))
}

func TestAppUI_AnAppWithoutFilesWriteCannotSave(t *testing.T) {
	f := newAppFixture(t, nil)
	bundle := uiBundleZip(t)
	man := strings.Replace(uiOnlyManifest, `"permissions":["files:read","files:write"]`, `"permissions":["files:read"]`, 1)
	_, _, err := f.reg.Install(t.Context(), &wasmplugin.InstallInput{Manifest: []byte(man), UI: bytes.NewReader(bundle), Granted: []string{"files:read", "ui", "ui-viewer:.sketch"}})
	require.NoError(t, err)
	f.writeFile(t, "doc.sketch", "v1")
	code, body := f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://doc.sketch"), "v2")
	assert.Equal(t, http.StatusForbidden, code, body)
	assert.Contains(t, body, "not_granted")
	assert.Equal(t, "v1", f.readFile(t, "doc.sketch"))
}

// A large save goes in chunks, each under a proxy's body limit, and is
// written once at the end; a session belongs to the save it was started for.
func TestAppUI_AChunkedSaveIsWrittenOnceAtTheEnd(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "big.sketch", "old")
	target := "path=" + url.QueryEscape("main://big.sketch")

	code, body := f.uiSave(t, "sketch", "editor", target+"&chunk=start", "one-")
	require.Equal(t, http.StatusAccepted, code, body)
	var st struct {
		Session  string `json:"session"`
		Received int64  `json:"received"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &st))
	require.NotEmpty(t, st.Session)
	assert.EqualValues(t, 4, st.Received)
	assert.Equal(t, "old", f.readFile(t, "big.sketch"), "nothing is written before the last chunk")

	code, body = f.uiSave(t, "sketch", "editor", target+"&session="+st.Session+"&offset=0", "again")
	assert.Equal(t, http.StatusConflict, code, "a chunk at the wrong offset is refused: %s", body)

	code, _ = f.uiSave(t, "sketch", "editor", "path="+url.QueryEscape("main://other.sketch")+"&session="+st.Session+"&offset=4", "x")
	assert.Equal(t, http.StatusNotFound, code, "the session belongs to the file it was started for")

	code, body = f.uiSave(t, "sketch", "editor", target+"&session="+st.Session+"&offset=4", "two-")
	require.Equal(t, http.StatusAccepted, code, body)
	code, body = f.uiSave(t, "sketch", "editor", target+"&session="+st.Session+"&offset=8&final=1", "three")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "one-two-three", f.readFile(t, "big.sketch"))

	code, _ = f.uiSave(t, "sketch", "editor", target+"&session="+st.Session+"&offset=13&final=1", "more")
	assert.Equal(t, http.StatusNotFound, code, "a finished session is gone")
}

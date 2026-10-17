package handlers_test

// An app's save that does not go over the file it opened (app_ui_beside.go,
// filex 0.57, task #268): a save in a format the file cannot hold is written
// beside it, a save of a file that changed meanwhile becomes a conflict copy
// when the app asks for one - by the writer the Document Server's callback
// uses (internal/officesave write.go).
//
// ⚠ On 0.56 every save here was written over the opened file: a DOCX under
// the name rapor.doc, the other person's change gone unseen, or - without a
// conflict copy asked for - refused with 412, which an app built for 0.56
// still gets.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// officeAppManifest is an interface-only office editor named like the
// sketch app the editing-together helpers talk to (f.co, f.join): its
// editor opens legacy and OOXML documents and edits them together.
const officeAppManifest = `{"manifest_version":1,"name":"sketch","version":"1.0.0","label":{"en":"Office"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},"co_edit":{"open":true},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Office"},"applies":{"ext":["doc","docx","xls","xlsx"]}}]}`

func (f *appFixture) installOfficeApp(t *testing.T) {
	t.Helper()
	_, _, err := f.reg.Install(context.Background(), &wasmplugin.InstallInput{
		Manifest: []byte(officeAppManifest), UI: bytes.NewReader(uiBundleZip(t)), Source: "upload",
		Granted: []string{"files:read", "files:write", "files:co-edit", "ui",
			"ui-viewer:.doc", "ui-viewer:.docx", "ui-viewer:.xls", "ui-viewer:.xlsx"},
	})
	require.NoError(t, err)
}

// docxBytes is a save the office editor writes: a zip package.
func docxBytes(s string) string { return "PK\x03\x04" + s }

// officeSave is a save over main://<rel> through the office app, the admin's
// or c's, with q's options; its answer as a map.
func (f *appFixture) officeSave(t *testing.T, c *http.Client, rel string, q url.Values, body string) (int, map[string]any) {
	t.Helper()
	if q == nil {
		q = url.Values{}
	}
	q.Set("path", "main://"+rel)
	if c == nil {
		c = f.admin
	}
	code, raw := f.uiSaveAs(t, c, q.Encode(), body)
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &out)
	if len(out) == 0 {
		out["raw"] = raw
	}
	return code, out
}

// savedElsewhere is somebody else's save of rel through filex meanwhile:
// written, and its catalogue row moved on with it, as any save's is.
func (f *appFixture) savedElsewhere(t *testing.T, rel string) {
	t.Helper()
	code, out := f.officeSave(t, nil, rel, nil, "v2, written by somebody else")
	require.Equal(t, http.StatusOK, code, out)
}

func (f *appFixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	return err == nil
}

var conflictNameRe = regexp.MustCompile(`^teklif\.filex-conflict-\d{8}T\d{6}\.docx$`)

func TestAppUIBeside_ASaveInAnotherFormatIsWrittenBesideTheFile(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/rapor.doc", "a Word 97 file")

	code, out := f.officeSave(t, nil, "docs/rapor.doc", url.Values{"beside": {"docx"}}, docxBytes("one"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "rapor.docx", out["name"])
	assert.Equal(t, true, out["beside"])
	assert.Equal(t, "main://docs/rapor.docx", out["path"])
	assert.Equal(t, "a Word 97 file", f.readFile(t, "docs/rapor.doc"), "the file itself is left as it was")
	assert.Equal(t, docxBytes("one"), f.readFile(t, "docs/rapor.docx"))

	// The frame saves the same opening over the name it was given: an
	// ordinary save there, the same extension as the file's own.
	code, out = f.officeSave(t, nil, "docs/rapor.docx", url.Values{"beside": {"docx"}}, docxBytes("two"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "rapor.docx", out["name"])
	assert.Nil(t, out["beside"], "written over the file it names")
	assert.Equal(t, docxBytes("two"), f.readFile(t, "docs/rapor.docx"))

	// Another opening of rapor.doc finds the name taken: the next free one.
	code, out = f.officeSave(t, nil, "docs/rapor.doc", url.Values{"beside": {"docx"}}, docxBytes("three"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "rapor (2).docx", out["name"])
	assert.Equal(t, docxBytes("two"), f.readFile(t, "docs/rapor.docx"), "never over another save")

	// The same audit row as a Document Server's save beside, the app's action.
	entries, err := f.store.ListAuditRecent(context.Background(), 200)
	require.NoError(t, err)
	var rows int
	for _, e := range entries {
		if e.Action == "file.app_saved_beside" {
			rows++
			assert.Equal(t, "docs/rapor.doc", e.Metadata["original"])
		}
	}
	assert.Equal(t, 2, rows)
}

func TestAppUIBeside_TheOptionsAreChecked(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/rapor.doc", "a Word 97 file")

	for _, q := range []url.Values{{"beside": {"exe"}}, {"beside": {"html"}}, {"conflict": {"yes"}}} {
		code, out := f.officeSave(t, nil, "docs/rapor.doc", q, docxBytes("x"))
		assert.Equal(t, http.StatusBadRequest, code, "%v %v", q, out)
		assert.Contains(t, saidIn("app_save_option", nil), out["message"], "%v", out)
	}
	// Bytes that are not the package the format is: nothing is written.
	code, out := f.officeSave(t, nil, "docs/rapor.doc", url.Values{"beside": {"docx"}}, "plain text")
	assert.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Contains(t, saidIn("app_save_beside_bytes", apierr.Params{"name": "docs/rapor.doc", "format": "DOCX"}), out["message"], "%v", out)
	assert.False(t, f.exists("docs/rapor.docx"))
	assert.Equal(t, "a Word 97 file", f.readFile(t, "docs/rapor.doc"))
}

// A file of an encrypted folder is saved in its own format only: the
// server holds its ciphertext and cannot name a file beside it.
func TestAppUIBeside_NotInAnEncryptedFolder(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/rapor.doc", sketchCiphertext)

	code, out := f.officeSave(t, nil, "kasa/rapor.doc", url.Values{"beside": {"docx"}, "e2e": {"1"}}, sketchCiphertext)
	assert.Contains(t, []int{http.StatusUnprocessableEntity, http.StatusForbidden}, code, out)
	assert.False(t, f.exists("kasa/rapor.docx"))
	assert.Equal(t, sketchCiphertext, f.readFile(t, "kasa/rapor.doc"))
}

// The creator rule: a person who may change the file but not create files in
// its folder gets no file beside it - and is told why.
func TestAppUIBeside_OnlyForAPersonWhoMayCreateFilesThere(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/rapor.doc", "a Word 97 file")
	email, pw := "erin@example.com", "TestUserPass!1"
	testutil.SeedRegularUser(t, f.store, email, pw)
	u, err := f.store.GetUserByEmail(context.Background(), email)
	require.NoError(t, err)
	_, err = f.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: f.st.ID, PathPrefix: "docs/rapor.doc", IsDir: false, UserID: u.ID, Level: "editor",
	})
	require.NoError(t, err)
	c := freshClient(t)
	testutil.LoginAs(t, f.srv, c, email, pw)

	// The premise: an ordinary save over the file is hers to make.
	code, out := f.officeSave(t, c, "docs/rapor.doc", nil, "her own edit")
	require.Equal(t, http.StatusOK, code, "the grant lets her change the file: %v", out)

	code, out = f.officeSave(t, c, "docs/rapor.doc", url.Values{"beside": {"docx"}}, docxBytes("x"))
	assert.Equal(t, http.StatusForbidden, code, out)
	assert.Equal(t, "permission_denied", out["error"])
	assert.Contains(t, saidIn("app_save_beside_denied", apierr.Params{"name": "docs/rapor.doc", "format": "DOCX"}), out["message"], "%v", out)
	assert.False(t, f.exists("docs/rapor.docx"))
}

// readVersion is the interface's read of main://<rel> and the version its
// answer names (X-Filex-Version).
func (f *appFixture) readVersion(t *testing.T, rel string) string {
	t.Helper()
	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/ui/sketch/editor/read?path=" + url.QueryEscape("main://"+rel))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	return res.Header.Get("X-Filex-Version")
}

func TestAppUIBeside_TheReadNamesTheVersionItHandsOver(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/teklif.docx", "v1")
	assert.Equal(t, f.statSig(t, "docs/teklif.docx"), f.readVersion(t, "docs/teklif.docx"))

	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/ui/sketch/editor/read?path=" + url.QueryEscape("main://docs/missing.docx"))
	require.NoError(t, err)
	res.Body.Close()
	assert.Empty(t, res.Header.Get("X-Filex-Version"), "never on a refusal")
}

// An app built for 0.56 asks for no conflict copy: a file that changed since
// it read it is refused, as before, and nothing is written.
func TestAppUIBeside_WithoutTheOptionAChangedFileIsStillRefused(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/teklif.docx", "v1")
	read := f.readVersion(t, "docs/teklif.docx")
	f.savedElsewhere(t, "docs/teklif.docx")

	code, out := f.officeSave(t, nil, "docs/teklif.docx", url.Values{"expect": {read}}, docxBytes("mine"))
	assert.Equal(t, http.StatusPreconditionFailed, code, out)
	assert.Contains(t, saidIn("app_save_changed", apierr.Params{"name": "docs/teklif.docx"}), out["message"], "%v", out)
	assert.Equal(t, "v2, written by somebody else", f.readFile(t, "docs/teklif.docx"))
}

func TestAppUIBeside_AChangedFileGetsAConflictCopyWhenTheAppAsks(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/teklif.docx", "v1")
	read := f.readVersion(t, "docs/teklif.docx")
	f.savedElsewhere(t, "docs/teklif.docx")

	code, out := f.officeSave(t, nil, "docs/teklif.docx", url.Values{"expect": {read}, "conflict": {"copy"}}, docxBytes("mine"))
	require.Equal(t, http.StatusOK, code, out)
	name, _ := out["conflict"].(string)
	assert.Regexp(t, conflictNameRe, name)
	assert.Equal(t, name, out["name"])
	assert.Equal(t, "v2, written by somebody else", f.readFile(t, "docs/teklif.docx"), "the other change is kept")
	assert.Equal(t, docxBytes("mine"), f.readFile(t, "docs/"+name))

	// The file as it was read: an ordinary save, no copy.
	read = f.readVersion(t, "docs/teklif.docx")
	code, out = f.officeSave(t, nil, "docs/teklif.docx", url.Values{"expect": {read}, "conflict": {"copy"}}, docxBytes("again"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Nil(t, out["conflict"])
	assert.Equal(t, docxBytes("again"), f.readFile(t, "docs/teklif.docx"))
}

// A legacy file that changed meanwhile: the conflict copy is in the format
// the save is in.
func TestAppUIBeside_AConflictCopyOfALegacyFileIsInTheSavesFormat(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/rapor.doc", "v1")
	read := f.readVersion(t, "docs/rapor.doc")
	f.savedElsewhere(t, "docs/rapor.doc")

	code, out := f.officeSave(t, nil, "docs/rapor.doc", url.Values{"expect": {read}, "conflict": {"copy"}, "beside": {"docx"}}, docxBytes("mine"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Regexp(t, `^rapor\.filex-conflict-\d{8}T\d{6}\.docx$`, out["conflict"])
	assert.False(t, f.exists("docs/rapor.docx"), "a conflict copy, not the save beside")
}

// The desktop's "open with filex" working copy takes an app's save, and its
// save beside, as it takes the Document Server's (syspath.SaveVerb).
func TestAppUIBeside_TheDesktopsWorkingCopyTakesTheAppsSaves(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, ".filex-open/0123456789ab-notlar.docx", "v1")
	f.writeFile(t, ".filex-open/0123456789ab-rapor.doc", "a Word 97 file")

	code, out := f.officeSave(t, nil, ".filex-open/0123456789ab-notlar.docx", nil, docxBytes("v2"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, docxBytes("v2"), f.readFile(t, ".filex-open/0123456789ab-notlar.docx"))

	code, out = f.officeSave(t, nil, ".filex-open/0123456789ab-rapor.doc", url.Values{"beside": {"docx"}}, docxBytes("edit"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "0123456789ab-rapor.docx", out["name"], "the session prefix stays first, as the desktop looks for it")
	assert.Equal(t, docxBytes("edit"), f.readFile(t, ".filex-open/0123456789ab-rapor.docx"))
}

// Editing together: the session's first save beside the file sends every
// later save of the session there, whoever of its members saves.
func TestAppUIBeside_ASessionsSavesFollowItsFirstSaveBeside(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/rapor.doc", "a Word 97 file")
	code, a, out := f.join(t, "main://docs/rapor.doc")
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))
	session := url.Values{"coedit": {a.Session}, "client": {a.Client}, "token": {a.Token}, "through": {"1"}, "beside": {"docx"}}

	code, out = f.officeSave(t, nil, "docs/rapor.doc", cloneValues(session), docxBytes("one"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "rapor.docx", out["name"])

	code, out = f.officeSave(t, nil, "docs/rapor.doc", cloneValues(session), docxBytes("two"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "rapor.docx", out["name"], "the same file, not rapor (2).docx")
	assert.Equal(t, true, out["beside"], "each save says where it went")
	assert.Equal(t, docxBytes("two"), f.readFile(t, "docs/rapor.docx"))
	assert.Equal(t, "a Word 97 file", f.readFile(t, "docs/rapor.doc"))

	s, err := f.coedit.Session(context.Background(), a.Session)
	require.NoError(t, err)
	assert.Equal(t, "docs/rapor.docx", s.SavedTo)
}

// Editing together: a file changed outside the session gets the session's
// save as a conflict copy, and its later saves follow it there.
func TestAppUIBeside_ASessionOnAChangedFileSavesToAConflictCopy(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installOfficeApp(t)
	f.writeFile(t, "docs/teklif.docx", "v1")
	code, a, out := f.join(t, "main://docs/teklif.docx")
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, http.StatusCreated, f.putBase(t, a, "base"))
	f.savedElsewhere(t, "docs/teklif.docx")
	session := url.Values{"coedit": {a.Session}, "client": {a.Client}, "token": {a.Token}, "through": {"1"}}

	code, out = f.officeSave(t, nil, "docs/teklif.docx", cloneValues(session), docxBytes("mine"))
	assert.Equal(t, http.StatusPreconditionFailed, code, "no copy asked for: refused as in 0.56 %v", out)

	session.Set("conflict", "copy")
	code, out = f.officeSave(t, nil, "docs/teklif.docx", cloneValues(session), docxBytes("mine"))
	require.Equal(t, http.StatusOK, code, out)
	first, _ := out["conflict"].(string)
	assert.Regexp(t, conflictNameRe, first)

	code, out = f.officeSave(t, nil, "docs/teklif.docx", cloneValues(session), docxBytes("mine, later"))
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, first, out["conflict"], "the same copy")
	assert.Equal(t, docxBytes("mine, later"), f.readFile(t, "docs/"+first))
	assert.Equal(t, "v2, written by somebody else", f.readFile(t, "docs/teklif.docx"))
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

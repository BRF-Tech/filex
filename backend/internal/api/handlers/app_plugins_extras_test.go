package handlers_test

// The files an administrator adds to an app (handlers/app_plugins_extras.go,
// wasmplugin/extras.go): the office editor's extra dictionaries, through the
// whole router - the admin routes, their audit rows, and the interface route
// that serves them under the app's package address.
//
// RED on the code before 0.57: /api/admin/app-plugins/{id}/extras did not
// exist (404 from the router), and /_appui/<app>/<sha16>/extra/... answered
// 404, so a language the office editor's bundle does not carry could not be
// spell-checked at all.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// readingAppManifest is an interface-only app that reads its own package
// (`ui.package_fetch`) and takes dictionaries (`ui.extras`), as the office
// editor does - here with no catalog: upload only.
const readingAppManifest = `{"manifest_version":1,"name":"wordish","version":"1.0.0","label":{"en":"Wordish"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{},"package_fetch":true,"extras":{"dictionaries":{}}},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Wordish"},"applies":{"ext":["wordish"]}}]}`

// installReadingApp uploads it (granted as reviewed) and answers its id and
// its package's address (`/_appui/wordish/<sha16>/`).
func (f *appFixture) installReadingApp(t *testing.T) (int64, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write([]byte(readingAppManifest))
	uf, _ := w.CreateFormFile("ui", "ui.zip")
	_, _ = uf.Write(uiBundleZip(t))
	g, _ := json.Marshal(map[string]any{"permissions": []string{"files:read", "files:write", "ui", "ui-viewer:.wordish", "ui:package-fetch"}})
	_ = w.WriteField("grant", string(g))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, string(raw))
	p, ok := f.reg.ByName("wordish")
	require.True(t, ok)
	return p.Row.ID, "/_appui/wordish/" + p.Row.UISHA256[:16] + "/"
}

// extrasCall sends one request with the fixture's administrator session, or
// with token (an API key, no cookie).
func (f *appFixture) extrasCall(t *testing.T, token, method, path string, body []byte) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/octet-stream")
	c := f.admin
	if token != "" {
		req.Header.Set("X-Filex-Token", token)
		c = &http.Client{}
	}
	res, err := c.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestAppExtras_AnAdministratorAddsADictionaryTheInterfaceReads(t *testing.T) {
	f := newAppFixture(t, nil)
	id, pkg := f.installReadingApp(t)
	base := "/api/admin/app-plugins/" + strconv.FormatInt(id, 10) + "/extras"
	dic := []byte("2\nkot/A\npies/B\n")

	status, ans := f.extrasCall(t, "", http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, status, "%v", ans)
	assert.Equal(t, true, ans["read"], "the interface reads its package, so it can read what is added")
	assert.Empty(t, ans["files"])

	status, put := f.extrasCall(t, "", http.MethodPut, base+"/dictionaries/pl_PL/pl_PL.dic.txt", dic)
	require.Equal(t, http.StatusOK, status, "%v", put)
	sum := sha256.Sum256(dic)
	assert.Equal(t, hex.EncodeToString(sum[:]), put["sha256"])
	assert.Equal(t, "dictionaries/pl_PL/pl_PL.dic.txt", put["path"])

	rows := auditRows(t, f.store, "app_plugin.extra_add")
	require.Len(t, rows, 1, "one row for the added file")
	assert.Equal(t, "app_plugin", rows[0].TargetType)
	assert.Equal(t, strconv.FormatInt(id, 10), rows[0].TargetID)
	assert.Equal(t, "extra/dictionaries/pl_PL/pl_PL.dic.txt", rows[0].Metadata["path"])
	assert.Equal(t, hex.EncodeToString(sum[:]), rows[0].Metadata["sha256"])

	status, ans = f.extrasCall(t, "", http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, status)
	files, _ := ans["files"].([]any)
	require.Len(t, files, 1)

	// Where the office editor's spell checker asks for it: under the
	// package, without a session, as data.
	res, err := http.Get(f.srv.URL + pkg + "extra/dictionaries/pl_PL/pl_PL.dic.txt")
	require.NoError(t, err)
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, dic, got)
	assert.Equal(t, "text/plain; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Equal(t, "default-src 'none'; sandbox", res.Header.Get("Content-Security-Policy"))
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"))

	// What is refused, and why, in the reader's words.
	status, ans = f.extrasCall(t, "", http.MethodPut, base+"/dictionaries/pl_PL/pl_PL.html", []byte("<script>1</script>"))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "extra_path", ans["error"], "%v", ans)
	assert.NotEmpty(t, ans["message"])
	status, ans = f.extrasCall(t, "", http.MethodPut, base+"/dictionaries/pl_PL/pl_PL.aff.txt", []byte("PK\x03\x04\x00zip"))
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, "extra_not_text", ans["error"], "%v", ans)

	// An admin API key reads the list; a change needs an administrator
	// signed in, like every change to what an app is handed.
	admin, err := f.store.GetUserByEmail(context.Background(), "admin@test.local")
	require.NoError(t, err)
	key := testutil.NewAPIToken(t, f.store, admin.ID, "read,write,delete,mcp,admin")
	status, _ = f.extrasCall(t, key, http.MethodGet, base, nil)
	assert.Equal(t, http.StatusOK, status)
	status, ans = f.extrasCall(t, key, http.MethodPut, base+"/dictionaries/de_AT/de_AT.dic.txt", dic)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "session_required", ans["error"], "%v", ans)
	status, ans = f.extrasCall(t, key, http.MethodDelete, base+"/dictionaries/pl_PL/pl_PL.dic.txt", nil)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, "session_required", ans["error"], "%v", ans)

	status, _ = f.extrasCall(t, "", http.MethodDelete, base+"/dictionaries/pl_PL/pl_PL.dic.txt", nil)
	assert.Equal(t, http.StatusNoContent, status)
	require.Len(t, auditRows(t, f.store, "app_plugin.extra_remove"), 1)
	status, ans = f.extrasCall(t, "", http.MethodDelete, base+"/dictionaries/pl_PL/pl_PL.dic.txt", nil)
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "not_found", ans["error"], "%v", ans)
	res, err = http.Get(f.srv.URL + pkg + "extra/dictionaries/pl_PL/pl_PL.dic.txt")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode, "removed: no longer served")
}

func TestAppExtras_AnAppThatCannotReadThemIsGivenNone(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	p, ok := f.reg.ByName("sketch")
	require.True(t, ok)
	base := "/api/admin/app-plugins/" + strconv.FormatInt(p.Row.ID, 10) + "/extras"
	status, ans := f.extrasCall(t, "", http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, ans["read"])
	status, ans = f.extrasCall(t, "", http.MethodPut, base+"/dictionaries/pl_PL/pl_PL.dic.txt", []byte("1\nkot\n"))
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "extras_unused", ans["error"], "%v", ans)
	assert.True(t, strings.TrimSpace(ans["message"].(string)) != "", "said in words")
	assert.Empty(t, auditRows(t, f.store, "app_plugin.extra_add"), "nothing added, nothing recorded")
}

// The catalog door (0.57): an app that offers no catalog says so, in the
// administrator's words, and an item cannot be installed from it.
func TestAppExtras_TheCatalogDoorSaysWhenThereIsNone(t *testing.T) {
	f := newAppFixture(t, nil)
	id, _ := f.installReadingApp(t)
	base := "/api/admin/app-plugins/" + strconv.FormatInt(id, 10)

	code, out := f.extrasCall(t, "", http.MethodGet, base+"/extras", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, []any{"dictionaries"}, out["kinds"], "the kinds the manifest names")
	assert.Equal(t, []any{}, out["catalogs"])

	code, out = f.extrasCall(t, "", http.MethodGet, base+"/extras-catalog/dictionaries", nil)
	assert.Equal(t, http.StatusNotFound, code, out)
	assert.Equal(t, "extras_no_catalog", out["error"])
	code, out = f.extrasCall(t, "", http.MethodPost, base+"/extras-catalog/dictionaries/pl_PL", nil)
	assert.Equal(t, http.StatusNotFound, code, out)
	assert.Equal(t, "extras_no_catalog", out["error"])
	code, out = f.extrasCall(t, "", http.MethodGet, base+"/extras-catalog/fonts", nil)
	assert.Equal(t, http.StatusConflict, code, out)
	assert.Equal(t, "extras_unused", out["error"])
}

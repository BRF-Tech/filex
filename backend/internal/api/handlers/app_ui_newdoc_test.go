package handlers_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// Burak, 2026-09-27: an app's rows in the "New" menu (`new_documents`). The
// explorer is told them with the other document types, a person makes one
// like any New document — a file, or a draft (issue #71) — and it is made of
// the app's template (or nothing).

const newDocAppManifest = `{"manifest_version":1,"name":"sketch","version":"1.0.0","label":{"en":"Sketch"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Sketch"},"applies":{"ext":["sketch"]}}],
 "new_documents":[{"ext":"sketch","label":{"en":"Sketch board"},"view":"editor","template":"new/blank.json"}]}`

func (f *appFixture) installNewDocApp(t *testing.T) int64 {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"index.html":     "<!doctype html><html><body>sketch</body></html>",
		"new/blank.json": `{"sketch":1}`,
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = io.WriteString(w, body)
	}
	require.NoError(t, zw.Close())
	st, _, err := f.reg.Install(context.Background(), &wasmplugin.InstallInput{
		Manifest: []byte(newDocAppManifest), UI: bytes.NewReader(buf.Bytes()), Source: "upload",
		Granted: []string{"files:read", "files:write", "ui", "ui-viewer:.sketch", "ui-new:.sketch"},
	})
	require.NoError(t, err)
	return st.ID
}

type newDocRow struct {
	Ext         string `json:"ext"`
	Key         string `json:"key"`
	Group       string `json:"group"`
	Requires    string `json:"requires"`
	ExtRequired bool   `json:"ext_required"`
	App         *struct {
		Plugin string            `json:"plugin"`
		View   string            `json:"view"`
		Label  map[string]string `json:"label"`
	} `json:"app"`
}

func newDocRows(t *testing.T, c *http.Client, base string) []newDocRow {
	t.Helper()
	res, err := c.Get(base + "/api/files/capabilities")
	require.NoError(t, err)
	defer res.Body.Close()
	var body struct {
		NewDocTypes []newDocRow `json:"newdoc_types"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	return body.NewDocTypes
}

func TestAppNewDoc_TheNewMenuOffersTheAppsKinds(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installNewDocApp(t)

	var app *newDocRow
	rows := newDocRows(t, f.admin, f.srv.URL)
	for i := range rows {
		if rows[i].Key == "app:sketch:sketch" {
			app = &rows[i]
		}
	}
	require.NotNil(t, app, "the app's row is offered: %+v", rows)
	assert.Equal(t, "sketch", app.Ext)
	assert.Equal(t, "app", app.Group)
	assert.Equal(t, "app", app.Requires)
	assert.True(t, app.ExtRequired)
	require.NotNil(t, app.App)
	assert.Equal(t, "sketch", app.App.Plugin)
	assert.Equal(t, "editor", app.App.View)
	assert.Equal(t, "Sketch board", app.App.Label["en"])

	// Not told to somebody who is not signed in: which apps an instance runs
	// is not a property of the build.
	for _, r := range newDocRows(t, freshClient(t), f.srv.URL) {
		assert.Empty(t, r.App, "anonymous: %+v", r)
		assert.False(t, strings.HasPrefix(r.Key, "app:"))
	}
}

func TestAppNewDoc_AFileIsMadeFromTheTemplate(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installNewDocApp(t)
	create := func(name, typ string) (int, map[string]any) {
		code, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/manager?action=newfile",
			map[string]any{"path": "main://", "name": name, "type": typ, "exact_name": true})
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return code, out
	}
	code, out := create("Plan", "app:sketch:sketch")
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "main://Plan.sketch", out["path"], "the kind's extension is the file's")
	assert.Equal(t, "sketch", out["ext"])
	got, err := os.ReadFile(filepath.Join(f.root, "Plan.sketch"))
	require.NoError(t, err)
	assert.Equal(t, `{"sketch":1}`, string(got), "made of the app's template")

	for _, bad := range []string{"app:sketch:nope", "app:other:sketch", "app:sketch"} {
		code, out := create("x", bad)
		assert.Equal(t, http.StatusBadRequest, code, bad)
		assert.Equal(t, "UNSUPPORTED_TYPE", out["code"], bad)
	}

	// The app switched off: its kind is no longer made.
	_, err = f.reg.SetEnabled(context.Background(), id, false)
	require.NoError(t, err)
	code, _ = create("Later", "app:sketch:sketch")
	assert.Equal(t, http.StatusBadRequest, code)
	for _, r := range newDocRows(t, f.admin, f.srv.URL) {
		assert.NotEqual(t, "app:sketch:sketch", r.Key, "and not offered")
	}
}

func TestAppNewDoc_ADraftIsMadeFromTheTemplateToo(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installNewDocApp(t)
	code, raw := doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/drafts",
		map[string]any{"path": "main://", "name": "Board", "type": "app:sketch:sketch", "exact_name": true})
	require.Less(t, code, 300, string(raw))
	var d struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Ext  string `json:"ext"`
	}
	require.NoError(t, json.Unmarshal(raw, &d))
	assert.Equal(t, "Board.sketch", d.Name)
	assert.Equal(t, "sketch", d.Ext)
	rel := strings.TrimPrefix(d.Path, "main://")
	got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	assert.Equal(t, `{"sketch":1}`, string(got))
	_, err = os.Stat(filepath.Join(f.root, "Board.sketch"))
	assert.True(t, os.IsNotExist(err), "nothing in the folder until it is saved")
}

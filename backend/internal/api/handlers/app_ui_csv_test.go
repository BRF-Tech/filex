package handlers_test

// A .csv saved and read through an app's interface (app_ui_csv.go, task
// #255 / #268): the save is written the file's way whichever way the app
// wrote it, a declaration its bytes contradict is refused, and the read says
// how the file is written.
//
// ⚠ On 0.56 every save here was written as the app sent it: the commas of a
// semicolon file, UTF-8 over a windows-1254 file, `7` for `007`, and a
// declaration (`csv_*`) was not read at all.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// csvAppManifest is an interface-only spreadsheet editor for .csv.
const csvAppManifest = `{"manifest_version":1,"name":"sheet","version":"1.0.0","label":{"en":"Sheet"},
 "permissions":["files:read","files:write"],"ui":{"bundle":{}},
 "views":[{"id":"editor","placement":"viewer","ui":"index.html","label":{"en":"Sheet"},"applies":{"ext":["csv"]}}]}`

func (f *appFixture) installCSVApp(t *testing.T) {
	t.Helper()
	_, _, err := f.reg.Install(context.Background(), &wasmplugin.InstallInput{
		Manifest: []byte(csvAppManifest), UI: bytes.NewReader(uiBundleZip(t)), Source: "upload",
		Granted: []string{"files:read", "files:write", "ui", "ui-viewer:.csv"},
	})
	require.NoError(t, err)
}

// csvSave is a save over main://<name> through the sheet app, with the
// declaration's query (csv_delimiter, csv_encoding, csv_bom, csv_eol).
func (f *appFixture) csvSave(t *testing.T, name, body string, decl url.Values) (int, map[string]any) {
	t.Helper()
	q := url.Values{"path": {"main://" + name}}
	for k, v := range decl {
		q[k] = v
	}
	code, raw := f.uiSave(t, "sheet", "editor", q.Encode(), body)
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &out)
	if len(out) == 0 {
		out["raw"] = raw
	}
	return code, out
}

// csvRead is the interface's read of main://<name>: the bytes and the
// dialect header.
func (f *appFixture) csvRead(t *testing.T, plugin, name string) (int, string, string) {
	t.Helper()
	res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/ui/" + plugin + "/editor/read?path=" + url.QueryEscape("main://"+name))
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(raw), res.Header.Get("X-Filex-Text-Dialect")
}

func crlfRows(rows ...string) string { return strings.Join(rows, "\r\n") + "\r\n" }
func lfRows(rows ...string) string   { return strings.Join(rows, "\n") + "\n" }

// The e2e 199 fixture: semicolons, CRLF, no byte order mark, cells a
// spreadsheet writes another way.
var csvFixture = crlfRows("ad;adet;not;kod;tel;tarih",
	`elma;3;"a; b";007;05320000001;01.02.2026`,
	"armut;5;şeker;042;05330000002;15.03.2026")

var csvFixtureEdited = strings.Replace(csvFixture, "elma;3;", "elma;42;", 1)

func TestAppUI_ACSVSaveIsWrittenTheFilesWay(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCSVApp(t)
	for name, saved := range map[string]string{
		// What the office editor's engine writes as Docs 9.4 does.
		"commas and a byte order mark": "\xEF\xBB\xBF" + lfRows("ad,adet,not,kod,tel,tarih",
			"elma,42,a; b,7,5320000001,1/2/2026", "armut,5,şeker,42,5330000002,15.03.2026"),
		// As Docs 10 does: the file's own delimiter (task #255).
		"the file's own semicolon": lfRows("ad;adet;not;kod;tel;tarih",
			`elma;42;"a; b";7;5320000001;1/2/2026`, "armut;5;şeker;42;5330000002;15.03.2026"),
	} {
		t.Run(name, func(t *testing.T) {
			f.writeFile(t, "liste.csv", csvFixture)
			code, out := f.csvSave(t, "liste.csv", saved, nil)
			require.Equal(t, http.StatusOK, code, out)
			assert.Equal(t, csvFixtureEdited, f.readFile(t, "liste.csv"),
				"semicolons, CRLF, no byte order mark; 007, 05320000001 and 01.02.2026 as the file has them")
			assert.EqualValues(t, len(csvFixtureEdited), out["size"], "the size written")
		})
	}
}

func TestAppUI_ACSVSaveTheDeclarationContradictsIsRefused(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCSVApp(t)
	f.writeFile(t, "liste.csv", csvFixture)
	semicolons := lfRows("ad;adet;not;kod;tel;tarih", `elma;42;"a; b";7;5320000001;1/2/2026`, "armut;5;şeker;42;5330000002;15.03.2026")

	code, out := f.csvSave(t, "liste.csv", semicolons, url.Values{"csv_delimiter": {","}})
	require.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Equal(t, "csv_dialect", out["error"])
	assert.Equal(t, "delimiter", out["field"])
	assert.Contains(t, saidIn("app_csv_delimiter", apierr.Params{"name": "liste.csv", "declared": ",", "found": ";"}), out["message"], "%v", out)
	assert.Equal(t, csvFixture, f.readFile(t, "liste.csv"), "nothing was written")

	code, out = f.csvSave(t, "liste.csv", semicolons, url.Values{"csv_eol": {"crlf"}})
	require.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Equal(t, "eol", out["field"])

	code, out = f.csvSave(t, "liste.csv", semicolons, url.Values{"csv_encoding": {"utf-16le"}})
	require.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Equal(t, "encoding", out["field"])
	assert.Equal(t, csvFixture, f.readFile(t, "liste.csv"))

	code, out = f.csvSave(t, "liste.csv", semicolons, url.Values{"csv_delimiter": {"::"}})
	require.Equal(t, http.StatusBadRequest, code, out)
	assert.Equal(t, "csv_declaration", out["error"])

	code, out = f.csvSave(t, "liste.csv", "PK\x03\x04 an XLSX", nil)
	require.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Equal(t, "csv_not_text", out["error"])

	// The declaration the bytes agree with is taken.
	code, out = f.csvSave(t, "liste.csv", semicolons, url.Values{
		"csv_delimiter": {";"}, "csv_encoding": {"utf-8"}, "csv_bom": {"0"}, "csv_eol": {"lf"},
	})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, csvFixtureEdited, f.readFile(t, "liste.csv"))
}

// A file in a code page: the read says which, the save is written back in
// it, and a character it cannot hold refuses the save.
func TestAppUI_ACSVInACodePage(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCSVApp(t)
	original := "kod;ad;not\r\n007;\xFEeker;a\r\n"
	f.writeFile(t, "eski.csv", original)

	code, body, dialect := f.csvRead(t, "sheet", "eski.csv")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, original, body, "the bytes as they are: the app decodes them with the encoding it is told")
	assert.JSONEq(t, `{"delimiter":";","bom":false,"crlf":true,"encoding":"windows-1254"}`, dialect)

	code, out := f.csvSave(t, "eski.csv", "kod,ad,not\n7,şeker,a\n9,ığdır,b\n", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "kod;ad;not\r\n007;\xFEeker;a\r\n9;\xFD\xF0d\xFDr;b\r\n", f.readFile(t, "eski.csv"))

	before := f.readFile(t, "eski.csv")
	code, out = f.csvSave(t, "eski.csv", "kod,ad,not\n7,şeker,a\n8,日本,c\n", nil)
	require.Equal(t, http.StatusUnprocessableEntity, code, out)
	assert.Equal(t, "csv_encoding", out["error"])
	assert.Equal(t, "windows-1254", out["encoding"])
	assert.Contains(t, saidIn("app_csv_unencodable", apierr.Params{"name": "eski.csv", "encoding": "windows-1254", "char": "日"}), out["message"], "%v", out)
	assert.Equal(t, before, f.readFile(t, "eski.csv"))
}

func TestAppUI_TheReadOfACSVSaysHowItIsWritten(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCSVApp(t)
	f.writeFile(t, "liste.csv", csvFixture)
	f.writeFile(t, "utf16.csv", "\xFF\xFEa\x00\t\x00b\x00\n\x00")

	code, body, dialect := f.csvRead(t, "sheet", "liste.csv")
	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{"delimiter":";","bom":false,"crlf":true,"encoding":"utf-8"}`, dialect)

	_, _, dialect = f.csvRead(t, "sheet", "utf16.csv")
	assert.JSONEq(t, `{"delimiter":"\t","bom":true,"crlf":false,"encoding":"utf-16le"}`, dialect)

	// A refusal says nothing of the file.
	code, _, dialect = f.csvRead(t, "sheet", "missing.csv")
	assert.NotEqual(t, http.StatusOK, code)
	assert.Empty(t, dialect)
}

// "Save as" a new .csv: no file to write it the way of, the bytes as the app
// wrote them - once they are what it declared.
func TestAppUI_ANewCSVIsHeldToItsDeclaration(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installCSVApp(t)
	save := func(body string, decl url.Values) (int, string) {
		q := url.Values{"dir": {"main://"}, "name": {"yeni.csv"}}
		for k, v := range decl {
			q[k] = v
		}
		return f.uiSave(t, "sheet", "editor", q.Encode(), body)
	}
	code, raw := save(lfRows("a;b", "1;2", "3;4"), url.Values{"csv_delimiter": {","}})
	require.Equal(t, http.StatusUnprocessableEntity, code, raw)
	assert.Contains(t, raw, `"field":"delimiter"`)

	code, raw = save(lfRows("a;b", "1;2", "3;4"), url.Values{"csv_delimiter": {";"}})
	require.Equal(t, http.StatusCreated, code, raw)
	assert.Equal(t, lfRows("a;b", "1;2", "3;4"), f.readFile(t, "yeni.csv"))
}

// Editing together: a save made in a session goes through the same door, and
// is written the file's way too.
func TestAppUI_ACSVSaveInAnEditingSessionIsWrittenTheFilesWay(t *testing.T) {
	f := newAppFixture(t, nil)
	manifest := strings.Replace(coeditManifest, `"applies":{"ext":["sketch"]}`, `"applies":{"ext":["csv"]}`, 1)
	require.NotEqual(t, coeditManifest, manifest)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	mf, _ := w.CreateFormFile("manifest", "filex-app.json")
	_, _ = mf.Write([]byte(manifest))
	uf, _ := w.CreateFormFile("ui", "ui.zip")
	_, _ = uf.Write(uiBundleZip(t))
	g, _ := json.Marshal(map[string]any{"permissions": []string{"files:co-edit", "files:read", "files:write", "ui", "ui-viewer:.csv"}})
	_ = w.WriteField("grant", string(g))
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/admin/app-plugins", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := f.admin.Do(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Less(t, res.StatusCode, 300, string(raw))

	f.writeFile(t, "liste.csv", csvFixture)
	_, a, _ := f.join(t, "main://liste.csv")
	require.Equal(t, http.StatusCreated, f.putBase(t, a, csvFixture))
	q := url.Values{"path": {"main://liste.csv"}, "coedit": {a.Session}, "client": {a.Client}, "token": {a.Token}, "through": {"1"}}
	code, body := f.uiSave(t, "sketch", "editor", q.Encode(), "\xEF\xBB\xBF"+lfRows("ad,adet,not,kod,tel,tarih",
		"elma,42,a; b,7,5320000001,1/2/2026", "armut,5,şeker,42,5330000002,15.03.2026"))
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, csvFixtureEdited, f.readFile(t, "liste.csv"))
}

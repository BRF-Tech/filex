package handlers_test

// Archives, public links and file requests with every hostile name (task
// #238): a selection ZIP holds the stored names and flags them UTF-8, an
// extraction lands them, a shared folder's page shows each one escaped and
// with its bidi controls made visible and links to the right file, and a file
// request takes them all - and refuses the ones no door may create as the
// visitor's to fix (400 bad_name), never as "storage unavailable".

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/testutil/archivetest"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// hnaGet is an agent-token GET of one file's bytes (/api/ai/download), the
// path escaped as a query value.
func hnaGet(t *testing.T, client *http.Client, base, tok, p string) (int, string) {
	t.Helper()
	resp := aiReq(t, client, http.MethodGet, base+"/api/ai/download?path="+url.QueryEscape(p), tok, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// hnaSeed makes dir and writes every name in it through the agent surface,
// each file holding its own id.
func hnaSeed(t *testing.T, client *http.Client, base, tok, dir string, names []hostilenames.Name) {
	t.Helper()
	resp := aiReq(t, client, http.MethodPost, base+"/api/ai/mkdir", tok, map[string]any{"path": "main://" + dir})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	for _, n := range names {
		resp := aiReq(t, client, http.MethodPost, base+"/api/ai/upload", tok, map[string]any{"path": "main://" + dir + "/" + n.Name, "content": n.ID})
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", n.ID, raw)
	}
}

func TestHostileNames_SelectionZipKeepsNamesAndUTF8Flag(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	names := hostilenames.ForLocalDisk()
	hnaSeed(t, client, srv.URL, tok, "hz", names)
	d := hostilenames.Deep()
	seedFiles(t, client, srv.URL, tok, map[string]string{"main://hz/" + d.Path: "deep"})

	code, info := mintArchive(t, client, srv.URL, tok, []string{"main://hz"})
	require.Equal(t, http.StatusOK, code, "mint: %v", info)
	link, _ := info["url"].(string)
	resp, raw := fetchArchive(t, srv.URL, link)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	members := map[string]*zip.File{}
	for _, f := range zr.File {
		members[f.Name] = f
	}
	body := func(f *zip.File) string {
		rc, err := f.Open()
		require.NoError(t, err)
		defer rc.Close()
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		return string(b)
	}
	for _, n := range names {
		// The copy unpacked elsewhere carries a bidi control as `_`
		// (leafname.SavedRune): the RLO member is invoice_fdp.exe.
		f, ok := members["hz/"+strings.Map(leafname.SavedRune, n.Name)]
		if !assert.True(t, ok, "%s: no member hz/%q", n.ID, n.Name) {
			continue
		}
		assert.Equal(t, n.ID, body(f), n.ID)
		// The flag is the member's: a name `_` made ASCII (invoice_fdp.exe)
		// is plain ASCII in the archive, and needs none.
		if !(len(f.Name) == utf8.RuneCountInString(f.Name)) {
			assert.NotZero(t, f.Flags&0x800, "%s: %q is not flagged UTF-8", n.ID, f.Name)
		}
	}
	f, ok := members["hz/"+d.Path]
	require.True(t, ok, "the deep path is not in the archive")
	assert.Equal(t, "deep", body(f))
}

// The archive's own name, cut at 120 bytes, is cut at a rune boundary: the
// byte cut ended a Turkish name in half a letter and the browser saved
// `a____.zip`.
func TestHostileNames_ArchiveNameIsValidUTF8(t *testing.T) {
	long := "a" + strings.Repeat("ş", 60)
	got := handlers.ArchiveName([]string{"main://x.txt"}, long)
	assert.True(t, utf8.ValidString(got), "%q", got)
	assert.LessOrEqual(t, len(strings.TrimSuffix(got, ".zip")), 120)
	assert.True(t, strings.HasPrefix(got, "aş"), "%q", got)
}

func TestHostileNames_ExtractLandsEveryName(t *testing.T) {
	srv, client, _, tok, opsSvc := aiFixtureWithOps(t)
	names := hostilenames.ForLocalDisk()
	files := map[string]string{}
	for _, n := range names {
		files[n.Name] = n.ID
	}
	d := hostilenames.Deep()
	files[d.Path] = "deep"
	raw := craftedZip(t, files)
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://archives/names.zip", "content_base64": base64.StdEncoding.EncodeToString(raw),
	})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	op := hnaExtract(t, client, srv.URL, tok, opsSvc, "main://archives/names.zip", "main://hx")
	require.Equal(t, ops.StatusOK, op.Status, op.Error)
	for name, want := range files {
		code, got := hnaGet(t, client, srv.URL, tok, "main://hx/"+name)
		if assert.Equal(t, http.StatusOK, code, "%q was not extracted: %s", name, got) {
			assert.Equal(t, want, got, "%q", name)
		}
	}
}

// hnaExtract queues an extraction and waits for it.
func hnaExtract(t *testing.T, client *http.Client, base, tok string, opsSvc *ops.Service, src, dest string) *ops.Op {
	t.Helper()
	resp := aiReq(t, client, http.MethodPost, base+"/api/files/archive/extract", tok, map[string]any{"path": src, "dest": dest})
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var queued struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &queued))
	return waitForArchiveOp(t, opsSvc, queued.Op.ID)
}

// A member named `...` or holding a NUL is skipped - no door may create the
// name (leafname) - and the rest of the archive lands. `...` used to refuse
// the WHOLE archive as unsafe (it begins with two dots).
func TestHostileNames_ExtractSkipsNamesNoDoorMayCreate(t *testing.T) {
	srv, client, _, tok, opsSvc := aiFixtureWithOps(t)
	raw := craftedZip(t, map[string]string{
		"ok.txt":                            "ok",
		hostilenames.Get("three-dots").Name: "dots",
		hostilenames.Get("nul").Name:        "nul",
		"..notes":                           "a name that begins with two dots",
		"1:1 notes.txt":                     "a colon after a digit",
	})
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://archives/mixed.zip", "content_base64": base64.StdEncoding.EncodeToString(raw),
	})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The job finishes PARTIAL and says what it left out (archive_skips.go;
	// the queue reads archiveSkipsError.Skips()).
	op := hnaExtract(t, client, srv.URL, tok, opsSvc, "main://archives/mixed.zip", "main://hy")
	require.Equal(t, ops.StatusPartial, op.Status, op.Error)
	assert.Equal(t, 2, op.Skipped, op.Error)
	assert.Contains(t, op.Error, `"..."`)
	for name, want := range map[string]string{
		"ok.txt": "ok", "..notes": "a name that begins with two dots", "1:1 notes.txt": "a colon after a digit",
	} {
		code, got := hnaGet(t, client, srv.URL, tok, "main://hy/"+name)
		require.Equal(t, http.StatusOK, code, "%q: %s", name, got)
		assert.Equal(t, want, got)
	}
}

// hnaTemplateText is s as html/template writes it into a page's text.
func hnaTemplateText(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, template.Must(template.New("").Parse(`{{.}}`)).Execute(&b, s))
	return b.String()
}

var hnaFileLink = regexp.MustCompile(`href="(/s/[^"/]+/f/[^"]+)"`)

// A shared folder's page shows every name escaped, with a bidi control made
// visible (the RLO name keeps its true extension at the end), and every row
// links to its own file.
func TestHostileNames_ShareFolderPage(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	names := hostilenames.ForLocalDisk()
	hnaSeed(t, client, srv.URL, tok, "hs", names)
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/share", tok, map[string]any{"path": "main://hs"})
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var shared struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &shared))
	require.NotEmpty(t, shared.Token)

	page, err := http.Get(srv.URL + "/s/" + shared.Token)
	require.NoError(t, err)
	pageRaw, _ := io.ReadAll(page.Body)
	page.Body.Close()
	require.Equal(t, http.StatusOK, page.StatusCode)
	body := string(pageRaw)

	rlo := string(rune(0x202E))
	assert.NotContains(t, body, rlo, "a right-to-left override reached the page")
	assert.Contains(t, body, hnaTemplateText(t, "invoice"+string(unicode.ReplacementChar)+"fdp.exe"))
	assert.NotContains(t, body, hostilenames.Get("angle-brackets").Name, "a name reached the page unescaped")
	for _, n := range names {
		if n.Has("bidi-control") {
			continue
		}
		assert.Contains(t, body, hnaTemplateText(t, n.Name), "%s is not on the page", n.ID)
	}

	// Every row's link serves its own file: the bodies are the ids.
	served := map[string]bool{}
	for _, m := range hnaFileLink.FindAllStringSubmatch(body, -1) {
		link := html.UnescapeString(m[1])
		r, err := http.Get(srv.URL + link)
		require.NoError(t, err)
		got, _ := io.ReadAll(r.Body)
		r.Body.Close()
		require.Equal(t, http.StatusOK, r.StatusCode, "%s: %s", link, got)
		served[string(got)] = true
	}
	for _, n := range names {
		assert.True(t, served[n.ID], "%s: no link on the page serves it", n.ID)
	}
}

// hnaDrop posts one submission to a file request: each name sent in `name[]`
// beside a part with a plain filename, the way the page's script sends it.
func hnaDrop(t *testing.T, r http.Handler, tok string, names []string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i, name := range names {
		part, err := mw.CreateFormFile("file[]", fmt.Sprintf("f%d.bin", i))
		require.NoError(t, err)
		_, _ = io.WriteString(part, "dropped "+name)
		require.NoError(t, mw.WriteField("name[]", name))
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/d/"+tok, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHostileNames_DropLandsEveryName(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "gelen", false)
	names := hostilenames.ForLocalDisk()
	for start := 0; start < len(names); start += 20 {
		end := min(start+20, len(names))
		var batch []string
		for _, n := range names[start:end] {
			batch = append(batch, n.Name)
		}
		rec := hnaDrop(t, r, tok, batch)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			Count  int    `json:"count"`
			Folder string `json:"folder"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, len(batch), out.Count)
		for _, name := range batch {
			got, err := os.ReadFile(filepath.Join(root, "gelen", out.Folder, name))
			if assert.NoError(t, err, "%q did not land", name) {
				assert.Equal(t, "dropped "+name, string(got))
			}
		}
	}
}

func TestHostileNames_DropRefusesNamesNoDoorMayCreate(t *testing.T) {
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "gelen", false)
	for _, n := range hostilenames.Refused() {
		rec := hnaDrop(t, r, tok, []string{"fine.txt", n.Name})
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", n.ID, rec.Body.String())
		var out map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, "bad_name", out["error"], n.ID)
		assert.NotEmpty(t, out["message"], n.ID)
	}
	entries, err := os.ReadDir(filepath.Join(root, "gelen"))
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused submission wrote something")
}

// A name longer than the folder's storage holds is the visitor's to shorten:
// 400 file_name_too_long, not 503 storage_unavailable (which told them to
// retry into the same refusal).
func TestHostileNames_DropTooLongIsTheVisitorsToShorten(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this file system holds every name of the list")
	}
	r, _, store, st, root := newDropFixture(t)
	tok, _ := mintDrop(t, r, store, st, root, "gelen", false)
	rec := hnaDrop(t, r, tok, []string{long[0].Name})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "file_name_too_long", out["error"])
	assert.NotContains(t, rec.Body.String(), root)
}

// hnaPost is an agent-token POST answering status and decoded body.
func hnaPost(t *testing.T, client *http.Client, base, tok, route string, body map[string]any) (int, map[string]any) {
	t.Helper()
	resp := aiReq(t, client, http.MethodPost, base+route, tok, body)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	out["_raw"] = string(raw)
	return resp.StatusCode, out
}

// hnaSkipped is an answer's `skipped` list as name -> reason.
func hnaSkipped(t *testing.T, out map[string]any) map[string]string {
	t.Helper()
	list, ok := out["skipped"].([]any)
	require.True(t, ok, "no skipped list: %v", out["_raw"])
	got := map[string]string{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		reason, _ := m["reason"].(string)
		got[name] = reason
	}
	return got
}

// The members left out for their names are listed one by one with why - on
// the explorer's extraction (answered in the request when no queue runs) and
// on the agent's unzip. They used to be counted, or only logged.
func TestHostileNames_ExtractListsSkippedMembers(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	members := map[string]string{
		"ok.txt":                                          "ok",
		hostilenames.Get("three-dots").Name:               "dots",
		hostilenames.Get("nul").Name:                      "nul",
		hostilenames.Get("control-tab").Name:              "tab",
		"sub/" + hostilenames.Get("control-newline").Name: "newline",
	}
	raw := craftedZip(t, members)
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://archives/skips.zip", "content_base64": base64.StdEncoding.EncodeToString(raw),
	})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	want := map[string]string{
		hostilenames.Get("three-dots").Name:               "name_not_allowed",
		hostilenames.Get("nul").Name:                      "name_not_allowed",
		hostilenames.Get("control-tab").Name:              "name_not_allowed",
		"sub/" + hostilenames.Get("control-newline").Name: "name_not_allowed",
	}

	code, out := hnaPost(t, client, srv.URL, tok, "/api/files/archive/extract", map[string]any{"path": "main://archives/skips.zip", "dest": "main://xs"})
	require.Equal(t, http.StatusOK, code, out["_raw"])
	assert.Equal(t, want, hnaSkipped(t, out))
	assert.EqualValues(t, 0, out["skipped_more"])

	code, out = hnaPost(t, client, srv.URL, tok, "/api/ai/unzip", map[string]any{"src": "main://archives/skips.zip", "dest": "main://ys"})
	require.Equal(t, http.StatusOK, code, out["_raw"])
	assert.Equal(t, want, hnaSkipped(t, out))
	code, got := hnaGet(t, client, srv.URL, tok, "main://ys/ok.txt")
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, "ok", got)
}

// A ZIP written without the UTF-8 flag in a DOS code page (Windows'
// "Compressed folder") is listed, selected and extracted under its decoded
// name. An account with no language reads IBM 437, the ZIP specification's:
// byte 0x9E is the peseta sign there (on a Turkish account, IBM 857, it is
// Ş). A name without the flag whose bytes ARE UTF-8 (macOS) stays UTF-8.
func TestHostileNames_CodePageZipNames(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	raw := archivetest.RawNameZip(t,
		archivetest.RawMember{Name: "\x9ei\x9fli.txt", Body: "cp"},
		archivetest.RawMember{Name: "Bütçe.txt", Body: "utf8"},
	)
	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://archives/dos.zip", "content_base64": base64.StdEncoding.EncodeToString(raw),
	})
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	dos := string(rune(0x20A7)) + "i" + string(rune(0x0192)) + "li.txt"

	code, out := hnaPost(t, client, srv.URL, tok, "/api/files/archive/list", map[string]any{"path": "main://archives/dos.zip"})
	require.Equal(t, http.StatusOK, code, out["_raw"])
	var listed []string
	entries, _ := out["entries"].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		name, _ := m["name"].(string)
		listed = append(listed, name)
	}
	assert.ElementsMatch(t, []string{dos, "Bütçe.txt"}, listed)

	// Selected by the name the listing gave.
	code, out = hnaPost(t, client, srv.URL, tok, "/api/files/archive/extract", map[string]any{
		"path": "main://archives/dos.zip", "dest": "main://one", "members": []string{dos},
	})
	require.Equal(t, http.StatusOK, code, out["_raw"])
	assert.EqualValues(t, 1, out["count"], out["_raw"])
	code, got := hnaGet(t, client, srv.URL, tok, "main://one/"+dos)
	require.Equal(t, http.StatusOK, code, got)
	assert.Equal(t, "cp", got)

	// All of it.
	code, out = hnaPost(t, client, srv.URL, tok, "/api/files/archive/extract", map[string]any{"path": "main://archives/dos.zip", "dest": "main://all"})
	require.Equal(t, http.StatusOK, code, out["_raw"])
	for name, want := range map[string]string{dos: "cp", "Bütçe.txt": "utf8"} {
		code, got := hnaGet(t, client, srv.URL, tok, "main://all/"+name)
		require.Equal(t, http.StatusOK, code, "%q: %s", name, got)
		assert.Equal(t, want, got)
	}
}

// On a Linux storage a backslash is part of a name: a shared folder lists
// `a\b.txt`, serves it from its link, and opens a folder named `x\y`. The
// share's path cleaner folded the backslash into a separator on every host,
// so the file answered 404 (task #238).
func TestHostileNames_ShareFolderBackslashName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash separates on Windows")
	}
	h, _, sh := newBrowseFixture(t, map[string]string{`a\b.txt`: "bs", `x\y/inner.txt`: "in"})
	rec := browseGet(h, sh.Token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "/f/a%5Cb.txt")

	req := httptest.NewRequest(http.MethodGet, "/s/"+sh.Token+"/f/a%5Cb.txt", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", sh.Token)
	rctx.URLParams.Add("*", `a\b.txt`)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	file := httptest.NewRecorder()
	h.HandleBrowseFile(file, req)
	require.Equal(t, http.StatusOK, file.Code, file.Body.String())
	assert.Equal(t, "bs", file.Body.String())

	rec = browseGet(h, sh.Token, "?dir="+url.QueryEscape(`x\y`))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "inner.txt")
}

// Two names that become one only once saved - `a` TAB `b.txt` (a file that
// already had the name) and `a_b.txt` - both go into an archive filex
// creates: the second is numbered with the selection download's own " (2)"
// (zipstream.Dedupe), instead of the whole archive being refused as a
// collision. The owner's decision for 0.57.
func TestHostileNames_ArchiveCreateNumbersSavedNameTwins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses a tab in a file name")
	}
	srv, client, store, tok, opsSvc := aiFixtureWithOps(t)
	storages, err := store.ListEnabledStorages(context.Background())
	require.NoError(t, err)
	var root string
	for _, s := range storages {
		if s.Name == "main" {
			var cfg struct {
				Root string `json:"root"`
			}
			require.NoError(t, json.Unmarshal(s.ConfigJSON, &cfg))
			root = cfg.Root
		}
	}
	require.NotEmpty(t, root)
	tab := hostilenames.Get("control-tab").Name // "tab" TAB "name.txt"
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ac"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ac", tab), []byte("tab"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ac", "tab_name.txt"), []byte("plain"), 0o644))

	resp := aiReq(t, client, http.MethodPost, srv.URL+"/api/files/archive/create", tok, map[string]any{
		"sources": []string{"main://ac/" + tab, "main://ac/tab_name.txt"},
		"dest":    "main://out/twins.zip",
		"format":  "zip",
	})
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var created struct {
		Op struct {
			ID int64 `json:"id"`
		} `json:"op"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	op := waitForArchiveOp(t, opsSvc, created.Op.ID)
	require.Equal(t, ops.StatusOK, op.Status, op.Error)

	code, zipped := hnaGet(t, client, srv.URL, tok, "main://out/twins.zip")
	require.Equal(t, http.StatusOK, code)
	got := openZip(t, []byte(zipped))
	assert.Equal(t, map[string]string{"tab_name.txt": "tab", "tab_name (2).txt": "plain"}, got)
}

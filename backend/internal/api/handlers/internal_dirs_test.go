package handlers_test

// filex's own directories — the trash, version history, the desktop app's
// "open with filex" working area — are never shown to a person, on any
// surface, however they got there (the owner, 2026-09-21: "Onlar hem bildirim
// içinde hem de tıklanınca webapp içinde gözükmüyor olması lazım").
//
// Measured on the unfixed build before these were written (repro against a
// live binary, same API the explorer and the desktop use):
//
//	root listing                    `.filex-open` in `files` (hidden only by
//	                                a client-side filter)
//	index .filex-trash              200, breadcrumb `docs › .filex-trash`
//	search "Plan"                   `/.filex-open/0123456789ab-Plan.docx`
//	trash list                      `a1b2c3d4e5f6-Bütçe Özeti.xlsx`, path
//	                                `/.filex-open/…`
//
// ⚠⚠ And one thing that must NOT change, pinned first because it is the one a
// well-meant fix breaks: the desktop app lists `.filex-open` itself, by exact
// path, to see whether the editor saved its copy — an empty answer reads to
// it as "unchanged" and the edit never reaches the original.

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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
)

// fxReq is one token-authenticated request against the running router — the
// way the desktop app talks to the manager API.
func fxReq(t *testing.T, method, u, tok string, body io.Reader, contentType string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, u, body)
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func fxMutate(t *testing.T, base, tok, action string, payload any) (int, string) {
	t.Helper()
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	return fxReq(t, "POST", base+"/api/files/manager?action="+action, tok, bytes.NewReader(b), "application/json")
}

// fxUpload is desktop/src/openwith-io.ts uploadFile, byte for byte in shape.
func fxUpload(t *testing.T, base, tok, dir, name, content string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", dir))
	fw, err := mw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = fw.Write([]byte(content))
	require.NoError(t, mw.Close())
	status, body := fxReq(t, "POST", base+"/api/files/manager?action=upload", tok, &buf, mw.FormDataContentType())
	require.Equal(t, http.StatusOK, status, "upload %s/%s: %s", dir, name, body)
}

func fxIndex(t *testing.T, base, tok, wire string) (int, []string) {
	t.Helper()
	status, body := fxReq(t, "GET", base+"/api/files/manager?action=index&path="+url.QueryEscape(wire), tok, nil, "")
	if status != http.StatusOK {
		return status, nil
	}
	var resp struct {
		Files []struct {
			Basename string `json:"basename"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp), body)
	names := make([]string, 0, len(resp.Files))
	for _, f := range resp.Files {
		names = append(names, f.Basename)
	}
	return status, names
}

// seedInternal lays out what a real account holds after a day of use: a
// person's folder, the desktop's working area with a copy still open, and a
// version snapshot. The first two go through the manager API exactly as the
// browser and the desktop send them, so every row the listings read is one
// the server itself wrote.
//
// ⚠ The version snapshot does NOT: `.versions` is written by the server
// (versioning), and the manager API refuses to create it or upload into it
// (TestInternalDirs_PeopleCannotWriteThere). It is laid down the way
// versioning does it — bytes on the storage, then the catalogue row.
func seedInternal(t *testing.T, base, tok string, store db.Store) {
	seedInternalIn(t, base, tok, store, "main")
}

func seedInternalIn(t *testing.T, base, tok string, store db.Store, storage string) {
	t.Helper()
	for _, dir := range []string{"Documents", ".filex-open"} {
		status, body := fxMutate(t, base, tok, "newfolder", map[string]any{"path": storage + "://", "name": dir})
		require.Equal(t, http.StatusOK, status, "newfolder %s: %s", dir, body)
	}
	fxUpload(t, base, tok, storage+"://Documents", "Plan notes.txt", "a person's own file")
	fxUpload(t, base, tok, storage+"://.filex-open", "0123456789ab-Plan.docx", "the desktop's working copy")
	seedServerFile(t, store, storage, ".versions/1", "an old version")
}

// seedServerFile writes rel the way filex's own services do (versioning, the
// trash): straight onto the storage's disk, then into the catalogue.
func seedServerFile(t *testing.T, store db.Store, storage, rel, content string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.GetStorageByName(ctx, storage)
	require.NoError(t, err)
	require.NotNil(t, st)
	var cfg struct {
		Root string `json:"root"`
	}
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	require.NotEmpty(t, cfg.Root, "storage %s has no local root", storage)
	full := filepath.Join(cfg.Root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	require.True(t, protocolsync.New(store, nil, nil, "test").Write(ctx, st, rel, int64(len(content)), "text/plain"),
		"catalogue %s", rel)
}

func TestInternalDirs_ManagerNeverOffersThem_ButServesTheDesktopsWorkingArea(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	seedInternal(t, srv.URL, tok, store)

	status, names := fxIndex(t, srv.URL, tok, "main://")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, names, "Documents")
	assert.NotContains(t, names, ".filex-open", "the root listing offered the desktop's working area")
	assert.NotContains(t, names, ".versions", "the root listing offered the version history")

	// ⚠⚠ The desktop's own read — must keep working.
	status, names = fxIndex(t, srv.URL, tok, "main://.filex-open")
	require.Equal(t, http.StatusOK, status, "the desktop app lists its working area by exact path")
	assert.Contains(t, names, "0123456789ab-Plan.docx", "the desktop's stat of its own copy came back empty")
	status, _ = fxReq(t, "GET", srv.URL+"/api/files/manager?action=download&path="+
		url.QueryEscape("main://.filex-open/0123456789ab-Plan.docx"), tok, nil, "")
	assert.Equal(t, http.StatusOK, status, "the desktop downloads the saved copy by exact path")

	// Sealed: never served by path, by any verb, however it is asked for.
	for _, wire := range []string{"main://.versions", "main://.filex-trash", "main://.thumbs", "main://Documents/.versions"} {
		for _, action := range []string{"index", "subfolders"} {
			status, body := fxReq(t, "GET", srv.URL+"/api/files/manager?action="+action+"&path="+
				url.QueryEscape(wire), tok, nil, "")
			assert.Equal(t, http.StatusNotFound, status, "action=%s %s was served: %s", action, wire, body)
		}
	}
	for _, action := range []string{"download", "preview"} {
		status, body := fxReq(t, "GET", srv.URL+"/api/files/manager?action="+action+"&path="+
			url.QueryEscape("main://.versions/1"), tok, nil, "")
		assert.Equal(t, http.StatusNotFound, status, "%s of a version snapshot by path: %s", action, body)
		assert.NotContains(t, body, "an old version")
	}

	// The toolbar search spans the storage: a working copy is not a result.
	status, body := fxReq(t, "GET", srv.URL+"/api/files/manager?action=search&path="+
		url.QueryEscape("main://")+"&filter=Plan", tok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Plan notes.txt", "precondition: the search itself works")
	assert.NotContains(t, body, ".filex-open")
	assert.NotContains(t, body, "0123456789ab-Plan.docx")

	// The global search (command palette / Home). ⚠ Scoped to the storage:
	// this fixture has no search index, and without a storage_id the SQL
	// fallback does not run at all — the assertions below then passed on an
	// empty answer and guarded nothing (caught by breaking the fix). The
	// indexed branch is measured in TestInternalDirs_GlobalSearchByIndex.
	st, err := store.GetStorageByName(context.Background(), "main")
	require.NoError(t, err)
	status, body = fxReq(t, "GET", fmt.Sprintf("%s/api/files/search?q=Plan&storage_id=%d", srv.URL, st.ID), tok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Plan notes.txt", "precondition: the global search itself works")
	assert.NotContains(t, body, ".filex-open")
	assert.NotContains(t, body, "0123456789ab-Plan.docx")
}

// The global search's INDEXED branch (Bleve), which is what answers on every
// real install: the working copy is indexed like any node the manager writes,
// so it is the result filter that has to drop it.
func TestInternalDirs_GlobalSearchByIndex(t *testing.T) {
	f := newMTFix(t, false)
	tok := issueToken(t, f.Store, f.UserA, fullScopes, nil)
	seedInternalIn(t, f.URL, tok, f.Store, "alpha")

	status, body := fxReq(t, "GET", f.URL+"/api/files/search?q=Plan", tok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Plan notes.txt", "precondition: the index answers")
	assert.NotContains(t, body, ".filex-open")
	assert.NotContains(t, body, "0123456789ab-Plan.docx")
}

func TestInternalDirs_ReadAndStatRefuseSealedRows(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	seedInternal(t, srv.URL, tok, store)
	st, err := store.GetStorageByName(context.Background(), "main")
	require.NoError(t, err)
	n, err := store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/.versions/1"))
	require.NoError(t, err, "precondition: the snapshot has a catalogue row")

	for _, u := range []string{
		fmt.Sprintf("/api/files/read?id=%d", n.ID),
		fmt.Sprintf("/api/files/read?storage=%d&path=%s", st.ID, url.QueryEscape(".versions/1")),
		fmt.Sprintf("/api/files/stat?id=%d", n.ID),
	} {
		status, body := fxReq(t, "GET", srv.URL+u, tok, nil, "")
		assert.Equal(t, http.StatusNotFound, status, "%s: %s", u, body)
		assert.NotContains(t, body, "an old version")
	}
}

// The trash is where the desktop's sweep sends a working copy once the edit is
// written back — a soft delete like any other. It must stay there (the app
// relies on the retention window) and must never be OFFERED back.
func TestInternalDirs_TrashNeverOffersASweptWorkingCopy(t *testing.T) {
	f := newMTFix(t, false)
	tok := issueToken(t, f.Store, f.UserA, fullScopes, nil)
	seedInternalIn(t, f.URL, tok, f.Store, "alpha")

	status, body := fxMutate(t, f.URL, tok, "delete", map[string]any{
		"path": "alpha://.filex-open", "items": []map[string]string{{"path": "alpha://.filex-open/0123456789ab-Plan.docx"}}})
	require.Equal(t, http.StatusOK, status, body)
	fxUpload(t, f.URL, tok, "alpha://Documents", "gone.txt", "bye")
	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{
		"path": "alpha://Documents", "items": []map[string]string{{"path": "alpha://Documents/gone.txt"}}})
	require.Equal(t, http.StatusOK, status, body)

	status, body = fxReq(t, "GET", f.URL+"/api/files/manager/trash", tok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "gone.txt", "precondition: the person's own delete is in the trash")
	assert.NotContains(t, body, ".filex-open", "the trash offered the desktop's swept working copy")
	assert.NotContains(t, body, "0123456789ab-Plan.docx")
	var list struct {
		Total int `json:"total"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	assert.Equal(t, 1, list.Total, "the total counts only what is offered")
}

func TestInternalDirs_RecentAndAIHideThem(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	seedInternal(t, srv.URL, tok, store)
	ctx := context.Background()

	// Recent: a working copy opened before it was hidden stays out of the view.
	st, err := store.GetStorageByName(ctx, "main")
	require.NoError(t, err)
	fxUpload(t, srv.URL, tok, "main://.filex-open", "abcdef012345-Kept.docx", "still open")
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/.filex-open/abcdef012345-Kept.docx"))
	require.NoError(t, err)
	status, body := fxReq(t, "POST", srv.URL+"/api/files/manager/recent", tok,
		strings.NewReader(fmt.Sprintf(`{"node_id":%d}`, n.ID)), "application/json")
	require.Equal(t, http.StatusOK, status, body)
	status, body = fxReq(t, "GET", srv.URL+"/api/files/manager/recent", tok, nil, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, ".filex-open", "Recent offered the desktop's working copy")

	// The AI / MCP surface: not listed, not reachable by path, not a search hit.
	resp := aiReq(t, http.DefaultClient, "GET", srv.URL+"/api/ai/files?path=main://", tok, nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	assert.Contains(t, string(b), "Documents")
	assert.NotContains(t, string(b), ".filex-open")
	assert.NotContains(t, string(b), ".versions")
	for _, p := range []string{"main://.filex-open/abcdef012345-Kept.docx", "main://.versions/1", "main://.filex-open"} {
		resp = aiReq(t, http.DefaultClient, "GET", srv.URL+"/api/ai/info?path="+url.QueryEscape(p), tok, nil)
		resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "AI info %s", p)
	}
	resp = aiReq(t, http.DefaultClient, "GET", srv.URL+"/api/ai/search?q=Kept&path=main://", tok, nil)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.NotContains(t, string(b), "Kept.docx", "AI search found the desktop's working copy")
}

func TestInternalDirs_PublicShareNeverShowsThem(t *testing.T) {
	h, _, sh := newBrowseFixture(t, map[string]string{
		"photo.gif":                          browseTestGif,
		".versions/7/1":                      "an old version",
		".filex-open/0123456789ab-Plan.docx": "working copy",
	})
	rec := browseGet(h, sh.Token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	page := rec.Body.String()
	assert.Contains(t, page, "photo.gif", "precondition: the share lists its folder")
	assert.NotContains(t, page, ".versions", "an anonymous visitor was shown the version history")
	assert.NotContains(t, page, ".filex-open", "an anonymous visitor was shown the working area")

	for _, rel := range []string{".versions/7/1", ".filex-open/0123456789ab-Plan.docx"} {
		rec = browseGetFile(h, sh.Token, rel, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "/s/<token>/f/%s served its bytes", rel)
		assert.NotContains(t, rec.Body.String(), "old version")
	}
}

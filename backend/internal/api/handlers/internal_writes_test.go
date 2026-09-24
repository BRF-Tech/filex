package handlers_test

// People cannot WRITE into filex's own directories either.
//
// The first half of this work (internal_dirs_test.go) stopped every surface
// from SHOWING `.filex-trash`, `.versions`, `.thumbs` and `.filex-open`. Every
// surface still let a person write there. Measured against the build before
// handlers/reserved_guard.go, as an ordinary editor:
//
//	newfolder  main:// ".filex-trash"                     200, a folder nobody can open
//	upload     main://.filex-trash  x.txt                 200, a file nobody can find
//	rename     Documents/Plan notes.txt → ".versions"     200, the document vanished
//	delete     main://.filex-trash/<key>                  200, a HARD delete from the bin
//	                                                     (removing for good is admin-only)
//	copy       Documents/… → main://.filex-trash          202, and the op ran
//	extract    a zip holding `.versions/evil.txt`         the member landed in .versions
//	share      main://.filex-open                         a public link to other people's
//	                                                     open documents
//
// ⚠⚠ And the one thing that must keep working, pinned first: the desktop
// app's "open with filex" round trip, which writes into `.filex-open` through
// exactly four requests (desktop/src/openwith-io.ts). Refusing any of them
// shows no error where a person looks — the edit just never comes back.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// fxUploadStatus is fxUpload without the 200 requirement.
func fxUploadStatus(t *testing.T, base, tok, dir, name, content string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", dir))
	fw, err := mw.CreateFormFile("file[]", name)
	require.NoError(t, err)
	_, _ = fw.Write([]byte(content))
	require.NoError(t, mw.Close())
	return fxReq(t, "POST", base+"/api/files/manager?action=upload", tok, &buf, mw.FormDataContentType())
}

func fxPost(t *testing.T, u, tok string, payload any) (int, string) {
	t.Helper()
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	return fxReq(t, "POST", u, tok, bytes.NewReader(b), "application/json")
}

// assertReserved: the refusal every surface answers with.
func assertReserved(t *testing.T, what string, status int, body string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, status, "%s was let through: %s", what, body)
	assert.Contains(t, body, "RESERVED_NAME", "%s: %s", what, body)
}

// TestInternalDirs_TheDesktopsRoundTripStillWrites is openwith-io.ts, call for
// call: ensureScratchDir (on every open, so twice), uploadFile, statRemote,
// downloadFile, deleteRemote.
func TestInternalDirs_TheDesktopsRoundTripStillWrites(t *testing.T) {
	srv, _, _, tok := aiFixture(t)
	const copyName = "a1b2c3d4e5f6-Bütçe Özeti.xlsx"

	for i := 1; i <= 2; i++ {
		status, body := fxMutate(t, srv.URL, tok, "newfolder", map[string]any{"path": "main://", "name": ".filex-open"})
		created := status == http.StatusOK || status == http.StatusConflict ||
			strings.Contains(strings.ToLower(body), "exist")
		require.True(t, created, "ensureScratchDir #%d was refused: %d %s", i, status, body)
	}
	status, body := fxUploadStatus(t, srv.URL, tok, "main://.filex-open", copyName, "the document")
	require.Equal(t, http.StatusOK, status, "uploadFile was refused: %s", body)

	status, names := fxIndex(t, srv.URL, tok, "main://.filex-open")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, names, copyName, "statRemote cannot see the copy it just uploaded")

	status, body = fxReq(t, "GET", srv.URL+"/api/files/manager?action=download&path="+
		url.QueryEscape("main://.filex-open/"+copyName), tok, nil, "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "the document", body, "downloadFile did not get the copy back")

	status, body = fxMutate(t, srv.URL, tok, "delete", map[string]any{
		"path":  "main://.filex-open",
		"items": []map[string]string{{"path": "main://.filex-open/" + copyName}},
	})
	require.Equal(t, http.StatusOK, status, "deleteRemote was refused: %s", body)
	_, names = fxIndex(t, srv.URL, tok, "main://.filex-open")
	assert.NotContains(t, names, copyName, "deleteRemote left the copy behind")
}

// TestInternalDirs_PeopleCannotWriteThere walks every HTTP verb that writes,
// moves, extracts, shares or restores.
func TestInternalDirs_PeopleCannotWriteThere(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	admin, err := f.Store.GetUserByEmail(ctx, "admin@alpha.test")
	require.NoError(t, err)
	tok := issueToken(t, f.Store, admin.ID, fullScopes, nil)
	seedInternalIn(t, f.URL, tok, f.Store, "alpha")
	const doc = "alpha://Documents/Plan notes.txt"
	const workCopy = "alpha://.filex-open/0123456789ab-Plan.docx"

	// ── manager verbs ────────────────────────────────────────────────────
	for _, c := range []struct{ parent, name string }{
		{"alpha://", ".filex-trash"},
		{"alpha://", ".versions"},
		{"alpha://", ".thumbs"},
		{"alpha://", ".keepdir"},
		{"alpha://Documents", ".filex-open"}, // the work area, but not at the root
		{"alpha://.filex-open", "sub"},       // nothing inside the work area
	} {
		status, body := fxMutate(t, f.URL, tok, "newfolder", map[string]any{"path": c.parent, "name": c.name})
		assertReserved(t, "newfolder "+c.parent+" "+c.name, status, body)
	}
	status, body := fxMutate(t, f.URL, tok, "newfile", map[string]any{"path": "alpha://.filex-open", "name": "a1b2c3d4e5f6-x", "type": "txt"})
	assertReserved(t, "newfile in the work area", status, body)
	// (A new document always gets its extension, so `.keepdir` becomes the
	// ordinary `.keepdir.txt` — only the folder can be reserved here.)
	status, body = fxMutate(t, f.URL, tok, "newfile", map[string]any{"path": "alpha://.versions", "name": "x", "type": "txt"})
	assertReserved(t, "newfile in .versions", status, body)

	status, body = fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://Documents", "item": doc, "name": ".versions"})
	assertReserved(t, "rename to .versions", status, body)
	status, body = fxMutate(t, f.URL, tok, "rename", map[string]any{"path": "alpha://.filex-open", "item": workCopy, "name": "Plan.docx"})
	assertReserved(t, "rename of a working copy", status, body)

	status, body = fxMutate(t, f.URL, tok, "move", map[string]any{"path": "alpha://.filex-trash", "items": []map[string]string{{"path": doc}}})
	assertReserved(t, "move into the trash folder", status, body)
	status, body = fxMutate(t, f.URL, tok, "move", map[string]any{"path": "alpha://Documents", "items": []map[string]string{{"path": workCopy}}})
	assertReserved(t, "move a working copy out", status, body)

	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{"path": "alpha://.versions", "items": []map[string]string{{"path": "alpha://.versions/1"}}})
	assertReserved(t, "delete a version snapshot by path", status, body)
	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{"path": "alpha://", "items": []map[string]string{{"path": "alpha://.filex-open"}}})
	assertReserved(t, "delete the work area itself", status, body)

	for _, c := range []struct{ dir, name string }{
		{"alpha://.filex-trash", "x.txt"},
		{"alpha://Documents", ".keepdir"},
		{"alpha://.filex-open", "Plan.docx"},               // no session prefix
		{"alpha://.filex-open/sub", "a1b2c3d4e5f6-x.docx"}, // not directly in it
		{"alpha://Documents/.filex-open", "a1b2c3d4e5f6-x.docx"},
	} {
		status, body := fxUploadStatus(t, f.URL, tok, c.dir, c.name, "x")
		assertReserved(t, "upload "+c.dir+"/"+c.name, status, body)
	}

	// ── the other write surfaces ────────────────────────────────────────
	// A text snapshot, so the text editor's own rules (extension, size) would
	// let the save through if nothing else stopped it.
	seedServerFile(t, f.Store, "alpha", ".versions/7/notes.txt", "the old notes")
	status, body = fxPost(t, f.URL+"/api/files/save-text", tok, map[string]any{"path": "alpha://.versions/7/notes.txt", "content": "rewritten"})
	assertReserved(t, "save-text over a version", status, body)
	notes, err := os.ReadFile(filepath.Join(f.RootA, ".versions", "7", "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "the old notes", string(notes), "save-text rewrote a version snapshot")
	status, body = fxPost(t, f.URL+"/api/files/upload/init", tok, map[string]any{"storage_id": f.StA.ID, "path": "alpha://.versions", "filename": "x.txt", "size": 3})
	assertReserved(t, "chunked upload into .versions", status, body)
	status, body = fxPost(t, f.URL+"/api/files/upload/begin", tok, map[string]any{"path": "alpha://.filex-trash", "name": "x.txt", "size": 3})
	assertReserved(t, "staged upload into the trash folder", status, body)

	status, body = fxPost(t, f.URL+"/api/files/copy", tok, map[string]any{"source": []string{doc}, "target": "alpha://.filex-trash/"})
	assertReserved(t, "queued copy into the trash folder", status, body)
	status, body = fxPost(t, f.URL+"/api/files/move", tok, map[string]any{"source": []string{workCopy}, "target": "alpha://Documents/"})
	assertReserved(t, "queued move of a working copy", status, body)
	status, body = fxPost(t, f.URL+"/api/files/delete", tok, map[string]any{"source": []string{"alpha://.versions/1"}})
	assertReserved(t, "queued delete of a version", status, body)
	status, body = fxPost(t, f.URL+"/api/files/ops", tok, map[string]any{"kind": "copy", "storage_id": f.StA.ID, "sources": []string{"Documents/Plan notes.txt"}, "dest": ".thumbs/"})
	assertReserved(t, "unified ops copy into .thumbs", status, body)

	status, body = fxPost(t, f.URL+"/api/files/share", tok, map[string]any{"path": "alpha://.filex-open"})
	assertReserved(t, "a public link to the work area", status, body)
	status, body = fxPost(t, f.URL+"/api/files/permissions", tok, map[string]any{"path": "alpha://.filex-trash", "user_id": f.UserA, "level": "viewer"})
	assertReserved(t, "a grant on the trash folder", status, body)

	// ── archives ─────────────────────────────────────────────────────────
	zipBytes := buildZip(t, map[string]string{
		"ok.txt":                          "fine",
		".versions/evil.txt":              "planted history",
		"sub/.keepdir":                    "",
		".filex-open/a1b2c3d4e5f6-x.docx": "planted copy",
	})
	fxUpload(t, f.URL, tok, "alpha://Documents", "bundle.zip", string(zipBytes))
	status, body = fxPost(t, f.URL+"/api/files/archive/extract", tok, map[string]any{"storage_id": f.StA.ID, "path": "Documents/bundle.zip", "dest": ".filex-open"})
	assertReserved(t, "extracting into the work area", status, body)
	status, body = fxPost(t, f.URL+"/api/files/archive/extract", tok, map[string]any{"storage_id": f.StA.ID, "path": "Documents/bundle.zip", "dest": "Unpacked"})
	require.Equal(t, http.StatusOK, status, "an ordinary extraction: %s", body)
	assert.FileExists(t, filepath.Join(f.RootA, "Unpacked", "ok.txt"), "the ordinary member was not extracted")
	for _, planted := range []string{".versions/evil.txt", "sub/.keepdir", ".filex-open/a1b2c3d4e5f6-x.docx"} {
		assert.NoFileExists(t, filepath.Join(f.RootA, "Unpacked", filepath.FromSlash(planted)), "extraction wrote %s", planted)
	}
	status, body = fxPost(t, f.URL+"/api/files/archive/add", tok, map[string]any{"storage_id": f.StA.ID, "path": ".filex-open/a.zip", "files": []map[string]string{{"source": "Documents/Plan notes.txt", "name": "p.txt"}}})
	assertReserved(t, "an archive created in the work area", status, body)
	status, body = fxPost(t, f.URL+"/api/files/archive/add", tok, map[string]any{"storage_id": f.StA.ID, "path": "Documents/b.zip", "files": []map[string]string{{"source": ".versions/1", "name": "v.txt"}}})
	assertReserved(t, "an archive packing a version snapshot", status, body)

	// ── AI surface: a reserved member of an ordinary destination ─────────
	fxUpload(t, f.URL, tok, "alpha://Documents", "agent.zip", string(zipBytes))
	resp := aiReq(t, http.DefaultClient, "POST", f.URL+"/api/ai/unzip", tok, map[string]any{"src": "alpha://Documents/agent.zip", "dest": "alpha://AgentOut"})
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	assert.FileExists(t, filepath.Join(f.RootA, "AgentOut", "ok.txt"))
	assert.NoFileExists(t, filepath.Join(f.RootA, "AgentOut", ".versions", "evil.txt"), "the agent's unzip wrote into .versions")
	assert.NoFileExists(t, filepath.Join(f.RootA, "AgentOut", "sub", ".keepdir"))

	// ── trash: a swept working copy is not restored into the work area ─
	n, err := f.Store.GetNodeByPath(ctx, f.StA.ID, pathkey.Hash(f.StA.ID, "/.filex-open/0123456789ab-Plan.docx"))
	require.NoError(t, err)
	status, body = fxMutate(t, f.URL, tok, "delete", map[string]any{"path": "alpha://.filex-open", "items": []map[string]string{{"path": workCopy}}})
	require.Equal(t, http.StatusOK, status, "the desktop's delete: %s", body)
	status, body = fxPost(t, f.URL+"/api/files/manager/restore", tok, map[string]any{"node_id": n.ID})
	assert.Equal(t, http.StatusNotFound, status, "a swept working copy was restored: %s", body)
	assert.NoFileExists(t, filepath.Join(f.RootA, ".filex-open", "0123456789ab-Plan.docx"))

	// ── nothing reached the disk ─────────────────────────────────────────
	for _, rel := range []string{".filex-trash/x.txt", ".thumbs", "Documents/.keepdir", ".filex-open/Plan.docx", "Documents/.filex-open", ".filex-open/sub"} {
		assert.NoFileExists(t, filepath.Join(f.RootA, filepath.FromSlash(rel)), "%s was written", rel)
		assert.NoDirExists(t, filepath.Join(f.RootA, filepath.FromSlash(rel)), "%s was created", rel)
	}
	v, err := os.ReadFile(filepath.Join(f.RootA, ".versions", "1"))
	require.NoError(t, err, "the version snapshot was deleted")
	assert.Equal(t, "an old version", string(v), "the version snapshot was rewritten")
	assert.FileExists(t, filepath.Join(f.RootA, "Documents", "Plan notes.txt"), "the document was moved or renamed away")
}

// TestInternalDirs_DropLinkRefusesReservedNames: a visitor's file named like
// filex's own is refused with its own code, not a "storage unavailable".
func TestInternalDirs_DropLinkRefusesReservedNames(t *testing.T) {
	r, svc, store, st, root := newDropFixture(t)
	folder := mkdirNode(t, store, st, root, "inbox")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: folder.ID, Kind: model.ShareKindDrop})
	require.NoError(t, err)

	rec := doDropUpload(t, r, "/d/"+sh.Token, "", nil, []fpart{{".keepdir", "x"}})
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "reserved_name")
	assert.Empty(t, findUnder(root, "inbox", ".keepdir"), "the visitor's .keepdir was written")
}

// TestInternalDirs_AppOutputCannotBeNamedLikeFilexOwn: an app names its own
// output (wasmplugin committer); it does not get to name it `.keepdir` or put
// it under `.versions/`.
func TestInternalDirs_AppOutputCannotBeNamedLikeFilexOwn(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()
	resolver := func(id int64) (storage.Driver, error) {
		d := &local.Driver{}
		if err := d.Init(ctx, map[string]any{"root": f.RootA}); err != nil {
			return nil, err
		}
		return d, nil
	}
	h := handlers.NewAppPlugins(nil, f.Store, nil, nil, resolver, nil, nil)

	_, err := h.CommitSibling(ctx, f.StA.ID, "", ".keepdir", strings.NewReader("x"), 1, nil)
	assert.ErrorIs(t, err, syspath.ErrReserved, "an app wrote a .keepdir")
	_, err = h.CommitSibling(ctx, f.StA.ID, ".versions/9", "out.pdf", strings.NewReader("x"), 1, nil)
	assert.ErrorIs(t, err, syspath.ErrReserved, "an app wrote into .versions")
	err = h.CommitVersion(ctx, f.StA.ID, ".filex-open/a1b2c3d4e5f6-x.docx", strings.NewReader("x"), 1, nil)
	assert.ErrorIs(t, err, syspath.ErrReserved, "an app overwrote a working copy")
	assert.NoFileExists(t, filepath.Join(f.RootA, ".keepdir"))
	assert.NoDirExists(t, filepath.Join(f.RootA, ".versions"))

	rel, err := h.CommitSibling(ctx, f.StA.ID, "", "out.pdf", strings.NewReader("x"), 1, nil)
	require.NoError(t, err, "precondition: an ordinary output is written")
	assert.Equal(t, "out.pdf", rel)
}

func buildZip(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = io.WriteString(w, content)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

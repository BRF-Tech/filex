package handlers_test

// Negative tests for the public doors and the job door, each refusing what it
// must. Written against the routes as they were before the hardening, so the
// same file runs red there.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func (f *appFixture) secNode(t *testing.T, rel string, kind model.NodeType, mime string) *model.Node {
	t.Helper()
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: filepath.Base(rel), Path: "/" + rel, PathHash: pathkey.Hash(f.st.ID, "/"+rel),
		Type: kind, Mime: mime, SyncState: model.SyncStateSynced,
	})
	require.NoError(t, err)
	return n
}

// secGet is a plain GET with a fresh cookie jar: a stranger holding a link.
func secGet(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := freshClient(t).Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// ⚠⚠ An app page's node is the ANCHOR its state hangs on, never a download —
// but the no-JS folder browser (/s/<token>/f/…) did not ask what kind of link
// it was serving, so an app link anchored on a folder handed out every file
// under it.
func TestSecurity_FolderBrowserServesNothingBehindAnAppLink(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.writeFile(t, "private/salaries.txt", "the payroll")
	dir := f.secNode(t, "private", model.NodeTypeDirectory, "")
	svc := share.NewService(f.store)
	ctx := context.Background()

	app, err := svc.Create(ctx, share.CreateOpts{NodeID: dir.ID, PluginID: id, PageID: "signer"})
	require.NoError(t, err)
	resp, body := secGet(t, f.srv.URL+"/s/"+app.Token+"/f/salaries.txt")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.NotContains(t, body, "the payroll")

	resp, _ = secGet(t, f.srv.URL+"/api/files/share/"+app.Token)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "nothing about an app link's anchor is answered")

	// The control: an ORDINARY share of the same folder serves the file.
	plain, err := svc.Create(ctx, share.CreateOpts{NodeID: dir.ID})
	require.NoError(t, err)
	resp, body = secGet(t, f.srv.URL+"/s/"+plain.Token+"/f/salaries.txt")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "the payroll")
}

// ⚠⚠ HandleDownload counts PIN strikes (five, then a ten-minute lock); the
// folder browser's file route compared the hash and counted nothing, so a
// four-digit PIN could be walked through there.
func TestSecurity_FolderBrowserPINIsTheOneGateWithItsLock(t *testing.T) {
	f := newAppFixture(t, nil)
	f.writeFile(t, "private/salaries.txt", "the payroll")
	dir := f.secNode(t, "private", model.NodeTypeDirectory, "")
	sh, err := share.NewService(f.store).Create(context.Background(), share.CreateOpts{NodeID: dir.ID, PIN: "4821"})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		resp, _ := secGet(t, f.srv.URL+"/s/"+sh.Token+"/f/salaries.txt?pin=0000")
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	}
	resp, body := secGet(t, f.srv.URL+"/s/"+sh.Token+"/f/salaries.txt?pin=4821")
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "five wrong answers lock the link; the right one is refused during the lock")
	assert.NotContains(t, body, "the payroll")
}

// ⚠ The file's name is behind the PIN, like its bytes; the older metadata
// route handed it out to anyone holding the token.
func TestSecurity_ShareMetadataKeepsTheNameBehindThePIN(t *testing.T) {
	f := newAppFixture(t, nil)
	f.writeFile(t, "docs/teklif.pdf", "%PDF-1.4")
	n := f.secNode(t, "docs/teklif.pdf", model.NodeTypeFile, "application/pdf")
	sh, err := share.NewService(f.store).Create(context.Background(), share.CreateOpts{NodeID: n.ID, PIN: "4821"})
	require.NoError(t, err)

	resp, body := secGet(t, f.srv.URL+"/api/files/share/"+sh.Token)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &meta))
	assert.Equal(t, true, meta["requires_pin"])
	assert.NotContains(t, meta, "filename")
	assert.NotContains(t, body, "teklif")
}

// ⚠⚠ filex served people's .html/.svg inline from its OWN origin with no
// policy: a share link with ?inline=1, and an app page's exposed copy whose
// name the APP chose. Script there runs as filex, with the session of
// whoever clicked. Now it renders inert: CSP sandbox, nothing it may load.
func TestSecurity_ActiveContentIsServedInert(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	ctx := context.Background()
	svc := share.NewService(f.store)
	const page = "<html><body><script>fetch('/api/auth/me')</script>hi</body></html>"

	f.writeFile(t, "docs/page.html", page)
	n := f.secNode(t, "docs/page.html", model.NodeTypeFile, "text/html")
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: n.ID})
	require.NoError(t, err)
	resp, _ := secGet(t, f.srv.URL+"/s/"+sh.Token+"?inline=1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "sandbox", "an inline .html share runs as filex otherwise")

	// An app page's exposed copy, named by the app.
	anchor := f.secNode(t, "docs/contract.txt", model.NodeTypeFile, "text/plain")
	files, _ := json.Marshal([]map[string]any{{"ref": "pub:0", "name": "invoice.html", "file": "0-invoice.html", "size": len(page)}})
	app, err := svc.Create(ctx, share.CreateOpts{NodeID: anchor.ID, PluginID: id, PageID: "signer", FilesJSON: string(files)})
	require.NoError(t, err)
	dir := filepath.Join(f.reg.Dir(), "public", strconv.FormatInt(app.ID, 10))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0-invoice.html"), []byte(page), 0o644))
	resp, body := secGet(t, f.srv.URL+"/api/public/s/"+app.Token+"/file/pub:0")
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "sandbox", "an app-named .html copy runs as filex otherwise")

	// A plain text file is left as it was.
	f.writeFile(t, "docs/notes.txt", "plain")
	txt := f.secNode(t, "docs/notes.txt", model.NodeTypeFile, "text/plain")
	tsh, err := svc.Create(ctx, share.CreateOpts{NodeID: txt.ID})
	require.NoError(t, err)
	resp, _ = secGet(t, f.srv.URL+"/s/"+tsh.Token+"?inline=1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Security-Policy"))
}

// ⚠⚠ `__output` is the host's per-job output choice, which runJob obeys
// without asking again. A run request's params came into the job row as
// sent, so a person who may only READ another storage put the result there
// by writing it into their request — past every check the output-folder door
// makes (tenant, editor right there, filex's own folders).
func TestSecurity_ARunRequestCannotChooseTheOutputFolderThroughParams(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEcho(t)
	dest, destRoot := f.addStorage(t, "dest")
	f.writeFile(t, "docs/a.txt", "hello")
	u := seedSharedUser(t, f.store, "reader@test.local", "ReaderPass1!")
	grant(t, f.store, f.st, u, "", model.GrantEditor, true)
	grant(t, f.store, dest, u, "", model.GrantViewer, true)
	reader := freshClient(t)
	testutil.LoginAs(t, f.srv, reader, "reader@test.local", "ReaderPass1!")

	op := f.runAndDrain(t, reader, "upper", map[string]any{
		"paths":  []string{"main://docs/a.txt"},
		"params": map[string]any{"__output": map[string]any{"mode": "folder", "dir": "dest://"}},
	})
	assert.Equal(t, "ok", op["status"], op)
	entries, err := os.ReadDir(destRoot)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing landed on a storage the person may only read")
	assert.FileExists(t, filepath.Join(f.root, "docs", "a-upper.txt"), "the result lands where the manifest says")
}

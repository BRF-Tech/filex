package handlers_test

// A folder chosen for an app's result tells a `root:` token nothing about what
// lies outside its folder (filex Roadmap #155, found in the GHSA-8gvc-6w52-6c7j
// review, 2026-10-04).
//
// checkOutputFolder is the one judge of such a folder: "save as" from an app's
// own interface (PUT .../ui/{plugin}/{view}/save?dir=&name=) and a job whose
// person chose where its result goes (a surface's output, mode folder). It
// asked the storage before it asked the token's root: an unknown storage
// answered 404, a read-only one 409, a folder that is not there 404, and only a
// folder that IS there reached the root check and its 403. So a token confined
// to main://docs could tell which folders and storages exist outside it. Every
// path outside the root now gets the same 403, before the storage is asked;
// inside the root the answers are what they were.
//
// The app doors that take the files an app runs on (a run, a screen's event,
// an interface's call) had the same order for the STORAGE: an unknown one
// answered 400 and a known one outside the root 403, an unknown storage id
// 404. The confinement middleware refuses both alike in a JSON body; a body
// sent as text/plain, or with no Content-Type, reached the handler as written.
//
// The token is bound to an ADMIN account: an admin's ACL clears every path, so
// nothing but the token's root can refuse.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// outsideFolders are folders outside main://docs, one of each kind the old
// order told apart.
var outsideFolders = []string{
	"main://secret",          // there
	"main://yok",             // not there
	"main://secret/yok",      // not there, under one that is
	"main://secret/s.sketch", // a file, not a folder
	"main://",                // the storage's own root
	"arsiv://",               // another storage, read-only
	"arsiv://x",              // a folder on it
	"yokdepo://x",            // no such storage
	"main://docs/../secret",  // climbing out of the root
}

// rootTokenForOutputs is an admin-bound token confined to main://docs, with a
// folder outside it (main://secret) and a read-only second storage.
func rootTokenForOutputs(t *testing.T, f *appFixture) string {
	t.Helper()
	f.writeFile(t, "docs/a.txt", "alpha")
	f.writeFile(t, "secret/s.sketch", "SECRET")
	arsiv, _ := f.addStorage(t, "arsiv")
	f.makeReadOnly(t, arsiv)
	u, err := f.store.CreateUser(context.Background(), "kutu-out@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	return issueToken(t, f.store, u.ID, "read,write,delete,root:main://docs", nil)
}

func TestAppOutputFolder_RootToken_SaveAsOutsideIsOneAnswer(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	tok := rootTokenForOutputs(t, f)
	save := func(dir string) (int, string) {
		req, err := http.NewRequest(http.MethodPut, f.srv.URL+"/api/files/plugins/ui/sketch/editor/save?dir="+url.QueryEscape(dir)+"&name=new.sketch", bytes.NewReader([]byte("v1")))
		require.NoError(t, err)
		req.Header.Set("X-Filex-Token", tok)
		req.Header.Set("Content-Type", "application/octet-stream")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}

	first, firstBody := save(outsideFolders[0])
	require.Equal(t, http.StatusForbidden, first, firstBody)
	for _, dir := range outsideFolders[1:] {
		code, body := save(dir)
		assert.Equal(t, http.StatusForbidden, code, "%s: %s", dir, body)
		assert.Equal(t, firstBody, body, "%s answers like any other folder outside the root", dir)
	}
	_, err := os.Stat(filepath.Join(f.root, "secret", "new.sketch"))
	assert.True(t, os.IsNotExist(err), "nothing was saved outside the root")

	// Inside the root the answers are what they were.
	code, body := save("main://docs")
	assert.Equal(t, http.StatusCreated, code, body)
	code, body = save("main://docs/yok")
	assert.Equal(t, http.StatusNotFound, code, "a folder inside the root that is not there: %s", body)
}

func TestAppOutputFolder_RootToken_AJobsFolderOutsideIsOneAnswer(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installEchoWith(t, func(m map[string]any) {
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			if a["id"] == "upper" {
				a["output"] = map[string]any{"mode": "sibling", "name": "{stem}-upper{ext}", "elsewhere": true}
			}
		}
	})
	tok := rootTokenForOutputs(t, f)
	submit := func(dir string) (int, string) {
		return appAs(t, f, tok, "/api/files/plugins/views/echo/hello/event", map[string]any{
			"paths": []string{"main://docs/a.txt"}, "event": "submit",
			"data": map[string]any{"output_mode": "folder", "output_dir": dir},
		})
	}

	first, firstBody := submit(outsideFolders[0])
	require.Equal(t, http.StatusForbidden, first, firstBody)
	for _, dir := range outsideFolders[1:] {
		code, body := submit(dir)
		assert.Equal(t, http.StatusForbidden, code, "%s: %s", dir, body)
		assert.Equal(t, firstBody, body, "%s answers like any other folder outside the root", dir)
	}

	code, body := submit("main://docs")
	assert.Equal(t, http.StatusAccepted, code, "a folder inside the root: %s", body)
	code, body = submit("main://docs/yok")
	assert.Equal(t, http.StatusNotFound, code, "a folder inside the root that is not there: %s", body)
}

func TestAppDoors_RootToken_AStorageOutsideIsOneAnswer(t *testing.T) {
	f, tok := rootConfinedApp(t)
	f.installUIApp(t)
	arsiv, _ := f.addStorage(t, "arsiv")

	doors := []struct{ label, route, body string }{
		{"a run", "/api/files/plugins/actions/echo/upper/run", `{"paths":["%s"]}`},
		{"a screen's event", "/api/files/plugins/views/echo/hello/event", `{"event":"change","paths":["%s"]}`},
		{"an interface's call", "/api/files/plugins/ui/sketch/editor/call", `{"method":"x","paths":["%s"]}`},
	}
	for _, d := range doors {
		for _, ct := range []string{"text/plain", ""} {
			code, first := confRaw(t, f.srv.URL, tok, http.MethodPost, d.route, ct, []byte(fmt.Sprintf(d.body, "arsiv://x.txt")))
			require.Equal(t, http.StatusForbidden, code, "%s (%q), a storage that is there: %s", d.label, ct, first)
			code, body := confRaw(t, f.srv.URL, tok, http.MethodPost, d.route, ct, []byte(fmt.Sprintf(d.body, "yokdepo://x.txt")))
			assert.Equal(t, http.StatusForbidden, code, "%s (%q), no such storage: %s", d.label, ct, body)
			assert.Equal(t, first, body, "%s (%q): a storage outside the root answers alike whether or not it is there", d.label, ct)
		}
	}

	// A storage named by id, with a bare path.
	run := func(id int64) (int, string) {
		return confRaw(t, f.srv.URL, tok, http.MethodPost, "/api/files/plugins/actions/echo/upper/run", "text/plain",
			[]byte(fmt.Sprintf(`{"storage_id":%d,"paths":["x.txt"]}`, id)))
	}
	code, first := run(arsiv.ID)
	require.Equal(t, http.StatusForbidden, code, first)
	code, body := run(arsiv.ID + 1000)
	assert.Equal(t, http.StatusForbidden, code, "a storage id that is not there: %s", body)
	assert.Equal(t, first, body)

	// Inside the root the same text/plain run is queued.
	code, body = confRaw(t, f.srv.URL, tok, http.MethodPost, "/api/files/plugins/actions/echo/upper/run", "text/plain",
		[]byte(`{"paths":["main://docs/a.txt"]}`))
	assert.Equal(t, http.StatusAccepted, code, body)
}

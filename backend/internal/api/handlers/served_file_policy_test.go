package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/share"
)

// Every door that hands a file's own bytes out from filex's origin — the
// explorer's preview and download, /api/files/read, a share's `?inline=1` and
// a shared folder's entries — answers an active kind (HTML, SVG, XML) with a
// script-less sandbox and a Content-Type it names itself, and a PDF, a
// picture, sound, video and plain text as before (the app link's exposed
// copies: public_exposed_copy_csp_test.go). One policy for all of them:
// httpx.ProtectServedFile.

type servedCase struct {
	name, mime, body string
	sandboxed        bool
}

var servedCases = []servedCase{
	{"page.html", "text/html", "<!doctype html><p>page</p>", true},
	{"pic.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"/>`, true},
	{"data.xml", "application/xml", "<a/>", true},
	{"doc.pdf", "application/pdf", "%PDF-1.4\n%%EOF\n", false},
	{"shot.png", "image/png", "\x89PNG\r\n\x1a\n", false},
	{"clip.mp4", "video/mp4", "....", false},
	{"notes.txt", "text/plain", "hello", false},
}

// seedTyped writes rel onto the storage's disk and catalogues it with its
// real type (seedServerFile records every file as text/plain).
func seedTyped(t *testing.T, store db.Store, rel, mime, content string) *model.Node {
	t.Helper()
	ctx := context.Background()
	st, err := store.GetStorageByName(ctx, "main")
	require.NoError(t, err)
	var cfg struct {
		Root string `json:"root"`
	}
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	full := filepath.Join(cfg.Root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	require.True(t, protocolsync.New(store, nil, nil, "test").Write(ctx, st, rel, int64(len(content)), mime))
	n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+rel))
	require.NoError(t, err)
	require.NotNil(t, n)
	return n
}

func servedGet(t *testing.T, u, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	require.NoError(t, err)
	if tok != "" {
		req.Header.Set("X-Filex-Token", tok)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

func assertServedPolicy(t *testing.T, where string, c servedCase, resp *http.Response) {
	t.Helper()
	require.Equal(t, http.StatusOK, resp.StatusCode, where)
	csp := resp.Header.Get("Content-Security-Policy")
	ct := resp.Header.Get("Content-Type")
	assert.NotEmpty(t, ct, "%s: a type is always named", where)
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"), where)
	if !c.sandboxed {
		// The kinds a person looks at every day keep their type and no sandbox.
		assert.Empty(t, httpx.InertPolicy(ct), "%s keeps a type the browser shows as itself (%s)", where, ct)
		assert.NotContains(t, csp, "sandbox", "%s is shown as itself", where)
		return
	}
	// An active file may be answered with a passive type (a driver that
	// records SVG as text/plain): with nosniff that shows as text. What may
	// never happen is an active type answered without the sandbox.
	if httpx.InertPolicy(ct) == "" {
		return
	}
	assert.Contains(t, csp, "sandbox", "%s must not run as a filex page", where)
	assert.Contains(t, csp, "default-src 'none'", where)
	assert.NotContains(t, csp, "allow-scripts", where)
	assert.NotContains(t, csp, "allow-same-origin", where)
}

func TestServedFiles_ExplorerPreviewDownloadAndRead(t *testing.T) {
	srv, _, store, tok := aiFixture(t)
	for _, c := range servedCases {
		n := seedTyped(t, store, "docs/"+c.name, c.mime, c.body)
		wire := url.QueryEscape("main://docs/" + c.name)
		for _, u := range []string{
			srv.URL + "/api/files/manager?action=preview&path=" + wire,
			srv.URL + "/api/files/manager?action=download&path=" + wire,
			srv.URL + fmt.Sprintf("/api/files/read?id=%d", n.ID),
			srv.URL + fmt.Sprintf("/api/files/read?storage=%d&path=%s", n.StorageID, url.QueryEscape("docs/"+c.name)),
		} {
			assertServedPolicy(t, strings.TrimPrefix(u, srv.URL), c, servedGet(t, u, tok))
		}
	}
}

func TestServedFiles_ShareInlineAndSharedFolderEntries(t *testing.T) {
	srv, _, store, _ := aiFixture(t)
	svc := share.NewService(store)
	ctx := context.Background()
	for _, c := range servedCases {
		n := seedTyped(t, store, "pub/"+c.name, c.mime, c.body)
		sh, err := svc.Create(ctx, share.CreateOpts{NodeID: n.ID})
		require.NoError(t, err)
		for _, q := range []string{"?inline=1", ""} {
			assertServedPolicy(t, "/s/<file>"+q+" "+c.name, c, servedGet(t, srv.URL+"/s/"+sh.Token+q, ""))
		}
	}
	st, err := store.GetStorageByName(ctx, "main")
	require.NoError(t, err)
	dir, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/pub"))
	require.NoError(t, err)
	require.NotNil(t, dir, "the folder is catalogued with its files")
	folder, err := svc.Create(ctx, share.CreateOpts{NodeID: dir.ID})
	require.NoError(t, err)
	for _, c := range servedCases {
		for _, q := range []string{"?inline=1", ""} {
			assertServedPolicy(t, "/s/<folder>/f/"+c.name+q, c, servedGet(t, srv.URL+"/s/"+folder.Token+"/f/"+c.name+q, ""))
		}
	}
}

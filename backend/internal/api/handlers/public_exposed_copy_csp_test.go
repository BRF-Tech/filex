package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// An app's public link hands its visitor COPIES the app exposed, from
// filex's own origin (GET /api/public/s/{token}/file/{ref}), inline unless
// `download=1`. The app chooses what it exposes and under which name and
// type, so every active kind carries a script-less sandbox; the kinds a
// browser shows as themselves (a PDF above all — the signing page exists to
// show one, and Chrome will not show a PDF in a sandboxed document) stay as
// they were. The policy is httpx.ProtectServedFile, shared with every other
// door that serves a file (served_file_policy_test.go).
func TestPublicApp_ExposedCopiesNeverRunAsFilexPages(t *testing.T) {
	_, store, _, st, root := newMutateFixture(t)
	node := fileNode(t, store, st, root, "doc.pdf", "anchor")
	ctx := context.Background()

	appsDir := t.TempDir()
	reg, err := wasmplugin.New(wasmplugin.Options{Store: store, Dir: appsDir, SecretKey: "0123456789abcdef0123456789abcdef"})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(context.Background()) })

	svc := share.NewService(store)
	svc.AttachSecret("0123456789abcdef0123456789abcdef")

	type copyFile struct {
		name, mime, body string
	}
	copies := []copyFile{
		{"page.html", "text/html; charset=utf-8", "<!doctype html><title>x</title><p>page</p>"},
		{"pic.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`},
		{"nameonly.html", "", "<!doctype html><p>typed by its name only</p>"},
		{"data.xml", "application/xml", "<a/>"},
		{"unknown.bin", "", "\x00\x01"},
		{"doc.pdf", "application/pdf", "%PDF-1.4\n%%EOF\n"},
		{"shot.png", "image/png", "\x89PNG\r\n\x1a\n"},
		{"clip.mp4", "video/mp4", "...."},
		{"notes.txt", "text/plain; charset=utf-8", "hello"},
	}
	files := make([]wasmplugin.PageFile, 0, len(copies))
	for i, c := range copies {
		files = append(files, wasmplugin.PageFile{Ref: "pub:" + strconv.Itoa(i), Name: c.name,
			File: strconv.Itoa(i) + "-" + c.name, Size: int64(len(c.body)), Mime: c.mime})
	}
	fj, err := json.Marshal(files)
	require.NoError(t, err)
	sh, err := svc.Create(ctx, share.CreateOpts{NodeID: node.ID, PluginID: 7, PageID: "signer", FilesJSON: string(fj)})
	require.NoError(t, err)
	pageDir := filepath.Join(appsDir, "public", strconv.FormatInt(sh.ID, 10))
	require.NoError(t, os.MkdirAll(pageDir, 0o700))
	for i, c := range copies {
		require.NoError(t, os.WriteFile(filepath.Join(pageDir, files[i].File), []byte(c.body), 0o600))
	}

	pub := handlers.NewPublicAPI(store, svc)
	pub.AttachApps(&handlers.AppPlugins{Registry: reg, Store: store})
	r := chi.NewRouter()
	r.Get("/api/public/s/{token}/file/{ref}", pub.File)

	get := func(ref, query string) *http.Response {
		rec := doGet(t, r, "/api/public/s/"+sh.Token+"/file/"+ref+query)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Result()
	}

	sandboxed := map[string]bool{"page.html": true, "pic.svg": true, "nameonly.html": true, "data.xml": true, "unknown.bin": true}
	for i, c := range copies {
		ref := "pub:" + strconv.Itoa(i)
		for _, q := range []string{"", "?download=1"} {
			res := get(ref, q)
			csp := res.Header.Get("Content-Security-Policy")
			assert.NotEmpty(t, res.Header.Get("Content-Type"), "%s%s: a type is always said, never left to the browser", c.name, q)
			assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"), c.name)
			if sandboxed[c.name] {
				assert.Contains(t, csp, "sandbox", "%s%s must not run as a filex page", c.name, q)
				assert.NotContains(t, csp, "allow-scripts", c.name)
				assert.NotContains(t, csp, "allow-same-origin", c.name)
				assert.Contains(t, csp, "default-src 'none'", c.name)
			} else {
				assert.Empty(t, csp, "%s%s is shown as itself and needs no sandbox", c.name, q)
			}
		}
	}

	// A copy with no recorded type is typed from its name, never left for
	// net/http to guess after the policy decision was made.
	assert.Equal(t, "text/html; charset=utf-8", get("pub:2", "").Header.Get("Content-Type"))
	assert.Equal(t, "application/octet-stream", get("pub:4", "").Header.Get("Content-Type"))
}

package handlers_test

// A path segment a client ENCODED must reach the handler DECODED.
//
// chi routes on r.URL.RawPath whenever the client's escaping differs from the
// one Go would have written, and then hands every route parameter back still
// escaped. The public link surfaces read a parameter that is not a hex token
// in two places — the copy an app page exposed (`/file/{ref}`, ref "pub:N")
// and a file inside a shared folder (`/s/{token}/f/*`) — and both failed for
// exactly the characters a careful client escapes and Go does not.
//
// ⚠ Measured through api.BuildRouter, not a hand-built router with the
// parameters injected: the defect lives in how chi picked the path it routed
// on, and a test that sets rctx.URLParams itself cannot see that at all.

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// ⭐ The outside signer's "See and approve" step. The SPA asks for the exposed
// copy with encodeURIComponent("pub:0") = "pub%3A0" (packages/core
// usePublicLink → fileUrl); before the fix that was a 404 and the page said
// "The document could not be loaded", while the literal "pub:0" the no-JS
// page links to answered 200. Both spellings name the same copy.
func TestPublicFile_AnEncodedRefIsTheSameCopy(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.seedDoc(t, "docs/terms.txt")
	token := f.openSigningLink(t, id, "docs/terms.txt")

	for _, tail := range []string{"/file/pub:0", "/file/pub%3A0", "/file/pub%3a0"} {
		status, raw := f.visitorGet(t, token, tail)
		require.Equal(t, http.StatusOK, status, "%s: %s", tail, raw)
		assert.Equal(t, "the terms", string(raw), "%s served other bytes", tail)
	}
	// ⚠ Decoding is not a way round the ref check: an encoded name that is
	// not an exposed copy is still not one.
	status, _ := f.visitorGet(t, token, "/file/pub%3A9")
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = f.visitorGet(t, token, "/file/..%2F..%2Fetc")
	assert.Equal(t, http.StatusNotFound, status)
}

// ⭐ A file in a shared folder whose name carries a character Go's path
// encoding leaves alone but a client escapes. The SPA builds these links with
// encodeURIComponent per segment (usePublicLink → shareDownloadUrl) and the
// no-JS page with url.PathEscape (share_browse.go → escapePathSegments); both
// escape `&` / `,` and both got a 404 before.
//
// The literal `%` case is the other half of the rule: when nothing forced
// chi onto the raw path the parameter is ALREADY decoded, and decoding it
// again would serve "100A.txt" for a request about "100%41.txt".
func TestPublicFolderFile_EncodedNamesAreServed(t *testing.T) {
	f := newAppFixture(t, nil)
	files := map[string]string{
		"Q&A notes.txt":    "questions",
		"a,b.txt":          "comma",
		"100%41.txt":       "a literal percent",
		"100A.txt":         "the decoy",
		"alt/x;y=z@ş.txt":  "reserved and Turkish",
		"alt/plain one.md": "plain",
	}
	for rel, body := range files {
		f.writeFile(t, "album/"+rel, body)
	}
	ctx := context.Background()
	node, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "album", Path: "/album",
		PathHash: pathkey.Hash(f.st.ID, "/album"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	sh, err := share.NewService(f.store).Create(ctx, share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)

	// As the SPA spells them: encodeURIComponent on every segment.
	spa := map[string]string{
		"Q%26A%20notes.txt":            "questions",
		"a%2Cb.txt":                    "comma",
		"100%2541.txt":                 "a literal percent",
		"alt/x%3By%3Dz%40%C5%9F.txt":   "reserved and Turkish",
		"alt/plain%20one.md":           "plain",
		"Q%26A%20notes.txt?download=1": "questions",
	}
	for tail, want := range spa {
		status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/s/"+sh.Token+"/f/"+tail, nil)
		require.Equal(t, http.StatusOK, status, "%s: %s", tail, raw)
		assert.Equal(t, want, string(raw), "%s served other bytes", tail)
	}

	// As the no-JS page spells them: whatever href it printed must open.
	status, page := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/s/"+sh.Token, nil)
	require.Equal(t, http.StatusOK, status, string(page))
	hrefs := regexp.MustCompile(`href="(/s/`+sh.Token+`/f/[^"?]+)`).FindAllStringSubmatch(string(page), -1)
	require.NotEmpty(t, hrefs, "the folder page links no file")
	for _, m := range hrefs {
		href := strings.ReplaceAll(m[1], "&amp;", "&")
		status, raw := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+href, nil)
		assert.Equal(t, http.StatusOK, status, "the page's own link %s: %s", href, raw)
	}
	var sawComma bool
	for _, m := range hrefs {
		sawComma = sawComma || strings.Contains(m[1], "a%2Cb.txt")
	}
	assert.True(t, sawComma, "the page no longer escapes the comma, so this case proves nothing: %v", hrefs)

	// ⚠ A decoded path is still contained: an escaped "../" cannot climb out.
	for _, tail := range []string{"..%2F..%2Fdocs", "%2E%2E/%2E%2E/etc/passwd"} {
		status, _ := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/s/"+sh.Token+"/f/"+tail, nil)
		assert.Equal(t, http.StatusNotFound, status, tail)
	}
}

// ⭐ The signing link on a document the catalogue has not seen yet, through
// the real sink (protocolsync). A file dropped on the storage outside filex is
// listed by the explorer before the next sync and the action gate stats it on
// the driver, so a person could ask for signatures on it — and the job failed
// "no such file" at share_create, because a share points at a catalogue row.
// Measured 2026-09-21 on a live instance before the fix.
func TestSigningLink_OnADocumentTheCatalogueHasNotSeen(t *testing.T) {
	f := newAppFixture(t, nil)
	id := f.installEcho(t)
	f.writeFile(t, "docs/fresh.txt", "the terms") // bytes only: no node row
	hash := pathkey.Hash(f.st.ID, "/docs/fresh.txt")
	before, _ := f.store.GetNodeByPath(context.Background(), f.st.ID, hash)
	require.Nil(t, before, "the fixture must start with the catalogue behind the storage")

	token := f.openSigningLink(t, id, "docs/fresh.txt")

	node, err := f.store.GetNodeByPath(context.Background(), f.st.ID, hash)
	require.NoError(t, err)
	require.NotNil(t, node, "the document the link is about has a catalogue row now")
	assert.Equal(t, int64(len("the terms")), node.Size, "recorded with the storage's own size")
	sh, err := f.store.GetShareByToken(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, node.ID, sh.NodeID)
	status, raw := f.visitorGet(t, token, "/file/pub%3A0")
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Equal(t, "the terms", string(raw))
}

// ⭐ The Share dialog on a file the catalogue has not seen yet — the same
// lagging catalogue, the other door. Measured 2026-09-21 before the fix:
// POST /api/files/share {path} → 404 "file not found" for a file the explorer
// was listing (from the driver). Now it records the file and shares it.
//
// ⚠ And only for somebody who could have shared it: a viewer is refused with
// the same 404 a missing file gets, and NOTHING is written — the row is
// created after the tenant, root and editor checks, never before.
func TestShareDialog_OnAFileTheCatalogueHasNotSeen(t *testing.T) {
	f := newAppFixture(t, nil)
	f.writeFile(t, "inbox/fresh.txt", "the terms") // bytes only: no node row
	hash := pathkey.Hash(f.st.ID, "/inbox/fresh.txt")

	// A viewer first: refused, and the catalogue is left as it was.
	u := seedSharedUser(t, f.store, "viewer@test.local", "ViewerPass1!")
	grant(t, f.store, f.st, u, "", model.GrantViewer, true)
	viewer := freshClient(t)
	testutil.LoginAs(t, f.srv, viewer, "viewer@test.local", "ViewerPass1!")
	status, raw := doReq(t, viewer, http.MethodPost, f.srv.URL+"/api/files/share", map[string]any{"path": "main://inbox/fresh.txt"})
	assert.Equal(t, http.StatusNotFound, status, string(raw))
	n, _ := f.store.GetNodeByPath(context.Background(), f.st.ID, hash)
	assert.Nil(t, n, "a caller who may not share the file wrote a catalogue row for it")

	// The administrator: shared, on a row that now exists.
	status, raw = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/share", map[string]any{"path": "main://inbox/fresh.txt"})
	require.Equal(t, http.StatusOK, status, string(raw))
	n, err := f.store.GetNodeByPath(context.Background(), f.st.ID, hash)
	require.NoError(t, err)
	require.NotNil(t, n, "sharing recorded the file")
	var created struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	require.NotEmpty(t, created.Token, string(raw))
	status, body := doReq(t, freshClient(t), http.MethodGet, f.srv.URL+"/s/"+created.Token+"?download=1", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	assert.Equal(t, "the terms", string(body))

	// A path that is not on the storage either is still "file not found".
	status, _ = doReq(t, f.admin, http.MethodPost, f.srv.URL+"/api/files/share", map[string]any{"path": "main://inbox/nothing.txt"})
	assert.Equal(t, http.StatusNotFound, status)
}

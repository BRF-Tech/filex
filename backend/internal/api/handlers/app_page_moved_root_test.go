package handlers_test

// A link's document moved out of the root after the link was opened (filex
// Roadmap #185, what #161 left).
//
// A link a `root:` token's job opens records that root, and the visitor's
// screen and the job the visitor's submit queues are held to it. Both are
// handed the link's document where it lies NOW - the share points at a node,
// and the node follows a move - and the document was judged against the root
// once, when the link was opened. Moved out of the root afterwards, the page
// was still handed it, its ref still named it (a notice pointed at it), and a
// visitor's submit queued a job on it, as the link's creator, outside the
// root. The ref is judged against the link's root on every use now, and the
// door refuses the submit.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// moveFile moves a catalogued file on the fixture's storage and in the
// catalogue, as somebody tidying the folders after the link was mailed would.
func (f *appFixture) moveFile(t *testing.T, from, to string) {
	t.Helper()
	ctx := context.Background()
	n, err := f.store.GetNodeByPath(ctx, f.st.ID, pathkey.Hash(f.st.ID, "/"+from))
	require.NoError(t, err)
	require.NotNil(t, n, "%s is not catalogued", from)
	var parent *int64
	if dir, derr := f.store.GetNodeByPath(ctx, f.st.ID, pathkey.Hash(f.st.ID, "/"+path.Dir(to))); derr == nil && dir != nil {
		parent = &dir.ID
	}
	require.NoError(t, f.store.MoveNode(ctx, n.ID, parent, path.Base(to), "/"+to, pathkey.Hash(f.st.ID, "/"+to)))
	dst := filepath.Join(f.root, filepath.FromSlash(to))
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	require.NoError(t, os.Rename(filepath.Join(f.root, filepath.FromSlash(from)), dst))
}

// visitorOpen is a stranger opening the link's page: echo's opening surface
// says how many files the page was handed (`inputs=`) - the link's document
// and the copy it exposed.
func (f *appFixture) visitorOpen(t *testing.T, token string) string {
	t.Helper()
	code, raw := doReq(t, freshClient(t), http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/event",
		map[string]any{"event": "open"})
	require.Equal(t, http.StatusOK, code, string(raw))
	var ans struct {
		Surface struct {
			Nodes []struct {
				Props struct {
					Text map[string]string `json:"text"`
				} `json:"props"`
			} `json:"nodes"`
		} `json:"surface"`
	}
	require.NoError(t, json.Unmarshal(raw, &ans))
	require.NotEmpty(t, ans.Surface.Nodes, string(raw))
	return ans.Surface.Nodes[0].Props.Text["en"]
}

func TestAppPage_ALinksDocumentMovedOutOfTheRootIsNoLongerReachedThroughIt(t *testing.T) {
	f, tok := rootConfinedApp(t)
	// The server's rule: a person sees what their ACL gives - here the whole
	// storage - and the visitor nothing.
	f.reg.SetVisibility(func(_ context.Context, u *model.User, _ int64, _ string) bool { return u != nil })
	sent := &pageNotices{}
	f.reg.SetNotify(sent)
	p, ok := f.reg.ByName("echo")
	require.True(t, ok)

	link := openSigningLinkWithToken(t, f, tok, p.Row.ID, "docs/a.txt")
	require.Contains(t, f.visitorOpen(t, link), "inputs=2", "inside the root the page is handed its document")

	f.moveFile(t, "docs/a.txt", "secret/a.txt")
	sent.events = nil

	// The screen: not handed the document, and the ref that named it is no
	// ref - no notice points at the file outside the root.
	assert.Contains(t, f.visitorOpen(t, link), "inputs=1", "only the copy the link exposed")
	got := f.visitorProbe(t, link, "secret/s.txt")
	assert.Contains(t, got, "ref=not_found", "the page's own ref no longer reaches a document outside the root")
	assert.NotContains(t, got, "secret")
	assert.False(t, sent.on("secret/a.txt"), "no notice points at the document outside the root")

	// The submit: refused at the door, with the sentence a creator who lost
	// their access gets, and nothing is queued or written.
	code, raw := f.visitorSubmit(t, link, nil)
	assert.Equal(t, http.StatusForbidden, code, string(raw))
	assert.Contains(t, string(raw), "no_access")
	_, serr := os.Stat(filepath.Join(f.root, "secret", "a-upper.txt"))
	assert.True(t, os.IsNotExist(serr), "nothing was written beside it: %v", serr)

	// A link an unconfined session opened follows its document anywhere, as
	// before.
	open := f.openSigningLink(t, p.Row.ID, "docs/b.txt")
	f.moveFile(t, "docs/b.txt", "secret/b.txt")
	assert.Contains(t, f.visitorOpen(t, open), "inputs=2")
	code, raw = f.visitorSubmit(t, open, nil)
	assert.Equal(t, http.StatusAccepted, code, "a link with no root behind it is held to none: %s", raw)
}

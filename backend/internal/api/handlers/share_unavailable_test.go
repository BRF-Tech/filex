package handlers_test

// Issue #104, the public side: a link minted BEFORE its target became an
// entry the storage could not answer for. Making a new link on such an entry
// is refused (HandleCreate); the links already out there served whatever the
// storage driver answered - its own error, in English, or stale bytes - to a
// visitor who could do nothing with it. The share page, its no-JS pages and
// its downloads now read the mark and say so in the visitor's language.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

const shareNoAnswer = "plugin: stat is not implemented for folders (http 500)"

func markUnavailable(t *testing.T, store db.Store, id int64) {
	t.Helper()
	changed, err := store.MarkNodeUnavailable(context.Background(), id, shareNoAnswer)
	require.NoError(t, err)
	require.True(t, changed)
}

func dirNode(t *testing.T, store db.Store, st *model.Storage, p string) *model.Node {
	t.Helper()
	n, err := store.CreateNode(context.Background(), &model.Node{
		StorageID: st.ID, Name: p[1:], Path: p,
		PathHash: mutTestPathHash(st.ID, p), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	return n
}

func getIn(t *testing.T, r http.Handler, url, lang string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Accept-Language", lang)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestShareLink_AnUnavailableFileIsSaidInTheVisitorsLanguage(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	node := fileNode(t, store, st, root, "rapor.txt", "the bytes")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)
	markUnavailable(t, store, node.ID)

	for _, lang := range []string{"tr", "en"} {
		rec := getIn(t, r, "/s/"+sh.Token, lang)
		assert.Equal(t, http.StatusConflict, rec.Code, "%s: %s", lang, rec.Body.String())
		assert.Contains(t, rec.Header().Get("Content-Type"), "text/html", lang)
		assert.Contains(t, rec.Body.String(), srvtext.Text(lang, "server.public.err_unavailable_title", nil), lang)
		assert.NotContains(t, rec.Body.String(), "the bytes", "%s: the link served the bytes of an entry nobody can vouch for", lang)
		assert.NotContains(t, rec.Body.String(), shareNoAnswer, "%s: the storage's own words are the owner's, not the visitor's", lang)
	}
	assert.NotEqual(t, srvtext.Text("en", "server.public.err_unavailable_title", nil),
		srvtext.Text("tr", "server.public.err_unavailable_title", nil), "the page has a Turkish title to say")
}

// A file whose FOLDER the storage could not answer for: the link names the
// file, the mark is on a row above it.
func TestShareLink_AFileInsideAnUnavailableFolderIsRefusedToo(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	folder := dirNode(t, store, st, "/Proje")
	node := fileNode(t, store, st, root, "Proje/a.txt", "inside")
	sh, err := svc.Create(context.Background(), share.CreateOpts{NodeID: node.ID})
	require.NoError(t, err)
	markUnavailable(t, store, folder.ID)

	rec := getIn(t, r, "/s/"+sh.Token, "en")
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "inside")
}

// The page's state says so before anything is pressed, so the shell offers
// no download that would fail; an ordinary link says nothing of the kind.
func TestShareLink_TheStateSaysUnavailable(t *testing.T) {
	r, svc, store, st, root := newPublicFixture(t)
	marked := fileNode(t, store, st, root, "marked.txt", "x")
	plain := fileNode(t, store, st, root, "plain.txt", "y")
	shMarked, err := svc.Create(context.Background(), share.CreateOpts{NodeID: marked.ID})
	require.NoError(t, err)
	shPlain, err := svc.Create(context.Background(), share.CreateOpts{NodeID: plain.ID})
	require.NoError(t, err)
	markUnavailable(t, store, marked.ID)

	rec := doGet(t, r, "/api/public/s/"+shMarked.Token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := publicDecode(t, rec)
	assert.Equal(t, true, body["unavailable"], rec.Body.String())
	assert.Equal(t, false, body["revoked"], "the link is alive: the entry may answer again")
	assert.NotContains(t, rec.Body.String(), shareNoAnswer)

	rec = doGet(t, r, "/api/public/s/"+shPlain.Token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, present := publicDecode(t, rec)["unavailable"]
	assert.False(t, present, "an ordinary link carries no unavailable key")

	// And its download is still the bytes.
	rec = getIn(t, r, "/s/"+shPlain.Token, "en")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "y", rec.Body.String())
}

package handlers_test

// A cut search answer, for a caller confined to a folder (filex Roadmap #185).
//
// Both search boxes say `truncated` when more matched than came back. The
// flag was judged by every hit the index or the fallback returned, the ones
// outside the root included, while those hits themselves were held back: a
// token confined to one folder was told "there is more" by a page or a window
// that files OUTSIDE its folder had filled. The flag is judged by the hits
// inside the root alone now; an unconfined caller's is what it was.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// rootSearchFixture is one storage, `main`, an administrator's token confined
// to main://in and an unconfined one; with an index when asked.
type rootSearchFixture struct {
	base     string
	store    db.Store
	idx      *search.Index
	st       *model.Storage
	confined string
	open     string
}

func newRootSearchFixture(t *testing.T, withIndex bool) *rootSearchFixture {
	t.Helper()
	f := &rootSearchFixture{}
	var deps func(*api.Deps)
	if withIndex {
		idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx.Close() })
		f.idx = idx
		deps = func(d *api.Deps) { d.Index = idx }
	}
	srv, _, store := testutil.NewTestServerWith(t, nil, deps)
	useProductionAuthChain(t, store)
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/main",
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	uid, _ := testutil.SeedAdminUser(t, store)
	f.base, f.store, f.st = srv.URL, store, st
	f.confined = testutil.NewAPIToken(t, store, uid, "read,root:main://in")
	f.open = testutil.NewAPIToken(t, store, uid, "read")
	return f
}

// file catalogues a file at rel (and indexes it when there is an index).
func (f *rootSearchFixture) file(t *testing.T, rel string) *model.Node {
	t.Helper()
	p := "/" + rel
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.st.ID, Name: filepath.Base(rel), Path: p, PathHash: pathkey.Hash(f.st.ID, p),
		Type: model.NodeTypeFile, SyncState: model.SyncStateSynced, Mime: "text/plain", Size: 1,
	})
	require.NoError(t, err)
	if f.idx != nil {
		require.NoError(t, f.idx.IndexNode(context.Background(), n))
	}
	return n
}

// search is POST /api/files/search with tok: the names that came back and
// whether the answer says it was cut.
func (f *rootSearchFixture) search(t *testing.T, tok string, body map[string]any) ([]string, bool) {
	t.Helper()
	status, raw := fxJSON(t, http.MethodPost, f.base+"/api/files/search", tok, body)
	require.Equal(t, http.StatusOK, status, raw)
	var got struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
		Truncated *bool `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.NotNil(t, got.Truncated, raw)
	names := make([]string, 0, len(got.Results))
	for _, r := range got.Results {
		names = append(names, r.Name)
	}
	return names, *got.Truncated
}

// The index's page: three hits asked for, one of them inside the root.
func TestSearch_AConfinedCallersPageIsCutOnlyByHitsInsideItsRoot(t *testing.T) {
	f := newRootSearchFixture(t, true)
	f.file(t, "in/ledger.txt")
	for i := 0; i < 6; i++ {
		f.file(t, fmt.Sprintf("out/ledger-%d.txt", i))
	}

	names, cut := f.search(t, f.confined, map[string]any{"query": "ledger", "limit": 3})
	assert.Equal(t, []string{"ledger.txt"}, names)
	assert.False(t, cut, "a page the hits outside the root filled is not a cut answer to a caller confined to main://in")

	names, cut = f.search(t, f.open, map[string]any{"query": "ledger", "limit": 3})
	assert.Len(t, names, 3)
	assert.True(t, cut, "unconfined, a full page is a cut answer, as before")
}

// The fallback's window (no index): one row past it, outside the root.
func TestSearch_AConfinedCallersFallbackWindowIsFullOnlyOfRowsInsideItsRoot(t *testing.T) {
	f := newRootSearchFixture(t, false)
	f.file(t, "in/ledger.txt")
	for i := 0; i < 6; i++ {
		f.file(t, fmt.Sprintf("out/ledger-%d.txt", i))
	}
	ask := map[string]any{"query": "ledger", "limit": 1, "storage_id": f.st.ID}

	names, cut := f.search(t, f.confined, ask)
	assert.Equal(t, []string{"ledger.txt"}, names)
	assert.False(t, cut, "a window the rows outside the root filled is not a cut answer to a caller confined to main://in")

	_, cut = f.search(t, f.open, ask)
	assert.True(t, cut, "unconfined, seven rows do not fit a window of four, as before")
}

// A bare `tag:` listing: a tagged file left over outside the root.
func TestSearch_AConfinedCallersTagListingIsCutOnlyByFilesInsideItsRoot(t *testing.T) {
	f := newRootSearchFixture(t, false)
	for i := 0; i < 3; i++ {
		testutil.TagNode(t, f.store, f.file(t, fmt.Sprintf("out/kart-%d.txt", i)).ID, 0, "kirmizi")
	}
	// The listing is newest first, by the second: the file inside the root is
	// made a second later, so it comes first and the others are left over.
	time.Sleep(1100 * time.Millisecond)
	testutil.TagNode(t, f.store, f.file(t, "in/kart.txt").ID, 0, "kirmizi")

	names, cut := f.search(t, f.confined, map[string]any{"query": "tag:kirmizi", "limit": 1})
	assert.Equal(t, []string{"kart.txt"}, names)
	assert.False(t, cut, "files left over outside the root do not cut a confined caller's listing")

	names, cut = f.search(t, f.open, map[string]any{"query": "tag:kirmizi", "limit": 1})
	assert.Equal(t, []string{"kart.txt"}, names)
	assert.True(t, cut, "unconfined, three are left over, as before")
}

// The explorer's own search box (GET /api/files/manager?action=search): its
// page of 250 index hits.
func TestManagerSearch_AConfinedCallersPageIsCutOnlyByHitsInsideItsRoot(t *testing.T) {
	f := newRootSearchFixture(t, true)
	f.file(t, "in/ledger.txt")
	for i := 0; i < 260; i++ {
		f.file(t, fmt.Sprintf("out/ledger-%03d.txt", i))
	}
	ask := func(tok string) managerSearchBody {
		t.Helper()
		status, raw := fxJSON(t, http.MethodGet, f.base+"/api/files/manager?action=search&path="+url.QueryEscape("main://")+
			"&filter=ledger", tok, nil)
		require.Equal(t, http.StatusOK, status, raw)
		var body managerSearchBody
		require.NoError(t, json.Unmarshal([]byte(raw), &body))
		require.NotNil(t, body.Truncated, raw)
		return body
	}

	got := ask(f.confined)
	assert.Equal(t, []string{"ledger.txt"}, basenames(got))
	assert.False(t, *got.Truncated, "a page the hits outside the root filled is not a cut answer to a caller confined to main://in")

	got = ask(f.open)
	assert.Len(t, got.Files, 250)
	assert.True(t, *got.Truncated, "unconfined, a full page is a cut answer, as before")
}

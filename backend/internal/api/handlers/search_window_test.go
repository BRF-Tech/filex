package handlers_test

// The search window: what a search returns when more rows match than it may
// return.
//
// Without the index (FILEX_SEARCH_ENABLED=0, or an index that failed to open)
// both search boxes answer from Store.SearchNodes, whose LIMIT used to cut the
// rows in `ORDER BY name` before anything was ranked. Measured before the fix,
// on this harness: 1,001 files called `a-report-NNNN.txt` next to one called
// `report.txt` — the toolbar's search for `report` returned 1,000 rows, the
// first of them `Zeta-report.txt` (capitals sort first), and not `report.txt`
// at all. Nothing on screen said the list had been cut.
//
// testutil's server has no index, so these exercise the fallback; the last
// test wires one for the index path's own page limit.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// windowStorage creates a storage and catalogues the given file names at its
// root. Returns the storage and the created nodes.
func windowStorage(t *testing.T, store db.Store, name string, files []string) (*model.Storage, []*model.Node) {
	t.Helper()
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	nodes := make([]*model.Node, 0, len(files))
	for _, f := range files {
		p := "/" + f
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: f, Path: p, PathHash: pathkey.Hash(st.ID, p),
			Type: model.NodeTypeFile, SyncState: model.SyncStateSynced, Mime: "text/plain", Size: 1,
		})
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	return st, nodes
}

// crowd returns n names that all contain `report` and all sort before
// `report.txt` by name, plus the two the tests look for.
func crowd(n int) []string {
	out := make([]string, 0, n+2)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("a-report-%04d.txt", i))
	}
	return append(out, "Zeta-report.txt", "report.txt")
}

type managerSearchBody struct {
	Files []struct {
		Basename string `json:"basename"`
	} `json:"files"`
	Truncated *bool `json:"truncated"`
}

func managerSearch(t *testing.T, client *http.Client, base, path, filter string) managerSearchBody {
	t.Helper()
	resp, err := client.Get(base + "/api/files/manager?action=search&path=" + url.QueryEscape(path) +
		"&filter=" + url.QueryEscape(filter))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body managerSearchBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func basenames(b managerSearchBody) []string {
	out := make([]string, 0, len(b.Files))
	for _, f := range b.Files {
		out = append(out, f.Basename)
	}
	return out
}

func signedInAdmin(t *testing.T) (base string, client *http.Client, store db.Store) {
	t.Helper()
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	return srv.URL, client, store
}

// TestManagerSearch_FallbackRanksBeforeItCuts — one storage, so the fallback
// asks for its 1,000-row window.
func TestManagerSearch_FallbackRanksBeforeItCuts(t *testing.T) {
	base, client, store := signedInAdmin(t)
	windowStorage(t, store, "main", crowd(1001))

	got := managerSearch(t, client, base, "main://", "report")
	require.NotEmpty(t, got.Files)
	assert.Equal(t, "report.txt", got.Files[0].Basename,
		"the file called exactly what was typed comes first, however late it sorts by name")
	require.NotNil(t, got.Truncated, "a search response says whether it was cut")
	assert.True(t, *got.Truncated, "1,003 matches do not fit a 1,000-row window, and the answer has to say so")
	assert.Len(t, got.Files, 1000)

	// A search that fits says so too.
	one := managerSearch(t, client, base, "main://", "zeta")
	assert.Equal(t, []string{"Zeta-report.txt"}, basenames(one))
	require.NotNil(t, one.Truncated)
	assert.False(t, *one.Truncated)

	// ⚠ What "cut" means: the window was full, so rows past it were never
	// read. Every word of a query is a condition in the database query (PR
	// #46), so a window fills only with rows holding every word — here all
	// 1,002 that hold `a` and `report` — and none of them answers the whole
	// piece `a_report` (the underscore is a letter). Nothing comes back, and
	// the answer still says it may not be everything: the server cannot know.
	cut := managerSearch(t, client, base, "main://", "a_report")
	assert.Empty(t, basenames(cut))
	require.NotNil(t, cut.Truncated)
	assert.True(t, *cut.Truncated, "a full window is a cut window, however few of its rows survive")

	// A query the database can answer exactly is not cut. Before PR #46 only
	// its longest word, `report`, reached the database: the window filled
	// with 1,000 rows, one survived, and the answer said it had been cut.
	narrow := managerSearch(t, client, base, "main://", "a-report-0007")
	assert.Equal(t, []string{"a-report-0007.txt"}, basenames(narrow))
	require.NotNil(t, narrow.Truncated)
	assert.False(t, *narrow.Truncated, "every word reached the database; no row was left unread")
}

// TestManagerSearch_CrossStorageFallbackRanksBeforeItCuts — more than one
// storage and a search at a storage root walks every storage, each with its
// own 400-row window.
func TestManagerSearch_CrossStorageFallbackRanksBeforeItCuts(t *testing.T) {
	base, client, store := signedInAdmin(t)
	windowStorage(t, store, "main", crowd(401))
	windowStorage(t, store, "notes", []string{"report-2026.md", "unrelated.md"})

	got := managerSearch(t, client, base, "main://", "report")
	names := basenames(got)
	require.GreaterOrEqual(t, len(names), 2)
	assert.Equal(t, "report.txt", names[0])
	assert.Contains(t, names, "report-2026.md", "the other storage's prefix match is kept")
	require.NotNil(t, got.Truncated)
	assert.True(t, *got.Truncated, "403 matches in one storage do not fit its 400-row window")
}

// TestFilesSearch_FallbackRanksBeforeItCuts — /api/files/search ranked its
// fallback only AFTER keeping the first `limit` rows by name, so with a small
// limit the exact match never had a chance.
func TestFilesSearch_FallbackRanksBeforeItCuts(t *testing.T) {
	base, client, store := signedInAdmin(t)
	st, _ := windowStorage(t, store, "main", crowd(1001))

	var body struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
		Truncated *bool `json:"truncated"`
	}
	get := func(q string, limit int) {
		t.Helper()
		body.Results, body.Truncated = nil, nil
		resp, err := client.Get(base + "/api/files/search?q=" + url.QueryEscape(q) +
			"&storage_id=" + strconv.FormatInt(st.ID, 10) + "&limit=" + strconv.Itoa(limit))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	}

	get("report", 5)
	require.Len(t, body.Results, 5)
	assert.Equal(t, "report.txt", body.Results[0].Name)
	require.NotNil(t, body.Truncated)
	assert.True(t, *body.Truncated)

	get("zeta", 5)
	require.Len(t, body.Results, 1)
	assert.Equal(t, "Zeta-report.txt", body.Results[0].Name)
	require.NotNil(t, body.Truncated)
	assert.False(t, *body.Truncated)
}

// TestManagerSearch_IndexPageLimitIsSaid — with the index, the toolbar asks for
// 250 hits. A full page is a cut answer and says so; a short one does not.
func TestManagerSearch_IndexPageLimitIsSaid(t *testing.T) {
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { d.Index = idx })
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	files := make([]string, 0, 261)
	for i := 0; i < 260; i++ {
		files = append(files, fmt.Sprintf("ledger-%03d.txt", i))
	}
	files = append(files, "memo.txt")
	_, nodes := windowStorage(t, store, "main", files)
	for _, n := range nodes {
		require.NoError(t, idx.IndexNode(context.Background(), n))
	}

	full := managerSearch(t, client, srv.URL, "main://", "ledger")
	assert.Len(t, full.Files, 250)
	require.NotNil(t, full.Truncated)
	assert.True(t, *full.Truncated, "a full page of index hits is a cut answer")

	short := managerSearch(t, client, srv.URL, "main://", "memo")
	assert.Equal(t, []string{"memo.txt"}, basenames(short))
	require.NotNil(t, short.Truncated)
	assert.False(t, *short.Truncated)
}

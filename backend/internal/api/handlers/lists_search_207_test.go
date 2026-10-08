package handlers_test

// filex 0.54, task #207: lists and search answered by the server.
//
//   - D6: a search's narrowing (type, size, dates, folder, owner, hidden)
//     travels with the query and is applied BEFORE the limit counts a row.
//     The explorer used to narrow the first N hits in the browser.
//   - D4: every list and search row says `starred` itself; the explorer
//     used to fetch the first 500 stars and match ids.
//   - D3: Recent and Starred come in the person's own order (opened_at,
//     starred_at), with total / offset / truncated.
//   - D5: the folder filter asks the server's name rule (POST
//     /api/files/search/match); the admin Users list takes ?q=.
//   - D7: /api/files/search answers total, truncated and score, and takes
//     type=file.
//   - Y3: a folder listing comes back folders first, by name, whichever path
//     built it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

type hit207 struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Size    int64    `json:"size"`
	Kind    string   `json:"kind"`
	Starred bool     `json:"starred"`
	Score   *float64 `json:"score"`
}

type search207 struct {
	Results   []hit207 `json:"results"`
	Truncated *bool    `json:"truncated"`
	Total     *int     `json:"total"`
}

func filesSearch207(t *testing.T, client *http.Client, base string, q url.Values) (int, search207) {
	t.Helper()
	resp, err := client.Get(base + "/api/files/search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	var body search207
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	}
	return resp.StatusCode, body
}

// sizedStorage catalogues files of the given sizes at a new storage's root.
func sizedStorage(t *testing.T, store db.Store, name string, sizes map[string]int64, dirs ...string) *model.Storage {
	t.Helper()
	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name,
		SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
	})
	require.NoError(t, err)
	for f, size := range sizes {
		p := "/" + f
		_, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: f, Path: p, PathHash: pathkey.Hash(st.ID, p),
			Type: model.NodeTypeFile, SyncState: model.SyncStateSynced, Mime: "text/plain", Size: size,
		})
		require.NoError(t, err)
	}
	for _, d := range dirs {
		p := "/" + d
		_, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: d, Path: p, PathHash: pathkey.Hash(st.ID, p),
			Type: model.NodeTypeDirectory, SyncState: model.SyncStateSynced,
		})
		require.NoError(t, err)
	}
	return st
}

// TestSearch207_NarrowingCountsBeforeTheLimit: thirty small reports and three
// big ones, `min_size` 1 MiB, limit 2. Before 0.54 the server ignored
// min_size and answered the first two name hits - small ones - and the
// browser's narrowing then had nothing left to show.
func TestSearch207_NarrowingCountsBeforeTheLimit(t *testing.T) {
	base, client, store := signedInAdmin(t)
	sizes := map[string]int64{}
	for i := 0; i < 30; i++ {
		sizes[fmt.Sprintf("report-small-%02d.txt", i)] = 10
	}
	for i := 0; i < 3; i++ {
		sizes[fmt.Sprintf("report-big-%d.txt", i)] = 5 << 20
	}
	st := sizedStorage(t, store, "main", sizes)

	q := url.Values{"q": {"report"}, "storage_id": {fmt.Sprint(st.ID)}, "limit": {"2"}, "min_size": {"1048576"}}
	code, got := filesSearch207(t, client, base, q)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, got.Results, 2)
	for _, h := range got.Results {
		assert.GreaterOrEqual(t, h.Size, int64(1<<20), "%s is under min_size: the narrowing ran after the limit", h.Name)
	}
	require.NotNil(t, got.Truncated)
	assert.True(t, *got.Truncated, "three big reports do not fit a page of two")
	require.NotNil(t, got.Total)
	assert.Equal(t, 3, *got.Total)

	q.Set("limit", "5")
	_, all := filesSearch207(t, client, base, q)
	assert.Len(t, all.Results, 3)
	require.NotNil(t, all.Truncated)
	assert.False(t, *all.Truncated)
}

// TestSearch207_TypeFileDropsFolders: the admin Files page asked for files and
// dropped folders out of a page of 50 in the browser (audit D7).
func TestSearch207_TypeFileDropsFolders(t *testing.T) {
	base, client, store := signedInAdmin(t)
	st := sizedStorage(t, store, "main", map[string]int64{"plans.txt": 1}, "plans-archive")

	q := url.Values{"q": {"plans"}, "storage_id": {fmt.Sprint(st.ID)}}
	_, both := filesSearch207(t, client, base, q)
	require.Len(t, both.Results, 2, "fixture check: the folder matches by name too")

	q.Set("type", "file")
	_, files := filesSearch207(t, client, base, q)
	require.Len(t, files.Results, 1)
	assert.Equal(t, "plans.txt", files.Results[0].Name)
	assert.Equal(t, "text", files.Results[0].Kind)
}

// TestSearch207_ABadFilterIsRefused: a narrowing the server cannot read is a
// 400 naming it, never a quietly wider answer.
func TestSearch207_ABadFilterIsRefused(t *testing.T) {
	base, client, store := signedInAdmin(t)
	st := sizedStorage(t, store, "main", map[string]int64{"a.txt": 1})
	code, _ := filesSearch207(t, client, base, url.Values{"q": {"a"}, "storage_id": {fmt.Sprint(st.ID)}, "min_size": {"lots"}})
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestSearch207_HitsSayStarredAndScore: the explorer drew a star from a list
// of the first 500 stars (audit D4) and the admin search test printed a score
// of 0.000 for every row (audit D7).
func TestSearch207_HitsSayStarredAndScore(t *testing.T) {
	base, client, store := signedInAdmin(t)
	ctx := context.Background()
	st := sizedStorage(t, store, "main", map[string]int64{"budget-2026.xlsx": 1, "budget-2025.xlsx": 1})
	admin, err := store.ListUsers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, admin)
	var starredID int64
	for _, name := range []string{"budget-2026.xlsx"} {
		n, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/"+name))
		require.NoError(t, err)
		starredID = n.ID
		require.NoError(t, store.SetUserNodeMeta(ctx, admin[0].ID, n.ID, "starred", "1"))
	}

	_, got := filesSearch207(t, client, base, url.Values{"q": {"budget"}, "storage_id": {fmt.Sprint(st.ID)}})
	require.Len(t, got.Results, 2)
	for _, h := range got.Results {
		assert.Equal(t, h.ID == starredID, h.Starred, "%s", h.Name)
		require.NotNil(t, h.Score, "every hit carries the ranker's score")
		assert.Equal(t, "spreadsheet", h.Kind)
	}
}

// TestManagerSearch207_TheToolbarTakesTheNarrowingToo: the explorer's name
// search (`action=search`) reads the same parameters.
func TestManagerSearch207_TheToolbarTakesTheNarrowingToo(t *testing.T) {
	base, client, store := signedInAdmin(t)
	sizedStorage(t, store, "main", map[string]int64{"plan-a.txt": 10, "plan-b.mp4": 50 << 20})

	resp, err := client.Get(base + "/api/files/manager?action=search&path=main%3A%2F%2F&filter=plan&type=video")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Files []map[string]any `json:"files"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Files, 1)
	assert.Equal(t, "plan-b.mp4", body.Files[0]["basename"])
	assert.Equal(t, "video", body.Files[0]["kind"])
}

// TestListing207_FoldersFirstAndStarredFromTheCatalogue: the catalogue path
// answered `ORDER BY type DESC, name` - files first - while the merged path
// put folders first (audit Y3); and no row said whether it was starred.
func TestListing207_FoldersFirstAndStarredFromTheCatalogue(t *testing.T) {
	r := newGoneRig(t)
	ctx := context.Background()
	require.NoError(t, r.raw.UpdateStorageSyncCursor(ctx, r.st.ID, time.Now(), ""))
	r.row(t, nil, "/b.txt", model.NodeTypeFile, 5)
	r.row(t, nil, "/Zeta", model.NodeTypeDirectory, 0)
	a := r.row(t, nil, "/a.txt", model.NodeTypeFile, 5)
	r.row(t, nil, "/alpha", model.NodeTypeDirectory, 0)
	require.NoError(t, r.raw.SetUserNodeMeta(ctx, r.owner, a.ID, "starred", "1"))
	u, err := r.raw.GetUser(ctx, r.owner)
	require.NoError(t, err)

	list := func(sortKey string) []map[string]any {
		q := url.Values{"action": {"index"}, "path": {"main://"}}
		if sortKey != "" {
			q.Set("sort", sortKey)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/files/manager?"+q.Encode(), nil)
		req = req.WithContext(auth.WithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		r.mh.List(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp struct {
			Files []map[string]any `json:"files"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp.Files
	}
	order := func(files []map[string]any) []string {
		out := make([]string, len(files))
		for i, f := range files {
			out[i] = fmt.Sprint(f["basename"])
		}
		return out
	}
	files := list("")
	assert.Equal(t, []string{"alpha", "Zeta", "a.txt", "b.txt"}, order(files), "folders first, then by name")
	for _, f := range files {
		_, said := f["starred"]
		assert.Equal(t, f["basename"] == "a.txt", said, "%v: only the starred row says starred", f["basename"])
	}
	assert.Equal(t, []string{"Zeta", "alpha", "b.txt", "a.txt"}, order(list("-name")), "descending keeps folders first")

	req := httptest.NewRequest(http.MethodGet, "/api/files/manager?action=index&path=main%3A%2F%2F&sort=colour", nil)
	rec := httptest.NewRecorder()
	r.mh.List(rec, req.WithContext(auth.WithUser(req.Context(), u)))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "an unknown sort key is refused")
}

// TestRecent207_InTheOrderTheyWereOpened: Recent was drawn in the files'
// modification order because the opening time never reached the client
// (audit D3). The older file, opened last, comes first, with opened_at, and
// the page says how many there are.
func TestRecent207_InTheOrderTheyWereOpened(t *testing.T) {
	r := newGoneRig(t)
	ctx := context.Background()
	old := r.row(t, nil, "/old.txt", model.NodeTypeFile, 1)
	newer := r.row(t, nil, "/newer.txt", model.NodeTypeFile, 1)
	u, err := r.raw.GetUser(ctx, r.owner)
	require.NoError(t, err)

	require.NoError(t, r.raw.SetUserNodeMeta(ctx, r.owner, newer.ID, "last_opened", "1"))
	time.Sleep(1100 * time.Millisecond) // updated_at has second precision
	require.NoError(t, r.raw.SetUserNodeMeta(ctx, r.owner, old.ID, "last_opened", "2"))

	call := func(q string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/files/manager/recent?"+q, nil)
		req = req.WithContext(auth.WithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		handlers.NewMeta(r.store).ListRecent(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp
	}
	resp := call("limit=1")
	nodes := resp["nodes"].([]any)
	require.Len(t, nodes, 1)
	first := nodes[0].(map[string]any)
	assert.Equal(t, "old.txt", first["name"], "the latest opening comes first")
	assert.NotZero(t, first["opened_at"])
	assert.EqualValues(t, 2, resp["total"])
	assert.Equal(t, true, resp["truncated"])

	rest := call("limit=1&offset=1")["nodes"].([]any)
	require.Len(t, rest, 1)
	assert.Equal(t, "newer.txt", rest[0].(map[string]any)["name"])
}

// TestMatchNames207_TheFolderFilterAsksTheServer: the box narrowed with an
// accent-stripped substring of its own (audit D5).
func TestMatchNames207_TheFolderFilterAsksTheServer(t *testing.T) {
	base, client, _ := signedInAdmin(t)
	body, err := json.Marshal(map[string]any{
		"q":     "invoice 2026",
		"names": []string{"invoice_2026.pdf", "invoice-final.pdf", "Invoice 2026 copy.docx"},
	})
	require.NoError(t, err)
	resp, err := client.Post(base+"/api/files/search/match", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got struct {
		Matches []int `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, []int{0, 2}, got.Matches)

	body, _ = json.Marshal(map[string]any{"q": "musteri", "names": []string{"müşteri.pdf"}})
	resp2, err := client.Post(base+"/api/files/search/match", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp2.Body.Close()
	var got2 struct {
		Matches []int `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got2))
	assert.Empty(t, got2.Matches, "accents count, as they do in the search")
}

// TestUsers207_TheSearchBoxIsAServerParameter: the admin Users page filtered
// with the browser's locale rules (audit D5).
func TestUsers207_TheSearchBoxIsAServerParameter(t *testing.T) {
	base, client, store := signedInAdmin(t)
	ctx := context.Background()
	_, err := store.CreateUser(ctx, "gulsen@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "ahmet@example.test", "x", model.RoleUser, "tr", "UTC")
	require.NoError(t, err)

	resp, err := client.Get(base + "/api/admin/users?q=" + url.QueryEscape("GÜLSEN"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var users []struct {
		Email string `json:"email"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&users))
	require.Len(t, users, 1)
	assert.True(t, strings.HasPrefix(users[0].Email, "gulsen@"))
}

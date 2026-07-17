package handlers_test

// /api/files/search content-search surface ("Bul" wave): the scope query
// param, and the snippet/matched additions to the response shape.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func searchTestPathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

// seedSearchContent stands up a server with a real Bleve index plus two
// indexed nodes: sunum.txt (name-only hit for "sunum") and rapor.txt
// (content mentions "sunum"). Returns the logged-in client.
func seedSearchContent(t *testing.T) (base string, client *http.Client, store db.Store) {
	t.Helper()

	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Index = idx
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
	})
	require.NoError(t, err)

	mk := func(name, p string) *model.Node {
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID,
			Name:      name,
			Path:      p,
			PathHash:  searchTestPathHash(st.ID, p),
			Type:      model.NodeTypeFile,
			Mime:      "text/plain",
			Size:      42,
			Etag:      "e-" + name,
		})
		require.NoError(t, err)
		require.NoError(t, idx.IndexNode(ctx, n))
		return n
	}
	mk("sunum.txt", "/sunum.txt")
	rapor := mk("rapor.txt", "/rapor.txt")
	require.NoError(t, idx.IndexNodeContent(ctx, rapor, "yarınki sunum için hazırlık notları"))
	return srv.URL, client, store
}

type searchRespItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
	Matched string `json:"matched"`
}

func doSearch(t *testing.T, base string, client *http.Client, q, scope string) []searchRespItem {
	t.Helper()
	u := base + "/api/files/search?q=" + url.QueryEscape(q)
	if scope != "" {
		u += "&scope=" + scope
	}
	resp, err := client.Get(u)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Results []searchRespItem `json:"results"`
	}
	testutil.ReadJSON(t, resp, &body)
	return body.Results
}

func TestSearchEndpoint_ScopeAndSnippets(t *testing.T) {
	base, client, _ := seedSearchContent(t)

	find := func(items []searchRespItem, name string) *searchRespItem {
		for i := range items {
			if items[i].Name == name {
				return &items[i]
			}
		}
		return nil
	}

	// Default scope (all): both files — the name hit ranked before the
	// content-only hit, each labeled correctly.
	all := doSearch(t, base, client, "sunum", "")
	require.NotNil(t, find(all, "sunum.txt"), "name hit missing: %+v", all)
	require.NotNil(t, find(all, "rapor.txt"), "content hit missing: %+v", all)
	assert.Equal(t, "sunum.txt", all[0].Name, "name hits must rank first")
	assert.Equal(t, search.MatchedName, find(all, "sunum.txt").Matched)
	contentHit := find(all, "rapor.txt")
	assert.Equal(t, search.MatchedContent, contentHit.Matched)
	assert.Contains(t, contentHit.Snippet, "«sunum»")
	assert.NotContains(t, contentHit.Snippet, "<", "snippet must be HTML-free")

	// scope=name: the pre-v0.2 behavior.
	names := doSearch(t, base, client, "sunum", "name")
	assert.NotNil(t, find(names, "sunum.txt"))
	assert.Nil(t, find(names, "rapor.txt"))

	// scope=content: content hits only.
	contents := doSearch(t, base, client, "sunum", "content")
	assert.Nil(t, find(contents, "sunum.txt"))
	require.NotNil(t, find(contents, "rapor.txt"))
	assert.Contains(t, find(contents, "rapor.txt").Snippet, "«sunum»")
}

func TestSearchEndpoint_PostScope(t *testing.T) {
	base, client, _ := seedSearchContent(t)

	body := strings.NewReader(`{"query":"sunum","scope":"content"}`)
	resp, err := client.Post(base+"/api/files/search", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got struct {
		Results []searchRespItem `json:"results"`
	}
	testutil.ReadJSON(t, resp, &got)
	require.Len(t, got.Results, 1)
	assert.Equal(t, "rapor.txt", got.Results[0].Name)
	assert.Equal(t, search.MatchedContent, got.Results[0].Matched)
}

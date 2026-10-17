package handlers_test

// The hostile names (task #238) through the two search endpoints, with and
// without the index: a query reaches the server as the person typed it (one
// decode of `%`, `+`, `#`, `&` in the query string, never two), a decomposed
// name answers the composed spelling, and a name with an invisible character
// in it answers its own name.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

type hostileSearchCorpus struct {
	base      string
	client    *http.Client
	storageID int64
}

// seedHostileSearch catalogues every Creatable hostile name at the root of a
// storage (rows only: a search reads the catalogue and the index, never the
// bytes), indexing them too when withIndex.
func seedHostileSearch(t *testing.T, withIndex bool) hostileSearchCorpus {
	t.Helper()
	var idx *search.Index
	if withIndex {
		var err error
		idx, err = search.Open(t.TempDir() + "/idx.bleve")
		require.NoError(t, err)
		t.Cleanup(func() { _ = idx.Close() })
	}
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		if idx != nil {
			d.Index = idx
		}
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "hostile", Driver: "local", MountPath: "/hostile", Enabled: true,
	})
	require.NoError(t, err)
	for _, n := range hostilenames.Creatable() {
		p := "/" + n.Name
		node, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: n.Name, Path: p,
			PathHash: searchTestPathHash(st.ID, p),
			Type:     model.NodeTypeFile, Mime: "text/plain", Size: 7, Etag: "e-" + n.ID,
		})
		require.NoError(t, err, n.ID)
		if idx != nil {
			require.NoError(t, idx.IndexNode(ctx, node), n.ID)
		}
	}
	return hostileSearchCorpus{base: srv.URL, client: client, storageID: st.ID}
}

func (c hostileSearchCorpus) toolbar(t *testing.T, q string) []string {
	t.Helper()
	resp, err := c.client.Get(c.base + "/api/files/manager?action=search&path=hostile://&filter=" + url.QueryEscape(q))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Files []struct {
			Basename string `json:"basename"`
		} `json:"files"`
	}
	testutil.ReadJSON(t, resp, &body)
	out := make([]string, 0, len(body.Files))
	for _, f := range body.Files {
		out = append(out, f.Basename)
	}
	return out
}

func (c hostileSearchCorpus) endpoint(t *testing.T, q string) []string {
	t.Helper()
	resp, err := c.client.Get(c.base + "/api/files/search?scope=name&storage_id=" +
		strconv.FormatInt(c.storageID, 10) + "&q=" + url.QueryEscape(q))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Results []searchRespItem `json:"results"`
	}
	testutil.ReadJSON(t, resp, &body)
	out := make([]string, 0, len(body.Results))
	for _, r := range body.Results {
		out = append(out, r.Name)
	}
	return out
}

func TestSearch_HostileNamesThroughBothEndpoints(t *testing.T) {
	// query (a hostile name's id) -> the name that must be among the hits.
	cases := []struct{ query, want string }{
		{"nfc-butce", "nfd-butce"},
		{"nfd-butce", "nfc-butce"},
		{"percent-20", "percent-20"},
		{"percent-alone", "percent-alone"},
		{"percent-2f", "percent-2f"},
		{"plus", "plus"},
		{"hash", "hash"},
		{"ampersand-equals", "ampersand-equals"},
		{"semicolon", "semicolon"},
		{"turkish", "turkish"},
		{"cjk-japanese", "cjk-japanese"},
		{"emoji", "emoji"},
		{"zero-width-space", "zero-width-space"},
		{"rlo-extension", "rlo-extension"},
		{"angle-brackets", "angle-brackets"},
	}
	for _, mode := range []struct {
		name  string
		index bool
	}{{"fallback", false}, {"index", true}} {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			c := seedHostileSearch(t, mode.index)
			for _, tc := range cases {
				q, want := hostilenames.Get(tc.query).Name, hostilenames.Get(tc.want).Name
				for surface, got := range map[string][]string{
					"toolbar":  c.toolbar(t, q),
					"endpoint": c.endpoint(t, q),
				} {
					require.Contains(t, got, want, "%s: %s (%q) does not find %s; got %q", surface, tc.query, q, tc.want, head(got))
				}
			}
		})
	}
}

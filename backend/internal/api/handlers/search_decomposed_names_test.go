package handlers_test

// "The file is there, but typing its name in the search box does not find
// it." A production report from 2026-09-24, reproduced here on both search
// paths and through both search endpoints.
//
// Two defects, and a user needed only one of them to get an empty list:
//
//  1. The names were decomposed. The Mac that uploaded them wrote `ü` as
//     `u` + U+0308 — about nine in ten Turkish names in that catalogue were
//     stored that way — and the search box sends the composed `ü`, so every
//     word with ü, ö, ç, ş, ğ or İ in it matched nothing.
//  2. Without the index, the SQL fallback sent the database only the longest
//     word and checked the others afterwards, over the first 1000 rows by
//     name. The longest word was the prefix every file in the storage
//     carries, and the files sat far past row 1000. Even the name pasted
//     byte for byte found nothing, which is what looked like "spaces break
//     search".
//
// The fixture has the same shape with made-up names: files that share every
// word but the person's name and sort before the two being looked for — more
// of them than the fallback's 1000-row window without the index, fewer with
// it, where the window is not the question and indexing costs ~20 ms a file.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const (
	decomposedGurel = "Plan - Ayşe Gürel - 2026.08.28 - Yeni 2 Günlük Ara Öğün"
	decomposedIpek  = "Plan - İpek Ada Yılmaz - 2026.09.21 - 2 Günlük Ara Öğün"
)

type decomposedCorpus struct {
	base      string
	client    *http.Client
	storageID int64
	// gurel and ipek are the stored (decomposed) names of the two files.
	gurel, ipek string
}

func seedDecomposedCorpus(t *testing.T, withIndex bool) decomposedCorpus {
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
		Name: "arsiv", Driver: "local", MountPath: "/arsiv", Enabled: true,
	})
	require.NoError(t, err)

	folder := norm.NFD.String("/Müşteri Veritabanı/2026/")
	add := func(name string) {
		t.Helper()
		p := folder + name
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: p,
			PathHash: searchTestPathHash(st.ID, p),
			Type:     model.NodeTypeFile, Mime: "application/pdf", Size: 42, Etag: "e-" + p,
		})
		require.NoError(t, err)
		if idx != nil {
			require.NoError(t, idx.IndexNode(ctx, n))
		}
	}
	fill := 1100
	if withIndex {
		fill = 150
	}
	for i := 0; i < fill; i++ {
		add(norm.NFD.String("Plan - Aa" + strconv.Itoa(1000+i) + " - 2026.08.28 - Yeni 2 Günlük Ara Öğün.pdf"))
	}
	c := decomposedCorpus{
		base: srv.URL, client: client, storageID: st.ID,
		gurel: norm.NFD.String(decomposedGurel + ".pdf"),
		ipek:  norm.NFD.String(decomposedIpek + ".pdf"),
	}
	add(c.gurel)
	add(c.ipek)
	return c
}

// toolbar is what the explorer's search field calls.
func (c decomposedCorpus) toolbar(t *testing.T, q string) []string {
	t.Helper()
	resp, err := c.client.Get(c.base + "/api/files/manager?action=search&path=arsiv://&filter=" + url.QueryEscape(q))
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

// endpoint is POST/GET /api/files/search, scoped to the storage — the only
// shape of it that has a fallback at all.
func (c decomposedCorpus) endpoint(t *testing.T, q string) []string {
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

func TestSearch_DecomposedNamesAndLongQueries(t *testing.T) {
	for _, mode := range []struct {
		name  string
		index bool
	}{{"fallback", false}, {"index", true}} {
		t.Run(mode.name, func(t *testing.T) {
			c := seedDecomposedCorpus(t, mode.index)
			cases := []struct {
				query string
				want  string
				// only: nothing else in the corpus answers the query.
				only bool
			}{
				// The two reported queries: the name typed as it is shown.
				{decomposedGurel, c.gurel, true},
				{decomposedIpek, c.ipek, true},
				// ...and pasted from the file list, byte for byte.
				{norm.NFD.String(decomposedGurel), c.gurel, true},
				{"Ayşe Gürel", c.gurel, true},
				{"Gürel", c.gurel, true},
				{"İpek Ada Yılmaz", c.ipek, true},
				{"ipek", c.ipek, true},
				// Lower-cased by a client the full Unicode way: `İ` -> `i` + U+0307.
				{"i\u0307pek ada yılmaz", c.ipek, true},
				{"GÜREL", c.gurel, true},
			}
			for _, tc := range cases {
				for surface, got := range map[string][]string{
					"toolbar":  c.toolbar(t, tc.query),
					"endpoint": c.endpoint(t, tc.query),
				} {
					require.NotEmpty(t, got, "%s %q found nothing", surface, tc.query)
					require.Equal(t, tc.want, got[0], "%s %q: the file must come first, got %v", surface, tc.query, head(got))
					if tc.only {
						require.Len(t, got, 1, "%s %q: nothing else answers it, got %v", surface, tc.query, head(got))
					}
				}
			}
		})
	}
}

// TestSearch_DecomposedNames_FallbackStillAnswersBroadQueries: every word
// being a condition must not turn a broad query into an empty one. `günlük
// öğün` is in every file here, decomposed; the fallback returns pages of them.
func TestSearch_DecomposedNames_FallbackStillAnswersBroadQueries(t *testing.T) {
	c := seedDecomposedCorpus(t, false)
	got := c.toolbar(t, "günlük öğün")
	require.GreaterOrEqual(t, len(got), 250, "at least the toolbar's page size, all of them answering both words")
	for _, name := range got {
		n := norm.NFC.String(strings.ToLower(name))
		require.True(t, strings.Contains(n, "günlük") && strings.Contains(n, "öğün"), "%q answers both words", name)
	}
}

func head(s []string) []string {
	if len(s) > 5 {
		return s[:5]
	}
	return s
}

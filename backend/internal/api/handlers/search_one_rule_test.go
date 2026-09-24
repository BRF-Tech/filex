package handlers_test

// One rule for when a typed word is in a file's name, on every door a search
// comes through, with the index and without it.
//
// GitHub PR #46 found that a name a Mac wrote decomposed (`u` + U+0308 for
// `ü`) answered no typed word, and made both search paths compare names
// composed. v0.43.0 folds the four Latin i's into one letter for tags
// (internal/tagname). Both are one normaliser now, internal/namefold, and
// this test holds every surface to it: the toolbar (the explorer, the
// desktop app — it embeds the same explorer), /api/files/search (the
// advanced search; without a storage, the admin Files page), the AI search
// and the MCP file_search tool, and a tag's files searched by name.
//
// The corpus is chosen so each case fails on exactly one part of the rule:
//   - a decomposed name answers a composed query (PR #46);
//   - an all-caps Turkish name answers the word typed in lower case — `kış`
//     for `KIŞ LİSTESİ` — which strings.ToLower calls two words (`kiş`,
//     `kış`), on the index path and the fallback alike;
//   - a word typed in capitals reaches a lower-case name through the part of
//     a name the index keeps as it was written (`IŞIK` in `ışıkları.txt`);
//   - a query lower-cased the full Unicode way (`i` + U+0307) answers `İ`.

import (
	"context"
	"encoding/json"
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

type oneRuleCorpus struct {
	base      string
	client    *http.Client
	token     string
	storageID int64
	withIndex bool
}

// The stored names. Two of them the way a Mac writes them.
var (
	oneRuleGurel = norm.NFD.String("Ayşe Gürel - Günlük Plan.pdf")
	oneRuleKis   = "KIŞ LİSTESİ.xlsx"
	oneRuleIsik  = "ışıkları.txt"
	oneRuleIpek  = norm.NFD.String("İPEK ADA YILMAZ.pdf")
)

func seedOneRuleCorpus(t *testing.T, withIndex bool) oneRuleCorpus {
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
	admin, err := store.GetUserByEmail(ctx, email)
	require.NoError(t, err)

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "arsiv", Driver: "local", MountPath: "/arsiv", Enabled: true,
	})
	require.NoError(t, err)
	folder := norm.NFD.String("/Müşteri Veritabanı/2026/")
	add := func(name string) int64 {
		t.Helper()
		p := folder + name
		n, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: name, Path: p,
			PathHash: searchTestPathHash(st.ID, p),
			Type:     model.NodeTypeFile, Mime: "application/octet-stream", Size: 42, Etag: "e-" + p,
		})
		require.NoError(t, err)
		if idx != nil {
			require.NoError(t, idx.IndexNode(ctx, n))
		}
		return n.ID
	}
	for i := 0; i < 20; i++ {
		add(norm.NFD.String("Plan - Aa" + strconv.Itoa(100+i) + " - Günlük.pdf"))
	}
	for _, name := range []string{oneRuleGurel, oneRuleKis, oneRuleIsik, oneRuleIpek} {
		// A team tag, so the same files can be searched inside a tag.
		testutil.TagNode(t, store, add(name), 0, "Arşiv")
	}
	return oneRuleCorpus{
		base: srv.URL, client: client, storageID: st.ID, withIndex: withIndex,
		token: testutil.NewAPIToken(t, store, admin.ID, "read,mcp"),
	}
}

func (c oneRuleCorpus) getJSON(t *testing.T, u string, withToken bool, into any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+u, nil)
	require.NoError(t, err)
	client := c.client
	if withToken {
		req.Header.Set("X-Filex-Token", c.token)
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s", u)
	testutil.ReadJSON(t, resp, into)
}

// surfaces is every door, each returning the names it answered with.
func (c oneRuleCorpus) surfaces() map[string]func(t *testing.T, q string) []string {
	out := map[string]func(t *testing.T, q string) []string{
		"toolbar": func(t *testing.T, q string) []string {
			var body struct {
				Files []struct {
					Basename string `json:"basename"`
				} `json:"files"`
			}
			c.getJSON(t, "/api/files/manager?action=search&path=arsiv://&filter="+url.QueryEscape(q), false, &body)
			var names []string
			for _, f := range body.Files {
				names = append(names, f.Basename)
			}
			return names
		},
		"files/search": func(t *testing.T, q string) []string {
			var body struct {
				Results []searchRespItem `json:"results"`
			}
			c.getJSON(t, "/api/files/search?scope=name&storage_id="+strconv.FormatInt(c.storageID, 10)+"&q="+url.QueryEscape(q), false, &body)
			var names []string
			for _, r := range body.Results {
				names = append(names, r.Name)
			}
			return names
		},
		"ai/search": func(t *testing.T, q string) []string {
			var body struct {
				Entries []struct {
					Name string `json:"name"`
				} `json:"entries"`
			}
			c.getJSON(t, "/api/ai/search?path=arsiv://&q="+url.QueryEscape(q), true, &body)
			var names []string
			for _, e := range body.Entries {
				names = append(names, e.Name)
			}
			return names
		},
		"mcp file_search": func(t *testing.T, q string) []string {
			args, err := json.Marshal(map[string]any{"path": "arsiv://", "query": q, "content": false})
			require.NoError(t, err)
			code, body := mcpPost(t, &http.Client{}, c.base+"/api/ai/mcp", c.token,
				`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"file_search","arguments":`+string(args)+`}}`)
			require.Equal(t, http.StatusOK, code, body)
			// Streamable HTTP may frame the answer as an SSE event.
			start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
			require.True(t, start >= 0 && end > start, body)
			var rpc struct {
				Result struct {
					IsError           bool `json:"isError"`
					StructuredContent struct {
						Entries []struct {
							Name string `json:"name"`
						} `json:"entries"`
					} `json:"structuredContent"`
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(body[start:end+1]), &rpc), body)
			require.False(t, rpc.Result.IsError, body)
			var names []string
			for _, e := range rpc.Result.StructuredContent.Entries {
				names = append(names, e.Name)
			}
			return names
		},
	}
	// Without a storage the endpoint has no fallback (a documented gate), so
	// the admin Files page answers from the index only.
	if c.withIndex {
		out["admin files"] = func(t *testing.T, q string) []string {
			var body struct {
				Results []searchRespItem `json:"results"`
			}
			c.getJSON(t, "/api/files/search?scope=name&q="+url.QueryEscape(q), false, &body)
			var names []string
			for _, r := range body.Results {
				names = append(names, r.Name)
			}
			return names
		}
	}
	return out
}

func TestSearch_OneNormaliserOnEverySurface(t *testing.T) {
	dot := string(rune(0x0307))
	for _, mode := range []struct {
		name  string
		index bool
	}{{"fallback", false}, {"index", true}} {
		t.Run(mode.name, func(t *testing.T) {
			c := seedOneRuleCorpus(t, mode.index)
			cases := []struct{ query, want string }{
				{"gürel", oneRuleGurel},
				{"AYŞE GÜREL", oneRuleGurel},
				{"kış", oneRuleKis},
				{"Kış listesi", oneRuleKis},
				{"IŞIK", oneRuleIsik},
				{"ipek yılmaz", oneRuleIpek},
				{"i" + dot + "pek", oneRuleIpek},
			}
			for surface, run := range c.surfaces() {
				for _, tc := range cases {
					for _, q := range []string{tc.query, "tag:Arşiv " + tc.query} {
						got := run(t, q)
						require.Contains(t, got, tc.want, "%s, %s: %q found %q", mode.name, surface, q, got)
					}
				}
			}
		})
	}
}

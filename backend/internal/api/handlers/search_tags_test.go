package handlers_test

// /api/files/search — the `tag:` filter and the forgiving name matching
// from issue #15, measured through the HTTP surface rather than the
// index package, because that is where the tag has to be resolved
// against the database.

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// seedTaggedSearch stands up a server with the issue's six-file fixture
// indexed, plus tags: main.go and readme.txt are `source`,
// invoice_2026.pdf is `invoice`, main.go is additionally `draft`.
func seedTaggedSearch(t *testing.T) (base string, client *http.Client, store db.Store, storageID int64) {
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
	mainGo := mk("main.go", "/main.go")
	mk("foo-bar.txt", "/foo-bar.txt")
	invoice := mk("invoice_2026.pdf", "/invoice_2026.pdf")
	mk("annual report 2025.docx", "/annual report 2025.docx")
	readme := mk("readme.txt", "/readme.txt")
	mk("notes.md", "/Documents/notes.md")

	require.NoError(t, store.SetNodeTags(ctx, mainGo.ID, []string{"source", "draft"}))
	require.NoError(t, store.SetNodeTags(ctx, readme.ID, []string{"source"}))
	require.NoError(t, store.SetNodeTags(ctx, invoice.ID, []string{"invoice"}))

	return srv.URL, client, store, st.ID
}

func namesOf(items []searchRespItem) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.Name)
	}
	return out
}

// TestSearchEndpoint_ForgivingNames is the issue's table over HTTP.
func TestSearchEndpoint_ForgivingNames(t *testing.T) {
	base, client, _, _ := seedTaggedSearch(t)
	cases := []struct{ query, want string }{
		{"main.go", "main.go"},
		{"main go", "main.go"},
		{"foo bar", "foo-bar.txt"},
		{"invoice 2026", "invoice_2026.pdf"},
		{"mian.go", "main.go"},
		{"report 2025", "annual report 2025.docx"},
		{"invoice-2026", "invoice_2026.pdf"},
		{"MAIN GO", "main.go"},
		{"documents notes", "notes.md"},
	}
	for _, c := range cases {
		got := namesOf(doSearch(t, base, client, c.query, "name"))
		assert.Contains(t, got, c.want, "query %q", c.query)
	}
}

// TestSearchEndpoint_ExactRanksFirst is the issue's explicit ask, at the
// surface a client actually reads.
func TestSearchEndpoint_ExactRanksFirst(t *testing.T) {
	base, client, _, _ := seedTaggedSearch(t)
	for _, q := range []string{"main.go", "main go", "main"} {
		got := namesOf(doSearch(t, base, client, q, "name"))
		require.NotEmpty(t, got, "query %q returned nothing", q)
		assert.Equal(t, "main.go", got[0], "query %q must rank the exact match first (got %v)", q, got)
	}
}

func TestSearchEndpoint_TagFilter(t *testing.T) {
	base, client, _, _ := seedTaggedSearch(t)

	// A bare tag: is a listing of everything carrying it.
	src := namesOf(doSearch(t, base, client, "tag:source", "name"))
	assert.ElementsMatch(t, []string{"main.go", "readme.txt"}, src)

	// Case-insensitive, both in the prefix and in the value.
	assert.ElementsMatch(t, src, namesOf(doSearch(t, base, client, "TAG:Source", "name")))

	// Free text + tag — the reporter's own example.
	assert.Equal(t, []string{"main.go"}, namesOf(doSearch(t, base, client, "main go tag:source", "name")))

	// The same text under a tag it does not carry finds nothing. A filter
	// narrows; it never quietly stops applying.
	assert.Empty(t, namesOf(doSearch(t, base, client, "main go tag:invoice", "name")))

	// Several tags AND.
	assert.Equal(t, []string{"main.go"}, namesOf(doSearch(t, base, client, "tag:source tag:draft", "name")))
	assert.Empty(t, namesOf(doSearch(t, base, client, "tag:invoice tag:draft", "name")))

	// A tag nobody has applied returns nothing — NOT everything.
	assert.Empty(t, namesOf(doSearch(t, base, client, "tag:nosuchtag", "name")))
	assert.Empty(t, namesOf(doSearch(t, base, client, "main tag:nosuchtag", "name")))

	// Exclusion.
	assert.Empty(t, namesOf(doSearch(t, base, client, "readme -tag:source", "name")))
	txt := namesOf(doSearch(t, base, client, "txt -tag:source", "name"))
	assert.Contains(t, txt, "foo-bar.txt")
	assert.NotContains(t, txt, "readme.txt")

	// `tag:` with no value is not a filter: it stays free text, so a file
	// literally called that is still findable and the query is not
	// silently reinterpreted.
	assert.Empty(t, namesOf(doSearch(t, base, client, "tag:", "name")))
}

// TestSearchEndpoint_TagFilterSurvivesTagChange is why tags are resolved
// against the database instead of copied into the search document: a tag
// applied a moment ago must filter correctly with no reindex at all.
func TestSearchEndpoint_TagFilterSurvivesTagChange(t *testing.T) {
	base, client, store, _ := seedTaggedSearch(t)
	ctx := context.Background()

	assert.Empty(t, namesOf(doSearch(t, base, client, "tag:fresh", "name")))

	nodes, err := store.ListNodesByTag(ctx, "invoice", 10)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.NoError(t, store.SetNodeTags(ctx, nodes[0].ID, []string{"fresh"}))

	assert.Equal(t, []string{"invoice_2026.pdf"}, namesOf(doSearch(t, base, client, "tag:fresh", "name")))
	// …and the tag it no longer carries stops matching, same instant.
	assert.Empty(t, namesOf(doSearch(t, base, client, "tag:invoice", "name")))
}

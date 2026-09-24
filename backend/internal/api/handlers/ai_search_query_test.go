package handlers_test

// The AI surfaces must speak the SAME query language as the web endpoints
// (issue #15). GET /api/ai/search used to call aiOps.Search with the raw
// string, so `invoice 2026` found nothing and `tag:source` was read as a
// filename — one product answering the same question differently
// depending on which door an agent came through.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func aiSeedFiles(t *testing.T, srv string, client *http.Client, tok string, names ...string) {
	t.Helper()
	for _, n := range names {
		resp := aiReq(t, client, "POST", srv+"/api/ai/upload", tok, map[string]any{
			"path":           "main://" + n,
			"content_base64": base64.StdEncoding.EncodeToString([]byte("x")),
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	}
}

func aiSearchNames(t *testing.T, srv string, client *http.Client, tok, q string) []string {
	t.Helper()
	resp := aiReq(t, client, "GET",
		srv+"/api/ai/search?path=main://&q="+url.QueryEscape(q), tok, nil)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	testutil.ReadJSON(t, resp, &body)
	out := make([]string, 0, len(body.Entries))
	for _, e := range body.Entries {
		out = append(out, e.Name)
	}
	return out
}

func aiTagNode(t *testing.T, store db.Store, storageID int64, word string, tags ...string) {
	t.Helper()
	ctx := context.Background()
	rows, err := store.SearchNodes(ctx, storageID, model.NameMatch{Words: []string{word}}, 10)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "no node matching %q to tag", word)
	testutil.TagNode(t, store, rows[0].ID, 0, tags...)
}

func TestAISearch_SpeaksTheSameQueryLanguage(t *testing.T) {
	srv, client, store, tok := aiFixture(t)
	aiSeedFiles(t, srv.URL, client, tok, "main.go", "invoice_2026.pdf", "foo-bar.txt")

	// Separator-blind, multi-word.
	assert.Contains(t, aiSearchNames(t, srv.URL, client, tok, "invoice 2026"), "invoice_2026.pdf")
	assert.Contains(t, aiSearchNames(t, srv.URL, client, tok, "main go"), "main.go")
	assert.Contains(t, aiSearchNames(t, srv.URL, client, tok, "foo bar"), "foo-bar.txt")
	// Extra words narrow.
	assert.NotContains(t, aiSearchNames(t, srv.URL, client, tok, "invoice 2025"), "invoice_2026.pdf")

	// tag: is a filter here too, not a filename.
	storages, err := store.ListEnabledStorages(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, storages)
	aiTagNode(t, store, storages[0].ID, "main.go", "source")

	assert.Equal(t, []string{"main.go"}, aiSearchNames(t, srv.URL, client, tok, "tag:source"))
	assert.Equal(t, []string{"main.go"}, aiSearchNames(t, srv.URL, client, tok, "main go tag:source"))
	assert.Empty(t, aiSearchNames(t, srv.URL, client, tok, "invoice tag:source"))
	assert.Empty(t, aiSearchNames(t, srv.URL, client, tok, "tag:nosuchtag"))
	// An exclusion is a filter here too. The name half used to test the
	// inclusive tags only — its entries carried no node id — so a `-tag:`
	// on the AI surface excluded nothing.
	assert.NotContains(t, aiSearchNames(t, srv.URL, client, tok, "main go -tag:source"), "main.go")
	assert.Contains(t, aiSearchNames(t, srv.URL, client, tok, "invoice -tag:source"), "invoice_2026.pdf")
}

// TestAISearch_ABareTagListsTheTaggedFiles — a bare `tag:x` is a listing, as
// on /api/files/search and the toolbar. It used to be the first 200 rows of
// the storage by name, filtered by the tag afterwards, so a tagged file that
// sorted past row 200 was never found.
func TestAISearch_ABareTagListsTheTaggedFiles(t *testing.T) {
	srv, client, store, tok := aiFixture(t)
	storages, err := store.ListEnabledStorages(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, storages)
	st := storages[0]
	ctx := context.Background()
	for i := 0; i < 210; i++ {
		p := fmt.Sprintf("/a-%03d.txt", i)
		_, err := store.CreateNode(ctx, &model.Node{
			StorageID: st.ID, Name: p[1:], Path: p, PathHash: pathkey.Hash(st.ID, p), Type: model.NodeTypeFile,
		})
		require.NoError(t, err)
	}
	late, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "zz-late.txt", Path: "/zz-late.txt",
		PathHash: pathkey.Hash(st.ID, "/zz-late.txt"), Type: model.NodeTypeFile,
	})
	require.NoError(t, err)
	testutil.TagNode(t, store, late.ID, 0, "geç")

	assert.Equal(t, []string{"zz-late.txt"}, aiSearchNames(t, srv.URL, client, tok, "tag:geç"))
	assert.Equal(t, []string{"zz-late.txt"}, aiSearchNames(t, srv.URL, client, tok, "tag:GEÇ"), "a tag is found whatever its case")
}

// ⚠ The word an agent searches for is a LIKE pattern's CONTENT, never part of
// its grammar. `rapor_2026` must find `rapor_2026.pdf` and must NOT find
// `rapor 2026.pdf`: the two-step plan (every word to the database, the
// whole query re-checked per row) is what keeps the underscore a letter, and
// the store escapes every word it is handed like every other door's.
func TestAISearch_AnUnderscoreIsALetter(t *testing.T) {
	srv, client, _, tok := aiFixture(t)
	aiSeedFiles(t, srv.URL, client, tok, "rapor_2026.pdf", "rapor 2026.pdf", "butce.xlsx")

	got := aiSearchNames(t, srv.URL, client, tok, "rapor_2026")
	assert.Contains(t, got, "rapor_2026.pdf")
	assert.NotContains(t, got, "rapor 2026.pdf", "the underscore matched any character")
}

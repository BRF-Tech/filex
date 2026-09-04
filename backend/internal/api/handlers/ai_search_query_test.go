package handlers_test

// The AI surfaces must speak the SAME query language as the web endpoints
// (issue #15). GET /api/ai/search used to call aiOps.Search with the raw
// string, so `invoice 2026` found nothing and `tag:source` was read as a
// filename — one product answering the same question differently
// depending on which door an agent came through.

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
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

func aiTagNode(t *testing.T, store db.Store, storageID int64, like string, tags ...string) {
	t.Helper()
	ctx := context.Background()
	rows, err := store.SearchNodes(ctx, storageID, like, 10)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "no node matching %q to tag", like)
	require.NoError(t, store.SetNodeTags(ctx, rows[0].ID, tags))
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
	aiTagNode(t, store, storages[0].ID, "%main.go%", "source")

	assert.Equal(t, []string{"main.go"}, aiSearchNames(t, srv.URL, client, tok, "tag:source"))
	assert.Equal(t, []string{"main.go"}, aiSearchNames(t, srv.URL, client, tok, "main go tag:source"))
	assert.Empty(t, aiSearchNames(t, srv.URL, client, tok, "invoice tag:source"))
	assert.Empty(t, aiSearchNames(t, srv.URL, client, tok, "tag:nosuchtag"))
}

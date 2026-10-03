package handlers_test

// Issue #104, item 6, on the AI/MCP surface: an agent hears of an entry the
// storage could not answer for (its listing carries `unavailable` and the
// storage's answer) and is refused, with the same 409 ENTRY_UNAVAILABLE as the
// explorer, whatever it asks to do with it or inside it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

func TestAI_AnUnavailableEntryIsListedAndRefused(t *testing.T) {
	srv, client, store, tok := aiFixture(t)
	ctx := context.Background()
	storages, err := store.ListEnabledStorages(ctx)
	require.NoError(t, err)
	st := storages[0]
	dir, err := store.CreateNode(ctx, &model.Node{
		StorageID: st.ID, Name: "Proje", Path: "/Proje", StorageKey: "/Proje",
		PathHash: pathkey.Hash(st.ID, "/Proje"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	_, err = store.MarkNodeUnavailable(ctx, dir.ID, "plugin: no answer")
	require.NoError(t, err)

	// Listed - the storage does not list it, the catalogue does - and flagged.
	resp := aiReq(t, client, "GET", srv.URL+"/api/ai/files?path=main://", tok, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ls struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ls))
	resp.Body.Close()
	var found map[string]any
	for _, e := range ls.Entries {
		if e["name"] == "Proje" {
			found = e
		}
	}
	require.NotNil(t, found, "an agent never hears of the unavailable entry: %+v", ls.Entries)
	assert.Equal(t, true, found["unavailable"])
	assert.Equal(t, "plugin: no answer", found["unavailable_reason"])
	assert.Equal(t, "dir", found["type"])

	for _, c := range []struct{ method, url string }{
		{"GET", "/api/ai/files?path=main://Proje"},
		{"GET", "/api/ai/info?path=main://Proje"},
		{"GET", "/api/ai/download?path=main://Proje/a.txt"},
	} {
		resp := aiReq(t, client, c.method, srv.URL+c.url, tok, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !assert.Equal(t, http.StatusConflict, resp.StatusCode, "%s %s: %s", c.method, c.url, body) {
			continue
		}
		var refusal map[string]any
		require.NoError(t, json.Unmarshal(body, &refusal))
		assert.Equal(t, "ENTRY_UNAVAILABLE", refusal["code"], c.url)
		assert.Equal(t, "main://Proje", refusal["path"], c.url)
	}

	// Writes too: a move into it, a delete of it.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://Proje/yeni"})
	resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode, "a folder was made inside an unavailable entry")
}

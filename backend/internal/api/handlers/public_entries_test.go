package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// A shared folder can be looked into, not only downloaded whole — and only
// after the PIN, and only inside the folder that was shared.
func TestPublicShare_FolderEntries(t *testing.T) {
	f := newAppFixture(t, nil)
	f.writeFile(t, "proje/rapor.pdf", "%PDF-1.7 report")
	f.writeFile(t, "proje/ekler/liste.csv", "a,b\n1,2\n")
	f.writeFile(t, "baska/sir.txt", "not yours")
	ctx := context.Background()
	_, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "proje", Path: "proje",
		PathHash: pathkey.Hash(f.st.ID, "/proje"), Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	token, pin := f.shareWithPin(t, "main://proje")

	// Locked: the listing is behind the gate like everything else.
	client := freshClient(t)
	status, raw := doReq(t, client, http.MethodGet, f.srv.URL+"/api/public/s/"+token+"/entries", nil)
	assert.Equal(t, http.StatusUnauthorized, status, string(raw))

	status, raw = doReq(t, client, http.MethodPost, f.srv.URL+"/api/public/s/"+token+"/pin", map[string]any{"pin": pin})
	require.Equal(t, http.StatusOK, status, string(raw))

	status, raw = doReq(t, client, http.MethodGet, f.srv.URL+"/api/public/s/"+token+"/entries", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var top struct {
		Path    string `json:"path"`
		Entries []struct {
			Name  string `json:"name"`
			Path  string `json:"path"`
			IsDir bool   `json:"is_dir"`
			Size  int64  `json:"size"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &top))
	names := map[string]bool{}
	for _, e := range top.Entries {
		names[e.Name] = e.IsDir
	}
	assert.Contains(t, names, "rapor.pdf")
	assert.True(t, names["ekler"], "folders first and marked as folders")
	assert.NotContains(t, names, "sir.txt", "a sibling folder is not in this share")

	// One level down, addressed by the path the listing gave.
	status, raw = doReq(t, client, http.MethodGet, f.srv.URL+"/api/public/s/"+token+"/entries?path=ekler", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), "liste.csv")

	// And nowhere else: a path that climbs out is not a path.
	status, _ = doReq(t, client, http.MethodGet, f.srv.URL+"/api/public/s/"+token+"/entries?path=../baska", nil)
	assert.Equal(t, http.StatusNotFound, status)
}

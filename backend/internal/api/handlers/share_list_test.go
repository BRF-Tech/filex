package handlers_test

// Regression test for the "Mevcut paylaşım yok" bug: creating a share link left
// the permissions modal's "Existing links" list empty because there was no
// GET /api/files/share?path= (list-by-node) endpoint — only POST (create) and
// DELETE. HandleList closes that gap; here we prove a freshly created link is
// listed, with the numeric id in `uuid` (what DELETE /share/{id} expects) and a
// token-based /s/ url.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

func newShareListFixture(t *testing.T) *chi.Mux {
	t.Helper()
	_, store, drv, st, _ := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown id %d", id)
		}
		return drv, nil
	}
	shareH := handlers.NewShare(share.NewService(store), store, resolver, "https://fm.example", sharezip.New(""))
	r := chi.NewRouter()
	r.Get("/api/files/share", shareH.HandleList)
	r.Post("/api/files/share", shareH.HandleCreate)
	return r
}

type shareListResp struct {
	Shares []map[string]any `json:"shares"`
}

func listShares(t *testing.T, r http.Handler, nodeID int64) shareListResp {
	t.Helper()
	rec := doGet(t, r, fmt.Sprintf("/api/files/share?node_id=%d", nodeID))
	require.Equal(t, http.StatusOK, rec.Code)
	var out shareListResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// TestShare_List_EmptyThenListsCreatedLink is the core regression: before any
// link the list is an empty array (not an error), and after POST /share the new
// link shows up exactly once.
func TestShare_List_EmptyThenListsCreatedLink(t *testing.T) {
	_, store, _, st, root := newMutateFixture(t)
	resolver := func(id int64) (storage.Driver, error) { return nil, fmt.Errorf("n/a %d", id) }
	shareH := handlers.NewShare(share.NewService(store), store, resolver, "https://fm.example", sharezip.New(""))
	r := chi.NewRouter()
	r.Get("/api/files/share", shareH.HandleList)
	r.Post("/api/files/share", shareH.HandleCreate)

	dir := mkdirNode(t, store, st, root, "docs")

	// No links yet → empty list, HTTP 200 (modal shows "none", not an error).
	require.Len(t, listShares(t, r, dir.ID).Shares, 0)

	// Mint a link on the folder.
	body, _ := json.Marshal(map[string]any{"node_id": dir.ID})
	req := httptest.NewRequest("POST", "/api/files/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	crec := httptest.NewRecorder()
	r.ServeHTTP(crec, req)
	require.Equal(t, http.StatusOK, crec.Code)

	// The list now surfaces exactly that link.
	out := listShares(t, r, dir.ID)
	require.Len(t, out.Shares, 1)
	assert.NotEmpty(t, out.Shares[0]["uuid"], "uuid (numeric share id) must be set for revoke")
	assert.Contains(t, fmt.Sprint(out.Shares[0]["url"]), "/s/", "download share url is /s/<token>")
	assert.Equal(t, "download", out.Shares[0]["kind"])
}

// TestShare_List_MissingPathIsBadRequest guards the argument contract.
func TestShare_List_MissingPathIsBadRequest(t *testing.T) {
	r := newShareListFixture(t)
	rec := doGet(t, r, "/api/files/share")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

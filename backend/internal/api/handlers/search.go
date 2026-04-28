package handlers

import (
	"encoding/json"
	"net/http"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/search"
)

// Search handles /api/files/search.
type Search struct {
	Index *search.Index
	Store db.Store
}

// NewSearch constructs a Search handler.
func NewSearch(idx *search.Index, store db.Store) *Search {
	return &Search{Index: idx, Store: store}
}

type searchRequest struct {
	StorageID int64  `json:"storage_id"`
	Query     string `json:"query"`
	Limit     int    `json:"limit"`
}

// Search returns up to N matching nodes.
//
// Strategy: try Bleve first; on miss/empty, fall back to SQL LIKE on the
// `nodes.name` column.
func (h *Search) Search(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	results := []*model.Node{}
	if h.Index != nil {
		hits := h.Index.SafeSearch(r.Context(), req.Query, req.Limit)
		for _, hit := range hits {
			n, err := h.Store.GetNode(r.Context(), hit.NodeID)
			if err == nil && (req.StorageID == 0 || n.StorageID == req.StorageID) {
				results = append(results, n)
			}
		}
	}
	if len(results) == 0 && req.StorageID != 0 && req.Query != "" {
		fallback, err := h.Store.SearchNodes(r.Context(), req.StorageID, search.SQLLike(req.Query), req.Limit)
		if err == nil {
			results = fallback
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

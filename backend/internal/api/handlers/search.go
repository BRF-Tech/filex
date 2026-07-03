package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
)

// Search handles /api/files/search.
type Search struct {
	Index *search.Index
	Store db.Store
	ACL   *acl.Resolver
}

// NewSearch constructs a Search handler.
func NewSearch(idx *search.Index, store db.Store) *Search {
	return &Search{Index: idx, Store: store}
}

// AttachACL wires the RBAC resolver so search results are filtered to the
// paths the caller may see (prevents cross-user enumeration via search).
func (h *Search) AttachACL(r *acl.Resolver) { h.ACL = r }

type searchRequest struct {
	StorageID int64  `json:"storage_id"`
	Query     string `json:"query"`
	Limit     int    `json:"limit"`
}

// Search returns up to N matching nodes.
//
// Strategy: try Bleve first; on miss/empty, fall back to SQL LIKE on the
// `nodes.name` column.
//
// Accepts both POST {query, storage_id, limit} (canonical) and
// GET ?q=…&storage_id=…&limit=… (admin SPA's toolbar search). The GET
// form lets the SFC degrade gracefully when the embedder hasn't wired
// the POST flow.
func (h *Search) Search(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		req.Query = q.Get("q")
		if req.Query == "" {
			req.Query = q.Get("query")
		}
		if v := q.Get("storage_id"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				req.StorageID = n
			}
		}
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				req.Limit = n
			}
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
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
	// RBAC: drop hits the caller can't see (per-storage grants; cached).
	if h.ACL != nil {
		user := auth.UserFrom(r.Context())
		cache := map[int64]*acl.Set{}
		kept := results[:0]
		for _, n := range results {
			set, ok := cache[n.StorageID]
			if !ok {
				st, _ := h.Store.GetStorage(r.Context(), n.StorageID)
				set, _ = h.ACL.LoadSet(r.Context(), user, st)
				cache[n.StorageID] = set
			}
			if set == nil || set.CanSee(n.Path) {
				kept = append(kept, n)
			}
		}
		results = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

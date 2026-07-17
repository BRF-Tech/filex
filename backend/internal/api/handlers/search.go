package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/tenant"
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
	// Scope selects the fields consulted: "name" | "content" | "all"
	// (default all — name hits ranked first, so pre-v0.2 clients see the
	// same ordering they always did, plus content hits after).
	Scope string `json:"scope"`
}

// searchResult is one hit in the response: the node row plus the v0.2
// content-search additions. Embedding keeps the wire shape backward
// compatible — old clients simply ignore snippet/matched.
type searchResult struct {
	*model.Node
	// Snippet is a short plain-text fragment around a content match with
	// the matched terms wrapped in « » ("" for name-only hits, never HTML).
	Snippet string `json:"snippet"`
	// Matched reports which side(s) hit: "name" | "content" | "both".
	Matched string `json:"matched"`
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
		req.Scope = q.Get("scope")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}
	sc := search.ParseScope(req.Scope)
	results := []searchResult{}
	if h.Index != nil {
		hits := h.Index.SafeSearchScoped(r.Context(), req.Query, req.Limit, sc)
		for _, hit := range hits {
			n, err := h.Store.GetNode(r.Context(), hit.NodeID)
			if err == nil && (req.StorageID == 0 || n.StorageID == req.StorageID) {
				/* wiring:e2 — marker dosyası ad aramasında da görünmez */
				if n.Name == e2e.MarkerName {
					continue
				}
				results = append(results, searchResult{Node: n, Snippet: hit.Snippet, Matched: hit.Matched})
			}
		}
	}
	// SQL LIKE fallback — name-only by nature, so a content-scoped query
	// never falls back (an index-less install has no content to search).
	if len(results) == 0 && req.StorageID != 0 && req.Query != "" && sc != search.ScopeContent {
		fallback, err := h.Store.SearchNodes(r.Context(), req.StorageID, search.SQLLike(req.Query), req.Limit)
		if err == nil {
			for _, n := range fallback {
				/* wiring:e2 — marker dosyası ad aramasında da görünmez */
				if n.Name == e2e.MarkerName {
					continue
				}
				results = append(results, searchResult{Node: n, Matched: search.MatchedName})
			}
		}
	}
	// Multi-tenant: drop hits in storages outside the caller's tenant. This is
	// the file-data (layer-1) confinement — an unfiltered search is the classic
	// cross-tenant leak (content, not just a name). No-op unless a scope is set.
	if scope, ok := tenant.FromContext(r.Context()); ok {
		kept := results[:0]
		for _, res := range results {
			if scope.CanAccessStorage(res.StorageID) {
				kept = append(kept, res)
			}
		}
		results = kept
	}

	// RBAC: drop hits the caller can't see (per-storage grants; cached).
	// Snippets ride on the hit, so a dropped hit drops its snippet too —
	// content search can never leak text the caller couldn't browse to.
	if h.ACL != nil {
		user := auth.UserFrom(r.Context())
		cache := map[int64]*acl.Set{}
		kept := results[:0]
		for _, res := range results {
			set, ok := cache[res.StorageID]
			if !ok {
				st, _ := h.Store.GetStorage(r.Context(), res.StorageID)
				set, _ = h.ACL.LoadSet(r.Context(), user, st)
				cache[res.StorageID] = set
			}
			if set == nil || set.CanSee(res.Path) {
				kept = append(kept, res)
			}
		}
		results = kept
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

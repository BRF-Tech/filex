package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
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

// tagFilterMax caps how many nodes ONE tag contributes to a filter.
//
// The filter is applied inside the index as a document-ID set, which is
// exact and lets `limit` count filtered results — but the set has to be
// materialised first. 10k nodes per tag is far past any hand-applied
// tag and keeps a runaway tag from turning one search into a
// ten-thousand-clause boolean query. A tag larger than this is truncated
// newest-first (the order ListNodesByTag returns), which is stated in
// docs/SEARCH.md rather than silently absorbed.
const tagFilterMax = 10000

// resolveTagFilter turns the parsed `tag:` / `-tag:` tokens into the
// node-ID sets the index filters on, plus the include-set nodes
// themselves (so a bare `tag:x` needs no second round trip).
//
// Several tags AND: a filter narrows. A tag nobody has ever applied
// resolves to an empty include set, and an empty include set with
// Restrict set means "no results" — never "ignore the filter", which
// would answer a typo'd tag with the entire storage.
func resolveTagFilter(ctx context.Context, store db.Store, p search.Parsed) (*search.Filter, []*model.Node, error) {
	if !p.HasTagFilter() {
		return nil, nil, nil
	}
	f := &search.Filter{}
	var included []*model.Node
	for i, tag := range p.Tags {
		nodes, err := store.ListNodesByTag(ctx, tag, tagFilterMax)
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			f.Restrict = true
			included = nodes
			continue
		}
		keep := map[int64]bool{}
		for _, n := range nodes {
			keep[n.ID] = true
		}
		narrowed := included[:0]
		for _, n := range included {
			if keep[n.ID] {
				narrowed = append(narrowed, n)
			}
		}
		included = narrowed
	}
	for _, n := range included {
		f.IncludeIDs = append(f.IncludeIDs, n.ID)
	}
	for _, tag := range p.ExcludeTags {
		nodes, err := store.ListNodesByTag(ctx, tag, tagFilterMax)
		if err != nil {
			return nil, nil, err
		}
		for _, n := range nodes {
			f.ExcludeIDs = append(f.ExcludeIDs, n.ID)
		}
	}
	return f, included, nil
}

// tagFilterAccepts applies a resolved filter to one node ID — the SQL
// LIKE path, where there is no index to push the filter into.
func tagFilterAccepts(f *search.Filter, id int64) bool {
	if f == nil {
		return true
	}
	for _, ex := range f.ExcludeIDs {
		if ex == id {
			return false
		}
	}
	if !f.Restrict {
		return true
	}
	for _, in := range f.IncludeIDs {
		if in == id {
			return true
		}
	}
	return false
}

// sortByRank puts fallback rows in the same tier order the index path
// uses. Without it an index-less install would answer the same query in
// a different order — `ORDER BY name` — and "exact matches rank first"
// would be true on one deployment and false on the next.
//
// The plan carries the prepared query, so the subsequence scorer runs
// once per row rather than once per comparison, and the shorter path
// wins a tie exactly as it does on the index path.
func sortByRank(results []searchResult, plan search.Fallback) {
	sort.SliceStable(results, func(a, b int) bool {
		ra := plan.Rank(results[a].Name, results[a].Path)
		rb := plan.Rank(results[b].Name, results[b].Path)
		if ra != rb {
			return ra < rb
		}
		if len(results[a].Path) != len(results[b].Path) {
			return len(results[a].Path) < len(results[b].Path)
		}
		return results[a].Name < results[b].Name
	})
}

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
	// `tag:` is a FILTER, not a search term (issue #15). It is parsed out
	// of the query string here and resolved against the database, which
	// is the only place tags are current — a tag copied into the search
	// document would go stale the moment somebody re-tagged a file
	// without touching it.
	parsed := search.ParseQuery(req.Query)
	tagFilter, tagged, err := resolveTagFilter(r.Context(), h.Store, parsed)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	results := []searchResult{}
	switch {
	case parsed.HasTagFilter() && parsed.Text == "":
		// A bare `tag:x` is a listing, not a search: there is no text to
		// score, so the tagged nodes ARE the answer (newest first, the
		// order ListNodesByTag already returns them in).
		for _, n := range tagged {
			if req.StorageID != 0 && n.StorageID != req.StorageID {
				continue
			}
			/* wiring:e2 — the marker file stays hidden in name search too */
			if n.Name == e2e.MarkerName {
				continue
			}
			results = append(results, searchResult{Node: n, Matched: search.MatchedName})
			if len(results) >= req.Limit {
				break
			}
		}
	case h.Index != nil:
		hits := h.Index.SafeSearchFiltered(r.Context(), parsed.Text, req.Limit, sc, tagFilter)
		for _, hit := range hits {
			n, err := h.Store.GetNode(r.Context(), hit.NodeID)
			if err == nil && (req.StorageID == 0 || n.StorageID == req.StorageID) {
				/* wiring:e2 — the marker file stays hidden in name search too */
				if n.Name == e2e.MarkerName {
					continue
				}
				results = append(results, searchResult{Node: n, Snippet: hit.Snippet, Matched: hit.Matched})
			}
		}
	}
	// SQL LIKE fallback — name-only by nature, so a content-scoped query
	// never falls back (an index-less install has no content to search).
	//
	// It stays gated on a non-zero storage_id: an unscoped query the
	// index cannot answer would otherwise LIKE-scan every mount in the
	// deployment. That gate is deliberate and predates this change; it is
	// documented in docs/SEARCH.md and left alone here.
	if len(results) == 0 && req.StorageID != 0 && parsed.Text != "" && sc != search.ScopeContent {
		plan := search.PlanFallback(parsed.Text)
		fallback, err := h.Store.SearchNodes(r.Context(), req.StorageID, plan.Like, req.Limit*search.FallbackOverFetch)
		if err == nil {
			for _, n := range fallback {
				/* wiring:e2 — the marker file stays hidden in name search too */
				if n.Name == e2e.MarkerName {
					continue
				}
				if !plan.Accepts(n.Name, n.Path) || !tagFilterAccepts(tagFilter, n.ID) {
					continue
				}
				results = append(results, searchResult{Node: n, Matched: search.MatchedName})
				if len(results) >= req.Limit {
					break
				}
			}
			sortByRank(results, plan)
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

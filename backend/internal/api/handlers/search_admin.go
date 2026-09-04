// Package handlers — search_admin.go
//
// Admin endpoints for the embedded Bleve full-text index.
//
//	GET  /api/admin/search/stats     — index stats
//	POST /api/admin/search/rebuild   — drop and rebuild the index from nodes
package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/search"
)

// SearchAdmin holds the Bleve admin actions.
//
// There is deliberately no rebuild lock here. The index owns that guard,
// because filex also rebuilds on its own when it finds an index written by
// an older document schema, and a handler-local flag could only ever see
// the rebuilds this handler started — the two would happily run at once
// and race to swap their replacement index in.
type SearchAdmin struct {
	Index *search.Index
	Store db.Store
}

// NewSearchAdmin constructs the handler.
func NewSearchAdmin(idx *search.Index, store db.Store) *SearchAdmin {
	return &SearchAdmin{Index: idx, Store: store}
}

// Stats returns index document counts and size.
func (h *SearchAdmin) Stats(w http.ResponseWriter, r *http.Request) {
	if h.Index == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":          false,
			"document_count":   0,
			"index_size_bytes": 0,
		})
		return
	}
	stats := h.Index.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":          true,
		"document_count":   stats.DocCount,
		"index_size_bytes": stats.SizeBytes,
		"last_updated_at":  stats.LastUpdated,
		// needs_rebuild is how an operator finds out that an upgrade
		// added indexed fields their documents do not have yet. Search
		// keeps working without it (the pre-upgrade sub-queries are
		// still part of every query), so this is an invitation, not an
		// alarm — the repair usually runs on its own (see
		// FILEX_SEARCH_AUTO_REBUILD) and this is what says whether it
		// has finished.
		"needs_rebuild": stats.NeedsRebuild,
		// rebuilding lets the admin UI say "rebuilding" instead of
		// showing a needs_rebuild banner over an index that is already
		// being repaired, and looking broken while it happens.
		"rebuilding": stats.Rebuilding,
	})
}

// Rebuild reindexes every node row into a replacement index and swaps it
// in when it is complete. Search keeps answering from the existing index
// for the whole rebuild, and extracted text is carried across, so this is
// no longer the destructive operation it was before v0.30.
//
// ?content=1 additionally re-enqueues content extraction for every
// eligible node once the new index is live — for an operator who has just
// added an extractor or raised FILEX_SEARCH_CONTENT_MAX and wants the text
// derived again rather than copied.
func (h *SearchAdmin) Rebuild(w http.ResponseWriter, r *http.Request) {
	if h.Index == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "search index disabled"})
		return
	}
	withContent := false
	if v := r.URL.Query().Get("content"); v == "1" || strings.EqualFold(v, "true") {
		withContent = true
	}
	// StartRebuild detaches from r.Context — chi cancels it the moment we
	// return from this handler, which would kill the background reindex
	// before it processed a single row — and refuses if a rebuild (this
	// endpoint's, or the automatic schema repair) is already running.
	err := h.Index.StartRebuild(h.Store, search.RebuildOptions{ReExtract: withContent, Reason: "admin"})
	switch {
	case errors.Is(err, search.ErrRebuildInProgress):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rebuild already in progress"})
		return
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	note := "rebuild started in background"
	if withContent {
		note = "rebuild started in background (content extraction re-enqueued)"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":   true,
		"note": note,
	})
}

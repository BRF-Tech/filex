// Package handlers — search_admin.go
//
// Admin endpoints for the embedded Bleve full-text index.
//
//	GET  /api/admin/search/stats     — index stats
//	POST /api/admin/search/rebuild   — drop and rebuild the index from nodes
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/search"
)

// SearchAdmin holds the Bleve admin actions.
type SearchAdmin struct {
	Index *search.Index
	Store db.Store

	// rebuildLock prevents concurrent rebuilds.
	rebuilding atomic.Bool
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
	})
}

// Rebuild drops the existing index and reindexes every node row.
//
// ?content=1 additionally re-enqueues content extraction for every eligible
// node once its metadata lands — a rebuild starts from an EMPTY index, so
// without the flag previously extracted content stays gone until each file
// next drifts.
func (h *SearchAdmin) Rebuild(w http.ResponseWriter, r *http.Request) {
	if h.Index == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "search index disabled"})
		return
	}
	withContent := false
	if v := r.URL.Query().Get("content"); v == "1" || strings.EqualFold(v, "true") {
		withContent = true
	}
	if !h.rebuilding.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rebuild already in progress"})
		return
	}
	// Detach from r.Context — chi cancels it the moment we return from
	// this handler, which would kill the background reindex before it
	// processes a single row. Use context.Background so the goroutine
	// runs to completion (or until container shutdown).
	go func() {
		defer h.rebuilding.Store(false)
		ctx := context.Background()
		var err error
		if withContent {
			err = h.Index.RebuildAllWithContent(ctx, h.Store)
		} else {
			err = h.Index.RebuildAll(ctx, h.Store)
		}
		if err != nil {
			slog.Warn("search: rebuild failed", slog.String("err", err.Error()))
			return
		}
		slog.Info("search: rebuild done", slog.Bool("content", withContent))
	}()
	note := "rebuild started in background"
	if withContent {
		note = "rebuild started in background (content extraction re-enqueued)"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":   true,
		"note": note,
	})
}

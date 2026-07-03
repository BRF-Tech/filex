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
func (h *SearchAdmin) Rebuild(w http.ResponseWriter, r *http.Request) {
	if h.Index == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "search index disabled"})
		return
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
		if err := h.Index.RebuildAll(ctx, h.Store); err != nil {
			slog.Warn("search: rebuild failed", slog.String("err", err.Error()))
			return
		}
		slog.Info("search: rebuild done")
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":   true,
		"note": "rebuild started in background",
	})
}

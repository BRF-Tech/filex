// Package handlers — sync_admin.go
//
// Admin views over sync runs (cross-storage list + per-run detail).
//
//	GET  /api/admin/sync-runs
//	GET  /api/admin/sync-runs/{id}
package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
)

// SyncAdmin handles /api/admin/sync-runs.
type SyncAdmin struct {
	Store db.Store
}

// NewSyncAdmin constructs the handler.
func NewSyncAdmin(store db.Store) *SyncAdmin { return &SyncAdmin{Store: store} }

// List returns recent sync runs across all storages with optional filters.
func (h *SyncAdmin) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var storageID *int64
	if v := q.Get("storage_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			storageID = &id
		}
	}
	status := q.Get("status")

	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	sid := int64(0)
	if storageID != nil {
		sid = *storageID
	}
	runs, total, err := h.Store.ListSyncRunsAcrossAll(r.Context(), sid, status, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": runs,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// Detail returns a single sync run with conflict info.
func (h *SyncAdmin) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	run, err := h.Store.GetSyncRun(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	conflicts, _ := h.Store.ListSyncConflictsByRun(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"run":       run,
		"conflicts": conflicts,
	})
}

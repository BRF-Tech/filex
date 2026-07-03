package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/queue"
)

// Queue exposes admin endpoints over the persistent queue driver.
//
// Only admin role hits these — wired under /api/admin/queue/... in
// routes.go behind auth.Middleware + auth.RequireAdmin.
type Queue struct {
	Driver queue.Driver
}

// NewQueue constructs the handler. driver may be nil when the bootstrap
// disabled the queue (config.Queue.Enabled = false) — handlers respond
// 503 in that case so the UI can render a graceful warning.
func NewQueue(driver queue.Driver) *Queue {
	return &Queue{Driver: driver}
}

// Stats returns the dashboard counters.
//
//	GET /api/admin/queue/stats
func (h *Queue) Stats(w http.ResponseWriter, r *http.Request) {
	if h.Driver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "queue offline"})
		return
	}
	s, err := h.Driver.Stats(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// List paginates ops with optional status filter.
//
//	GET /api/admin/queue?status=pending&limit=50&offset=0
func (h *Queue) List(w http.ResponseWriter, r *http.Request) {
	if h.Driver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "queue offline"})
		return
	}
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	ops, total, err := h.Driver.List(r.Context(), status, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  ops,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// Get returns a single op.
//
//	GET /api/admin/queue/{id}
func (h *Queue) Get(w http.ResponseWriter, r *http.Request) {
	if h.Driver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "queue offline"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	op, err := h.Driver.Get(r.Context(), id)
	if err == queue.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// Retry rolls a failed op back into pending.
//
//	POST /api/admin/queue/{id}/retry
func (h *Queue) Retry(w http.ResponseWriter, r *http.Request) {
	if h.Driver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "queue offline"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	if err := h.Driver.Retry(r.Context(), id); err != nil {
		if err == queue.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found or not failed"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Cancel marks a pending op cancelled.
//
//	DELETE /api/admin/queue/{id}
func (h *Queue) Cancel(w http.ResponseWriter, r *http.Request) {
	if h.Driver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "queue offline"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	if err := h.Driver.Cancel(r.Context(), id); err != nil {
		if err == queue.ErrNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/queue"
)

// Queue exposes admin endpoints over the persistent queue driver.
//
// Only admin role hits these — wired under /api/admin/queue/... in
// routes.go behind auth.Middleware + auth.RequireAdmin.
type Queue struct {
	Driver queue.Driver
	// Store names what a job is about (see subjectOf). Nil leaves the
	// payload as the only description.
	Store db.Store
}

// newQueueWithStore is NewQueue with its store attached (the AI admin copy).
func newQueueWithStore(driver queue.Driver, store db.Store) *Queue {
	h := NewQueue(driver)
	h.Store = store
	return h
}

// AttachStore wires the store the list names its jobs' subjects from.
func (h *Queue) AttachStore(s db.Store) { h.Store = s }

// queueRow is one listed job plus WHAT it is about, in words.
//
// ⚠ The Queue page's "Content" column printed the payload as JSON —
// `{"node_id":8}` (release-candidate sweep, 2026-09-21): an internal id the
// operator cannot look up anywhere. `subject` is the file's path (with its
// storage) or the storage's name the payload points at.
type queueRow struct {
	queue.Op
	Subject string `json:"subject,omitempty"`
}

// subjectOf reads the payload keys the queue's producers write: node_id
// (content index, antivirus scan), storage_id + path (replica), storage_id.
func (h *Queue) subjectOf(ctx context.Context, p map[string]any, storages map[int64]string) string {
	num := func(k string) int64 {
		switch v := p[k].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
		return 0
	}
	if id := num("node_id"); id > 0 && h.Store != nil {
		if n, err := h.Store.GetNode(ctx, id); err == nil && n != nil {
			if st := storages[n.StorageID]; st != "" {
				return st + ":" + n.Path
			}
			return n.Path
		}
	}
	path, _ := p["path"].(string)
	st := storages[num("storage_id")]
	switch {
	case path != "" && st != "":
		return st + ":" + path
	case path != "":
		return path
	}
	return st
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
	if !requireSupertenant(w, r, "the job queue is instance-wide and its payloads name every tenant's paths") {
		return
	}
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
	if !requireSupertenant(w, r, "the job queue is instance-wide and its payloads name every tenant's paths") {
		return
	}
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
	storages := map[int64]string{}
	if h.Store != nil {
		if list, lerr := h.Store.ListStorages(r.Context()); lerr == nil {
			for _, st := range list {
				storages[st.ID] = st.Name
			}
		}
	}
	rows := make([]queueRow, 0, len(ops))
	for _, op := range ops {
		rows = append(rows, queueRow{Op: op, Subject: h.subjectOf(r.Context(), op.Payload, storages)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// Get returns a single op.
//
//	GET /api/admin/queue/{id}
func (h *Queue) Get(w http.ResponseWriter, r *http.Request) {
	if !requireSupertenant(w, r, "the job queue is instance-wide and its payloads name every tenant's paths") {
		return
	}
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
	if !requireSupertenant(w, r, "the job queue is instance-wide and its payloads name every tenant's paths") {
		return
	}
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
	if !requireSupertenant(w, r, "the job queue is instance-wide and its payloads name every tenant's paths") {
		return
	}
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

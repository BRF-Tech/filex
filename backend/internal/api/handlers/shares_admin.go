// Package handlers — shares_admin.go
//
// Admin views/actions over all shares (every user, not just current user).
//
//	GET    /api/admin/shares
//	POST   /api/admin/shares/{id}/revoke
//	DELETE /api/admin/shares/{id}
package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
)

// SharesAdmin handles /api/admin/shares.
type SharesAdmin struct {
	Store db.Store
}

// NewSharesAdmin constructs the handler.
func NewSharesAdmin(store db.Store) *SharesAdmin { return &SharesAdmin{Store: store} }

// List returns all shares with optional creator/active filters.
func (h *SharesAdmin) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var creatorID *int64
	if v := q.Get("creator_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			creatorID = &id
		}
	}
	activeOnly := q.Get("active") == "true"

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

	rows, total, err := h.Store.ListAllShares(r.Context(), creatorID, activeOnly, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Dual envelope: `items/total/page/page_size` is what the admin
	// SPA expects (PaginatedResponse); `entries/limit/offset` keeps
	// any older consumers happy.
	pageSize := limit
	if pageSize <= 0 {
		pageSize = 1
	}
	page := (offset / pageSize) + 1
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		// Legacy aliases:
		"entries": rows,
		"limit":   limit,
		"offset":  offset,
	})
}

// Revoke soft-revokes by setting expiration to now (keeps audit trail).
func (h *SharesAdmin) Revoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Store.RevokeShare(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Delete hard-deletes a share row.
func (h *SharesAdmin) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Store.DeleteShare(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

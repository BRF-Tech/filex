// Package handlers — audit.go
//
// GET /api/admin/audit?user_id=&action=&from=&to=&limit=50&offset=0
package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
)

// Audit handles /api/admin/audit.
type Audit struct {
	Store db.Store
}

// NewAudit constructs the handler.
func NewAudit(store db.Store) *Audit { return &Audit{Store: store} }

// List returns paginated audit entries with optional filters.
func (h *Audit) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	var userID *int64
	if v := q.Get("user_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			userID = &id
		}
	}
	action := q.Get("action")

	var from, to *time.Time
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = &t
		}
	}

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

	entries, total, err := h.Store.ListAuditFiltered(ctx, userID, action, from, to, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []*db.AuditEntryWithUser{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

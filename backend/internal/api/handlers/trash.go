// Package handlers — trash.go
//
// Endpoints:
//
//	POST /api/files/manager/restore                       (auth)  body {node_id}
//	POST /api/admin/trash/empty?older_than_days=N         (admin) immediate purge
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"gitlab.com/brftech/filemanager/backend/internal/trash"
)

// Trash wires trash retention HTTP routes.
type Trash struct {
	Service *trash.Service
}

// NewTrash constructs the handler.
func NewTrash(svc *trash.Service) *Trash { return &Trash{Service: svc} }

type restoreNodeReq struct {
	NodeID int64 `json:"node_id"`
}

// Restore lifts the deleted_at flag on a soft-deleted node.
func (h *Trash) Restore(w http.ResponseWriter, r *http.Request) {
	var req restoreNodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node_id"})
		return
	}
	if err := h.Service.Restore(r.Context(), req.NodeID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminEmpty triggers an immediate purge.
//
// Optional ?older_than_days=N parameter narrows the window — older_than_days=0
// (the default) wipes everything currently soft-deleted regardless of age.
func (h *Trash) AdminEmpty(w http.ResponseWriter, r *http.Request) {
	older := 0
	if v := r.URL.Query().Get("older_than_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			older = n
		}
	}
	res, err := h.Service.EmptyOlderThan(r.Context(), older)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

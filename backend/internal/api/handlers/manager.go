// Package handlers contains one file per logical HTTP route group.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"gitlab.com/brftech/filemanager/backend/internal/db"
)

// Manager handles read-only browsing endpoints under /api/files/manager.
type Manager struct {
	Store db.Store
}

// NewManager constructs a Manager handler.
func NewManager(store db.Store) *Manager { return &Manager{Store: store} }

// List returns the children of a node by ID, or root if no id given.
//
// Query: ?storage=<id>&parent=<id>
func (h *Manager) List(w http.ResponseWriter, r *http.Request) {
	storageID, err := strconv.ParseInt(r.URL.Query().Get("storage"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage id"})
		return
	}
	var parentPtr *int64
	if v := r.URL.Query().Get("parent"); v != "" {
		pid, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad parent id"})
			return
		}
		parentPtr = &pid
	}
	nodes, err := h.Store.ListNodesByParent(r.Context(), storageID, parentPtr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
	})
}

// Stat returns metadata for a single node.
//
// Query: ?id=<id>
func (h *Manager) Stat(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	node, err := h.Store.GetNode(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, node)
}

// Read streams a file by node ID. Inline disposition for previewable mimes.
//
// Wired in routes.go — actual streaming happens via the storage resolver.
// This stub returns 501 until the storage resolver is propagated through
// to Manager (V2 cleanup).
func (h *Manager) Read(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"manager.read: TODO — call /api/files/share/<token> instead"}`))
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

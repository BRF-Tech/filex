// Package handlers — versions.go
//
// Endpoints under /api/files/versions.
//
//	GET    /api/files/versions?node_id=…             (auth)  list snapshots
//	POST   /api/files/versions/restore               (auth)  restore one
//	DELETE /api/files/versions/{id}                  (admin) hard delete
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// Versions wraps version-history HTTP routes.
type Versions struct {
	Store   db.Store
	Service *versioning.Service
}

// NewVersions constructs the handler.
func NewVersions(store db.Store, svc *versioning.Service) *Versions {
	return &Versions{Store: store, Service: svc}
}

// List returns the version timeline for a node.
func (h *Versions) List(w http.ResponseWriter, r *http.Request) {
	nodeID, err := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	if err != nil || nodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad node_id"})
		return
	}
	versions, err := h.Service.List(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if versions == nil {
		versions = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"versions": versions,
		"node_id":  nodeID,
	})
}

type restoreReq struct {
	NodeID          int64 `json:"node_id"`
	VersionID       int64 `json:"version_id"`
	SnapshotCurrent bool  `json:"snapshot_current,omitempty"`
}

// snapshotReq is the POST /api/files/versions/snapshot body.
type snapshotReq struct {
	NodeID int64 `json:"node_id"`
}

// Snapshot records the node's current content as a new version on demand
// (the inspector's "take a version now" button; writes normally snapshot
// implicitly, this is the explicit user-triggered path).
func (h *Versions) Snapshot(w http.ResponseWriter, r *http.Request) {
	var req snapshotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	v, err := h.Service.Snapshot(r.Context(), req.NodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": v})
}

// Restore replaces the live content with a recorded version.
func (h *Versions) Restore(w http.ResponseWriter, r *http.Request) {
	var req restoreReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID <= 0 || req.VersionID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
	}
	if err := h.Service.Restore(r.Context(), req.NodeID, req.VersionID, req.SnapshotCurrent); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HardDelete erases a version row + its storage object (admin only).
func (h *Versions) HardDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Service.HardDeleteVersion(r.Context(), id); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no rows in result set") || strings.Contains(msg, "not found") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "version not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

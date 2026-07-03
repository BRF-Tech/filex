// Package handlers — replication_targets.go
//
// /api/admin/replication-targets — CRUD for the new backup-only
// entity. Replication targets are NOT regular storages: operators
// never write to them, they don't show up in the Depolar list, and
// the file explorer never lists them as a virtual root. Each entry
// is a backup sink that a primary storage can fan its writes out to
// via `storages.replica_target_id`.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

type ReplicationTargets struct {
	Store db.Store
}

func NewReplicationTargets(store db.Store) *ReplicationTargets {
	return &ReplicationTargets{Store: store}
}

func (h *ReplicationTargets) List(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.ListReplicationTargets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if out == nil {
		out = []*model.ReplicationTarget{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ReplicationTargets) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	rt, err := h.Store.GetReplicationTarget(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (h *ReplicationTargets) Create(w http.ResponseWriter, r *http.Request) {
	var rt model.ReplicationTarget
	if err := json.NewDecoder(r.Body).Decode(&rt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if rt.Name == "" || rt.Driver == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and driver required"})
		return
	}
	if rt.Mode == "" {
		rt.Mode = "async"
	}
	if !rt.Enabled {
		// Default enabled=true unless explicitly turned off so the
		// fan-out engine picks the row up immediately. JSON
		// `enabled:false` still wins.
		rt.Enabled = true
	}
	created, err := h.Store.CreateReplicationTarget(r.Context(), &rt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *ReplicationTargets) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var rt model.ReplicationTarget
	if err := json.NewDecoder(r.Body).Decode(&rt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	rt.ID = id
	if err := h.Store.UpdateReplicationTarget(r.Context(), &rt); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	fresh, _ := h.Store.GetReplicationTarget(r.Context(), id)
	writeJSON(w, http.StatusOK, fresh)
}

func (h *ReplicationTargets) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := h.Store.DeleteReplicationTarget(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
